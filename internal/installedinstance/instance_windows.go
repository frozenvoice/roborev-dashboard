//go:build windows

package installedinstance

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/Microsoft/go-winio"
	"golang.org/x/sys/windows"
)

const (
	maxMessageBytes = 16 * 1024
	processWaitPoll = 25 * time.Millisecond
)

type winPlatform struct {
	config       Config
	callbacks    Callbacks
	canonicalExe string
	fileID       fileIdentity
	userSID      string
	pipeName     string
	mutexName    string
	mutex        windows.Handle
	instanceID   string

	mu       sync.RWMutex
	status   Status
	stopOnce sync.Once
	stopCh   chan struct{}
	closed   chan struct{}
	activeMu sync.Mutex
	listener net.Listener
	active   net.Conn
}

type pipeRequest struct {
	Version    int    `json:"version"`
	Operation  string `json:"operation"`
	Notify     bool   `json:"notify,omitempty"`
	PID        uint32 `json:"pid,omitempty"`
	InstanceID string `json:"instanceId,omitempty"`
}

type pipeResponse struct {
	OK     bool   `json:"ok"`
	Error  string `json:"error,omitempty"`
	Status Status `json:"status"`
}

type fileIdentity struct {
	volume uint32
	high   uint32
	low    uint32
}

func acquirePlatform(config Config, callbacks Callbacks) (managerPlatform, error) {
	exe, err := canonicalPath(config.ExecutablePath)
	if err != nil {
		return nil, fmt.Errorf("canonicalize executable: %w", err)
	}
	identity, err := executableIdentity(exe)
	if err != nil {
		return nil, fmt.Errorf("identify executable: %w", err)
	}
	sid, err := currentUserSID()
	if err != nil {
		return nil, fmt.Errorf("identify current user: %w", err)
	}
	nameHash := scopedHash(sid, exe)
	mutexName := `Global\RoboRevDashboard-` + nameHash
	pipeName := `\\.\pipe\RoboRevDashboard-` + nameHash
	mutexPtr, err := windows.UTF16PtrFromString(mutexName)
	if err != nil {
		return nil, err
	}
	// The mutex is an existence lease. It is intentionally not acquired by
	// the creating goroutine because Windows mutex ownership is thread-affine
	// while this manager can be closed from a shutdown goroutine.
	mutex, createErr := windows.CreateMutex(nil, false, mutexPtr)
	if mutex == 0 {
		return nil, fmt.Errorf("create instance lease: %w", createErr)
	}
	if errors.Is(createErr, windows.ERROR_ALREADY_EXISTS) {
		_ = windows.CloseHandle(mutex)
		return nil, ErrAlreadyRunning
	}
	if createErr != nil {
		_ = windows.CloseHandle(mutex)
		return nil, fmt.Errorf("create instance lease: %w", createErr)
	}
	listener, err := createControlListener(pipeName, sid)
	if err != nil {
		_ = windows.CloseHandle(mutex)
		return nil, fmt.Errorf("create control pipe: %w", err)
	}

	instanceID, err := randomID()
	if err != nil {
		_ = listener.Close()
		_ = windows.CloseHandle(mutex)
		return nil, err
	}
	p := &winPlatform{
		config: config, callbacks: callbacks, canonicalExe: exe, fileID: identity,
		userSID: sid, pipeName: pipeName, mutexName: mutexName, mutex: mutex,
		instanceID: instanceID, stopCh: make(chan struct{}), closed: make(chan struct{}),
		listener: listener,
	}
	p.status = Status{Running: true, PID: uint32(os.Getpid()), InstanceID: instanceID, ExecutablePath: exe}
	return p, nil
}

func createControlListener(pipeName, sid string) (net.Listener, error) {
	return winio.ListenPipe(pipeName, &winio.PipeConfig{
		// The named-pipe DACL is limited to the current SID. Requests are
		// additionally checked against the connecting PID's image identity.
		SecurityDescriptor: "D:P(A;;GA;;;" + sid + ")",
		MessageMode:        true,
		InputBufferSize:    maxMessageBytes,
		OutputBufferSize:   maxMessageBytes,
	})
}

func queryPlatform(ctx context.Context, config Config) (Status, error) {
	response, err := request(ctx, config, pipeRequest{Version: 1, Operation: "status"})
	if errors.Is(err, ErrNoInstance) {
		return Status{}, nil
	}
	if err != nil {
		return Status{}, err
	}
	if !response.OK {
		if response.Error == "" {
			response.Error = "installed instance returned an invalid status response"
		}
		return response.Status, errors.New(response.Error)
	}
	return response.Status, nil
}

func activatePlatform(ctx context.Context, config Config, notify bool) (Status, error) {
	deadline := time.Now().Add(config.StartupTimeout)
	for {
		response, err := request(ctx, config, pipeRequest{Version: 1, Operation: "activate", Notify: notify})
		if err == nil {
			if response.OK {
				return response.Status, nil
			}
			if response.Error != "starting" {
				return response.Status, errors.New(response.Error)
			}
		} else if !errors.Is(err, ErrNoInstance) {
			return Status{}, err
		}
		if time.Now().After(deadline) {
			return Status{}, fmt.Errorf("installed Dashboard did not become ready within %s", config.StartupTimeout)
		}
		select {
		case <-ctx.Done():
			return Status{}, ctx.Err()
		case <-time.After(40 * time.Millisecond):
		}
	}
}

func stopPlatform(ctx context.Context, config Config) error {
	response, err := request(ctx, config, pipeRequest{Version: 1, Operation: "status"})
	if errors.Is(err, ErrNoInstance) {
		return nil
	}
	if err != nil {
		return err
	}
	if !response.OK || !response.Status.Running {
		if response.Error == "" {
			response.Error = "installed instance returned a non-running status"
		}
		return errors.New(response.Error)
	}
	if response.Status.PID == 0 || response.Status.InstanceID == "" || response.Status.ExecutablePath == "" {
		return errors.New("installed instance returned incomplete status")
	}
	targetPID := response.Status.PID
	targetInstanceID := response.Status.InstanceID
	process, err := openAuthenticatedTarget(config, response.Status)
	if err != nil {
		return err
	}
	defer windows.CloseHandle(process)
	response, err = request(ctx, config, pipeRequest{Version: 1, Operation: "shutdown", PID: targetPID, InstanceID: targetInstanceID})
	if err != nil {
		return err
	}
	if !response.OK {
		if response.Error == "" {
			response.Error = "installed instance rejected shutdown"
		}
		return errors.New(response.Error)
	}
	if response.Status.PID != targetPID || response.Status.InstanceID != targetInstanceID {
		return errors.New("installed shutdown response has an invalid process identity")
	}
	return waitProcessExitHandle(ctx, process, config.ShutdownTimeout)
}

func (p *winPlatform) Run(ctx context.Context) error {
	defer close(p.closed)
	p.activeMu.Lock()
	listener := p.listener
	p.activeMu.Unlock()
	if listener == nil {
		return errors.New("control pipe is not ready")
	}
	defer listener.Close()
	for {
		select {
		case <-ctx.Done():
			_ = p.Close()
			return ctx.Err()
		case <-p.stopCh:
			return nil
		default:
		}
		pipe, err := listener.Accept()
		if err != nil {
			select {
			case <-p.stopCh:
				return nil
			default:
			}
			_ = p.Close()
			return fmt.Errorf("accept control pipe: %w", err)
		}
		p.activeMu.Lock()
		p.active = pipe
		p.activeMu.Unlock()
		done := make(chan struct{})
		go func() {
			p.handlePipe(pipe)
			close(done)
		}()
		timer := time.NewTimer(p.config.RequestTimeout)
		select {
		case <-done:
			if !timer.Stop() {
				<-timer.C
			}
		case <-timer.C:
			_ = pipe.Close()
			<-done
		}
		_ = pipe.Close()
		p.clearActive(pipe)
	}
}

func (p *winPlatform) handlePipe(conn net.Conn) {
	if !p.authenticatePeer(conn) {
		return
	}
	request, err := readRequest(conn)
	if err != nil {
		return
	}
	response := pipeResponse{Status: p.Status()}
	var afterAck func()
	if request.Version != 1 {
		response.Error = "unsupported protocol"
	} else {
		switch request.Operation {
		case "status":
			response.OK = response.Status.Running
		case "activate":
			if response.Status.URL == "" {
				response.Error = "starting"
			} else {
				response.OK = true
				if request.Notify && p.callbacks.Activate != nil {
					status := response.Status
					afterAck = func() { go p.callbacks.Activate(status) }
				}
			}
		case "shutdown":
			if request.PID != response.Status.PID || request.InstanceID == "" || request.InstanceID != response.Status.InstanceID {
				response.Error = "instance identity mismatch"
			} else {
				response.OK = true
				if p.callbacks.Shutdown != nil {
					afterAck = func() {
						go func() {
							shutdownCtx, cancel := context.WithTimeout(context.Background(), p.config.ShutdownTimeout)
							defer cancel()
							_ = p.callbacks.Shutdown(shutdownCtx)
						}()
					}
				}
			}
		default:
			response.Error = "unsupported operation"
		}
	}
	if err := writeResponse(conn, response); err == nil && afterAck != nil {
		afterAck()
	}
}

func (p *winPlatform) authenticatePeer(conn net.Conn) bool {
	fdProvider, ok := conn.(interface{ Fd() uintptr })
	if !ok {
		return false
	}
	pipe := windows.Handle(fdProvider.Fd())
	var pid uint32
	if windows.GetNamedPipeClientProcessId(pipe, &pid) != nil || pid == 0 {
		return false
	}
	peer, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, pid)
	if err != nil {
		return false
	}
	defer windows.CloseHandle(peer)
	path, err := processImagePath(peer)
	if err != nil {
		return false
	}
	canonical, err := canonicalPath(path)
	if err != nil || !strings.EqualFold(canonical, p.canonicalExe) {
		return false
	}
	identity, err := executableIdentity(canonical)
	if err != nil || identity != p.fileID {
		return false
	}
	var token windows.Token
	err = windows.OpenProcessToken(peer, windows.TOKEN_QUERY, &token)
	if err != nil {
		return false
	}
	defer token.Close()
	user, err := token.GetTokenUser()
	return err == nil && user != nil && user.User.Sid != nil && user.User.Sid.String() == p.userSID
}

func (p *winPlatform) SetStatus(status Status) {
	p.mu.Lock()
	defer p.mu.Unlock()
	status.PID = uint32(os.Getpid())
	status.InstanceID = p.instanceID
	status.ExecutablePath = p.canonicalExe
	status.Running = true
	p.status = status
}

func (p *winPlatform) Status() Status {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.status
}

func (p *winPlatform) clearActive(conn net.Conn) {
	p.activeMu.Lock()
	if p.active == conn {
		p.active = nil
	}
	p.activeMu.Unlock()
}

func (p *winPlatform) Close() error {
	var err error
	p.stopOnce.Do(func() {
		close(p.stopCh)
		p.activeMu.Lock()
		if p.active != nil {
			_ = p.active.Close()
			p.active = nil
		}
		if p.listener != nil {
			_ = p.listener.Close()
			p.listener = nil
		}
		p.activeMu.Unlock()
		if p.mutex != 0 {
			err = windows.CloseHandle(p.mutex)
			p.mutex = 0
		}
	})
	return err
}

func request(ctx context.Context, config Config, req pipeRequest) (pipeResponse, error) {
	exe, err := canonicalPath(config.ExecutablePath)
	if err != nil {
		return pipeResponse{}, err
	}
	sid, err := currentUserSID()
	if err != nil {
		return pipeResponse{}, err
	}
	identity, err := executableIdentity(exe)
	if err != nil {
		return pipeResponse{}, fmt.Errorf("identify client executable: %w", err)
	}
	hash := scopedHash(sid, exe)
	pipeName := `\\.\pipe\RoboRevDashboard-` + hash
	deadlineCtx, cancel := context.WithTimeout(ctx, config.RequestTimeout)
	defer cancel()
	file, err := connectPipe(deadlineCtx, pipeName)
	if err != nil {
		if isMissingPipe(err) {
			return pipeResponse{}, ErrNoInstance
		}
		return pipeResponse{}, fmt.Errorf("connect installed control pipe: %w", err)
	}
	defer file.Close()
	deadline := time.Now().Add(config.RequestTimeout)
	if ctxDeadline, ok := ctx.Deadline(); ok && ctxDeadline.Before(deadline) {
		deadline = ctxDeadline
	}
	if err := file.SetDeadline(deadline); err != nil {
		return pipeResponse{}, fmt.Errorf("bound installed control pipe: %w", err)
	}
	serverPID, err := authenticateServer(file, exe, identity, sid)
	if err != nil {
		return pipeResponse{}, err
	}
	if err := writeRequest(file, req); err != nil {
		return pipeResponse{}, fmt.Errorf("write installed control request: %w", err)
	}
	response, err := readResponse(file)
	if err != nil {
		return pipeResponse{}, fmt.Errorf("read installed control response: %w", err)
	}
	if response.Status.PID == 0 || response.Status.PID != serverPID {
		return pipeResponse{}, errors.New("installed control response has an invalid server PID")
	}
	return response, nil
}

func connectPipe(ctx context.Context, name string) (net.Conn, error) {
	return winio.DialPipeContext(ctx, name)
}

func authenticateServer(conn net.Conn, expectedPath string, expectedID fileIdentity, expectedSID string) (uint32, error) {
	fdProvider, ok := conn.(interface{ Fd() uintptr })
	if !ok {
		return 0, errors.New("installed control pipe has no native handle")
	}
	pipe := windows.Handle(fdProvider.Fd())
	var pid uint32
	if err := windows.GetNamedPipeServerProcessId(pipe, &pid); err != nil || pid == 0 {
		if err == nil {
			err = errors.New("named pipe server PID is empty")
		}
		return 0, fmt.Errorf("authenticate installed control server PID: %w", err)
	}
	process, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, pid)
	if err != nil {
		return 0, fmt.Errorf("open installed control server process: %w", err)
	}
	defer windows.CloseHandle(process)
	path, err := processImagePath(process)
	if err != nil {
		return 0, fmt.Errorf("read installed control server image: %w", err)
	}
	canonical, err := canonicalPath(path)
	if err != nil || !strings.EqualFold(canonical, expectedPath) {
		if err == nil {
			err = errors.New("server executable path does not match")
		}
		return 0, fmt.Errorf("authenticate installed control server image: %w", err)
	}
	identity, err := executableIdentity(canonical)
	if err != nil {
		return 0, fmt.Errorf("read installed control server file identity: %w", err)
	}
	if identity != expectedID {
		return 0, errors.New("installed control server file identity does not match")
	}
	var token windows.Token
	if err := windows.OpenProcessToken(process, windows.TOKEN_QUERY, &token); err != nil {
		return 0, fmt.Errorf("read installed control server user: %w", err)
	}
	defer token.Close()
	user, err := token.GetTokenUser()
	if err != nil || user == nil || user.User.Sid == nil || user.User.Sid.String() != expectedSID {
		if err == nil {
			err = errors.New("server user SID does not match")
		}
		return 0, fmt.Errorf("authenticate installed control server user: %w", err)
	}
	return pid, nil
}

func readRequest(file io.Reader) (pipeRequest, error) {
	var request pipeRequest
	line, err := readLine(file)
	if err != nil {
		return request, err
	}
	err = decodeStrict(line, &request)
	return request, err
}

func readResponse(file io.Reader) (pipeResponse, error) {
	var response pipeResponse
	line, err := readLine(file)
	if err != nil {
		return response, err
	}
	err = decodeStrict(line, &response)
	return response, err
}

func writeRequest(file io.Writer, request pipeRequest) error {
	encoded, err := json.Marshal(request)
	if err != nil {
		return err
	}
	encoded = append(encoded, '\n')
	if len(encoded) > maxMessageBytes {
		return errors.New("control request is too large")
	}
	_, err = file.Write(encoded)
	return err
}

func writeResponse(file io.Writer, response pipeResponse) error {
	encoded, err := json.Marshal(response)
	if err != nil {
		return err
	}
	encoded = append(encoded, '\n')
	if len(encoded) > maxMessageBytes {
		return errors.New("control response is too large")
	}
	_, err = file.Write(encoded)
	return err
}

func readLine(file io.Reader) ([]byte, error) {
	reader := bufio.NewReaderSize(file, 1024)
	var output []byte
	for len(output) < maxMessageBytes {
		chunk, err := reader.ReadBytes('\n')
		output = append(output, chunk...)
		if len(output) > maxMessageBytes {
			return nil, errors.New("control message is too large")
		}
		if len(chunk) > 0 && chunk[len(chunk)-1] == '\n' {
			return output[:len(output)-1], nil
		}
		if err != nil {
			return nil, err
		}
	}
	return nil, errors.New("control message is too large")
}

func decodeStrict(encoded []byte, target any) error {
	if err := rejectDuplicateKeys(encoded); err != nil {
		return err
	}
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			return errors.New("trailing JSON value")
		}
		return err
	}
	return nil
}

func rejectDuplicateKeys(encoded []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	if err := walkJSONValue(decoder); err != nil {
		return err
	}
	var trailing json.Token
	if token, err := decoder.Token(); err != io.EOF {
		if err == nil {
			trailing = token
			return fmt.Errorf("trailing JSON token %v", trailing)
		}
		return err
	}
	return nil
}

func walkJSONValue(decoder *json.Decoder) error {
	token, err := decoder.Token()
	if err != nil {
		return err
	}
	switch delimiter := token.(type) {
	case json.Delim:
		switch delimiter {
		case '{':
			seen := make(map[string]struct{})
			for decoder.More() {
				keyToken, err := decoder.Token()
				if err != nil {
					return err
				}
				key, ok := keyToken.(string)
				if !ok {
					return errors.New("JSON object key is not a string")
				}
				if _, exists := seen[key]; exists {
					return fmt.Errorf("duplicate JSON field %q", key)
				}
				seen[key] = struct{}{}
				if err := walkJSONValue(decoder); err != nil {
					return err
				}
			}
			_, err = decoder.Token()
			return err
		case '[':
			for decoder.More() {
				if err := walkJSONValue(decoder); err != nil {
					return err
				}
			}
			_, err = decoder.Token()
			return err
		default:
			return fmt.Errorf("unexpected JSON delimiter %q", delimiter)
		}
	}
	return nil
}

func openAuthenticatedTarget(config Config, status Status) (windows.Handle, error) {
	executable, err := canonicalPath(config.ExecutablePath)
	if err != nil {
		return 0, fmt.Errorf("canonicalize stop target: %w", err)
	}
	if !strings.EqualFold(executable, status.ExecutablePath) {
		return 0, errors.New("installed status executable path does not match this executable")
	}
	identity, err := executableIdentity(executable)
	if err != nil {
		return 0, fmt.Errorf("identify stop target executable: %w", err)
	}
	sid, err := currentUserSID()
	if err != nil {
		return 0, fmt.Errorf("identify stop target user: %w", err)
	}
	process, err := windows.OpenProcess(windows.SYNCHRONIZE|windows.PROCESS_QUERY_LIMITED_INFORMATION, false, status.PID)
	if err != nil {
		return 0, fmt.Errorf("open installed Dashboard process %d: %w", status.PID, err)
	}
	path, err := processImagePath(process)
	if err != nil {
		_ = windows.CloseHandle(process)
		return 0, fmt.Errorf("read installed Dashboard process image: %w", err)
	}
	canonical, err := canonicalPath(path)
	if err != nil || !strings.EqualFold(canonical, executable) {
		_ = windows.CloseHandle(process)
		if err == nil {
			err = errors.New("process executable path does not match")
		}
		return 0, fmt.Errorf("authenticate installed Dashboard process image: %w", err)
	}
	actualIdentity, err := executableIdentity(canonical)
	if err != nil || actualIdentity != identity {
		_ = windows.CloseHandle(process)
		if err == nil {
			err = errors.New("process executable file identity does not match")
		}
		return 0, fmt.Errorf("authenticate installed Dashboard process file: %w", err)
	}
	var token windows.Token
	if err := windows.OpenProcessToken(process, windows.TOKEN_QUERY, &token); err != nil {
		_ = windows.CloseHandle(process)
		return 0, fmt.Errorf("read installed Dashboard process user: %w", err)
	}
	user, tokenErr := token.GetTokenUser()
	_ = token.Close()
	if tokenErr != nil || user == nil || user.User.Sid == nil || user.User.Sid.String() != sid {
		_ = windows.CloseHandle(process)
		if tokenErr == nil {
			tokenErr = errors.New("process user SID does not match")
		}
		return 0, fmt.Errorf("authenticate installed Dashboard process user: %w", tokenErr)
	}
	return process, nil
}

func waitProcessExitHandle(ctx context.Context, process windows.Handle, timeout time.Duration) error {
	if process == 0 {
		return errors.New("installed Dashboard process handle is empty")
	}
	deadline := time.Now().Add(timeout)
	for {
		remaining := time.Until(deadline)
		if remaining <= 0 {
			return context.DeadlineExceeded
		}
		if ctx != nil {
			select {
			case <-ctx.Done():
				return ctx.Err()
			default:
			}
		}
		wait := uint32(remaining / time.Millisecond)
		if wait == 0 {
			wait = 1
		}
		if wait > 100 {
			wait = 100
		}
		result, waitErr := windows.WaitForSingleObject(process, wait)
		if waitErr != nil {
			return waitErr
		}
		if result == windows.WAIT_OBJECT_0 {
			return nil
		}
		if result != uint32(windows.WAIT_TIMEOUT) {
			return fmt.Errorf("wait for Dashboard process: %d", result)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(processWaitPoll):
		}
	}
}

func currentUserSID() (string, error) {
	var token windows.Token
	err := windows.OpenProcessToken(windows.CurrentProcess(), windows.TOKEN_QUERY, &token)
	if err != nil {
		return "", err
	}
	defer token.Close()
	user, err := token.GetTokenUser()
	if err != nil || user == nil || user.User.Sid == nil {
		if err == nil {
			err = errors.New("token has no user SID")
		}
		return "", err
	}
	sid := user.User.Sid.String()
	if sid == "" {
		return "", errors.New("token user SID is empty")
	}
	return sid, nil
}

func processImagePath(process windows.Handle) (string, error) {
	buffer := make([]uint16, windows.MAX_PATH)
	for len(buffer) <= 32768 {
		size := uint32(len(buffer))
		err := windows.QueryFullProcessImageName(process, 0, &buffer[0], &size)
		if err == nil {
			return windows.UTF16ToString(buffer[:size]), nil
		}
		if !errors.Is(err, windows.ERROR_INSUFFICIENT_BUFFER) {
			return "", err
		}
		buffer = make([]uint16, len(buffer)*2)
	}
	return "", errors.New("process image path is too long")
}

func canonicalPath(path string) (string, error) {
	if strings.TrimSpace(path) == "" {
		return "", errors.New("executable path is empty")
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	resolved, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return "", err
	}
	return filepath.Clean(resolved), nil
}

func executableIdentity(path string) (fileIdentity, error) {
	ptr, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return fileIdentity{}, err
	}
	handle, err := windows.CreateFile(ptr, windows.GENERIC_READ, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE, nil, windows.OPEN_EXISTING, windows.FILE_ATTRIBUTE_NORMAL, 0)
	if err != nil {
		return fileIdentity{}, err
	}
	defer windows.CloseHandle(handle)
	var info windows.ByHandleFileInformation
	if err := windows.GetFileInformationByHandle(handle, &info); err != nil {
		return fileIdentity{}, err
	}
	return fileIdentity{volume: info.VolumeSerialNumber, high: info.FileIndexHigh, low: info.FileIndexLow}, nil
}

func scopedHash(sid, path string) string {
	hash := sha256.Sum256([]byte(strings.ToUpper(sid) + "\x00" + strings.ToLower(path)))
	return hex.EncodeToString(hash[:16])
}

func randomID() (string, error) {
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(raw[:]), nil
}

func isMissingPipe(err error) bool {
	return errors.Is(err, windows.ERROR_FILE_NOT_FOUND) || errors.Is(err, windows.ERROR_PATH_NOT_FOUND) || errors.Is(err, windows.ERROR_PIPE_NOT_CONNECTED)
}
