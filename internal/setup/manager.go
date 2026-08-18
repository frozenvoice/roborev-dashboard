// Package setup owns the process-scoped RoboRev installation state. It never
// modifies PATH, initializes RoboRev, or changes agent/security settings.
package setup

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/froze/roborev-dashboard/internal/operation"
	"github.com/froze/roborev-dashboard/internal/roborev"
	"github.com/froze/roborev-dashboard/internal/roborevcompat"
)

type Phase string

const (
	defaultSetupCeiling    = 20 * time.Minute
	defaultRecoveryCeiling = 2 * time.Minute

	PhaseIdle             Phase = "idle"
	PhaseChecking         Phase = "checking"
	PhaseDownloading      Phase = "downloading"
	PhaseVerifying        Phase = "verifying"
	PhaseExtracting       Phase = "extracting"
	PhaseInstalling       Phase = "installing"
	PhaseBinding          Phase = "binding"
	PhaseStartingDaemon   Phase = "starting_daemon"
	PhaseWaitingForDaemon Phase = "waiting_for_daemon"
	PhaseReady            Phase = "ready"
	PhaseUnsupported      Phase = "unsupported"
	PhaseFailed           Phase = "failed"
	PhaseBlocked          Phase = "blocked"
	PhaseCanceled         Phase = "canceled"
)

var (
	ErrBusy               = errors.New("RoboRev setup is already in progress")
	ErrIneligible         = errors.New("RoboRev setup is not eligible for automatic installation")
	ErrApplicationControl = errors.New("Windows Application Control blocked RoboRev installation")
)

type State struct {
	Status                Phase      `json:"status"`
	Phase                 Phase      `json:"phase"`
	OperationActive       bool       `json:"operationActive"`
	Installed             bool       `json:"installed"`
	BinaryPath            string     `json:"binaryPath,omitempty"`
	Version               string     `json:"version,omitempty"`
	RecommendedVersion    string     `json:"recommendedVersion"`
	Compatible            *bool      `json:"compatible,omitempty"`
	PanelRefineCompatible *bool      `json:"panelRefineCompatible,omitempty"`
	DaemonRunning         bool       `json:"daemonRunning"`
	Blocked               bool       `json:"blocked"`
	Error                 string     `json:"error,omitempty"`
	FailureCode           string     `json:"failureCode,omitempty"`
	Retryable             bool       `json:"retryable"`
	InstallAvailable      bool       `json:"installAvailable"`
	CanInstall            bool       `json:"canInstall"`
	CanRetry              bool       `json:"canRetry"`
	CanStartDaemon        bool       `json:"canStartDaemon"`
	Architecture          string     `json:"architecture,omitempty"`
	SourceURL             string     `json:"sourceURL"`
	SourceLabel           string     `json:"sourceLabel"`
	AssetName             string     `json:"assetName"`
	SHA256                string     `json:"sha256"`
	TargetPath            string     `json:"targetPath"`
	DownloadedBytes       int64      `json:"downloadedBytes,omitempty"`
	TotalBytes            int64      `json:"totalBytes,omitempty"`
	StartedAt             time.Time  `json:"startedAt,omitempty"`
	UpdatedAt             time.Time  `json:"updatedAt"`
	CompletedAt           *time.Time `json:"completedAt,omitempty"`
	Message               string     `json:"message,omitempty"`
	// Internal aliases retained for Go callers while HTTP uses explicit setup names.
	InProgress bool   `json:"-"`
	Progress   int    `json:"-"`
	Binary     string `json:"-"`
	Supported  bool   `json:"-"`
}

func (s State) MarshalJSON() ([]byte, error) {
	type plain State
	s.Status = s.Phase
	s.OperationActive = s.InProgress
	s.BinaryPath = s.Binary
	manifest := roborevcompat.Manifest064()
	if s.RecommendedVersion == "" {
		s.RecommendedVersion = manifest.Version
	}
	if s.TotalBytes == 0 {
		s.TotalBytes = manifest.CompressedSize
	}
	return json.Marshal(plain(s))
}

type Prober interface {
	BinaryPath() string
	HasExplicitOverride() bool
	Rebind(context.Context, string) error
	Installation(context.Context) roborev.Installation
}

type daemonStarter interface{ StartDaemon(context.Context) error }

type Installer interface {
	Target() string
	Install(context.Context) (string, error)
}

type progressInstaller interface{ SetProgress(func(string, int)) }

type Manager struct {
	client          Prober
	installer       Installer
	mu              sync.RWMutex
	state           State
	root            context.Context
	rootCancel      context.CancelFunc
	cancel          context.CancelFunc
	closing         bool
	wg              sync.WaitGroup
	gate            *operation.Gate
	wait            func(context.Context, time.Duration) error
	gateRelease     func()
	setupCeiling    time.Duration
	recoveryCeiling time.Duration
}

func NewManager(client Prober, installer Installer) (*Manager, error) {
	return NewManagerWithGate(client, installer, nil)
}

func NewManagerWithGate(client Prober, installer Installer, gate *operation.Gate) (*Manager, error) {
	if client == nil || installer == nil {
		return nil, errors.New("RoboRev setup dependencies are required")
	}
	root, rootCancel := context.WithCancel(context.Background())
	m := &Manager{client: client, installer: installer, root: root, rootCancel: rootCancel, gate: gate, wait: waitSetupContext, setupCeiling: defaultSetupCeiling, recoveryCeiling: defaultRecoveryCeiling}
	m.state = m.newState(PhaseIdle)
	if progress, ok := installer.(progressInstaller); ok {
		progress.SetProgress(m.progress)
	}
	return m, nil
}

// SetGate wires the server's shared operation gate before the first install.
// It is intentionally only a pointer assignment; an active setup operation
// must not be rebound to another gate.
func (m *Manager) SetGate(gate *operation.Gate) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.closing && m.cancel == nil && !m.state.InProgress {
		m.gate = gate
	}
}

func (m *Manager) progress(phase string, percent int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.state.InProgress {
		return
	}
	var next Phase
	switch phase {
	case "downloading":
		next = PhaseDownloading
	case "verifying":
		next = PhaseVerifying
	case "extracting":
		next = PhaseExtracting
	case "installing":
		next = PhaseInstalling
	case "binding":
		next = PhaseBinding
	case "starting_daemon":
		next = PhaseStartingDaemon
	case "waiting_for_daemon":
		next = PhaseWaitingForDaemon
	case "ready":
		next = PhaseReady
	default:
		next = PhaseChecking
	}
	m.state.Phase, m.state.Status, m.state.Progress = next, next, percent
	m.state.UpdatedAt = time.Now().UTC()
}

func (m *Manager) newState(phase Phase) State {
	manifest := roborevcompat.Manifest064()
	target := m.installer.Target()
	return State{Phase: phase, Status: phase, RecommendedVersion: manifest.Version, Architecture: runtime.GOOS + "/" + runtime.GOARCH, SourceURL: manifest.URL, SourceLabel: "Official GitHub release", AssetName: "roborev_0.64.0_windows_amd64.zip", SHA256: manifest.SHA256, TargetPath: target, TotalBytes: manifest.CompressedSize, UpdatedAt: time.Now().UTC()}
}

func (m *Manager) Status(ctx context.Context) State {
	if m == nil {
		return State{Phase: PhaseFailed, Error: "RoboRev setup manager is unavailable"}
	}
	m.mu.RLock()
	active, failed := m.state.InProgress, isTerminalSetupPhase(m.state.Phase)
	m.mu.RUnlock()
	if !active && !failed {
		m.refresh(ctx)
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.state
}

func (m *Manager) refresh(ctx context.Context) {
	m.mu.RLock()
	inProgress := m.state.InProgress
	m.mu.RUnlock()
	if inProgress {
		return
	}
	installation := m.client.Installation(ctx)
	now := time.Now().UTC()
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.state.InProgress {
		return
	}
	state := m.newState(m.state.Phase)
	m.state.Binary = installation.Binary
	m.state.Version = installation.Version
	m.state.Installed = installation.Installed
	m.state.Supported = installation.Installed && roborevcompat.IsSupportedVersion(installation.Version)
	state.Binary = installation.Binary
	state.Version = installation.Version
	state.Installed = installation.Installed
	state.Supported = m.state.Supported
	compatible := state.Supported
	state.Compatible = &compatible
	state.DaemonRunning = daemonRunning(installation.Status)
	panelCompatible := state.Supported && installation.Capabilities.PanelReview && installation.Capabilities.FixCommand && installation.Capabilities.JobOutputStatusAPI
	state.PanelRefineCompatible = &panelCompatible
	state.CanStartDaemon = state.Supported && !state.DaemonRunning
	state.InstallAvailable = !state.Installed && roborevcompat.RuntimePlatformSupported() && !m.client.HasExplicitOverride()
	state.CanInstall = state.InstallAvailable
	state.Error = installation.Error
	state.FailureCode = installation.FailureCode
	state.UpdatedAt = now
	if !installation.Installed {
		if installation.Binary != "" {
			if installation.FailureCode == "application_control" && regularFile(installation.Binary) {
				state.Phase, state.Status = PhaseBlocked, PhaseBlocked
				state.Installed = true
				state.Binary = installation.Binary
				state.Blocked = true
				state.Message = "Windows Application Control blocked the verified RoboRev binary"
				compatible := false
				state.Compatible = &compatible
				state.PanelRefineCompatible = &compatible
				state.CanInstall, state.InstallAvailable = false, false
				state.CanRetry, state.Retryable = true, true
				m.state = state
				return
			}
			state.Phase, state.Status = PhaseFailed, PhaseFailed
			state.Message = "The existing RoboRev binary could not be verified"
			state.FailureCode = firstNonEmpty(installation.FailureCode, "invalid_binary")
			state.CanInstall, state.InstallAvailable = false, false
			state.CanRetry, state.Retryable = isRetryableFailureCode(state.FailureCode), isRetryableFailureCode(state.FailureCode)
			state.Blocked = false
			m.state = state
			return
		}
		if !roborevcompat.RuntimePlatformSupported() {
			state.Phase, state.Status = PhaseUnsupported, PhaseUnsupported
			state.Message = "RoboRev setup is unsupported on this architecture"
			state.InstallAvailable, state.CanInstall = false, false
		} else {
			state.Phase, state.Status = PhaseIdle, PhaseIdle
			state.Message = "RoboRev setup is required"
		}
		m.state = state
		return
	}
	if !state.Supported {
		state.Phase = PhaseUnsupported
		state.Status = PhaseUnsupported
		state.Message = "This RoboRev version is not eligible for automatic replacement"
		state.InstallAvailable = false
		state.CanInstall = false
		m.state = state
		return
	}
	state.Phase = PhaseReady
	state.Status = PhaseReady
	state.Message = "RoboRev is ready"
	// A daemon health failure is meaningful and must remain visible for retry.
	m.state = state
}

// acceptAsync atomically accepts a process-scoped setup operation after its
// caller has acquired the shared setup gate. The request context is consulted
// only for final synchronous acceptance; the worker derives its lifetime from
// the manager root so an HTTP request ending cannot cancel an accepted job.
func (m *Manager) acceptAsync(parent context.Context, release func(), ceiling time.Duration, prepare func(State) State, worker func(context.Context)) (State, error) {
	if parent == nil {
		parent = context.Background()
	}
	m.mu.Lock()
	if m.closing || m.cancel != nil || m.state.InProgress {
		state := m.state
		m.mu.Unlock()
		if release != nil {
			release()
		}
		return state, ErrBusy
	}
	if err := parent.Err(); err != nil {
		state := m.state
		m.mu.Unlock()
		if release != nil {
			release()
		}
		return state, err
	}
	ctx, cancel := context.WithTimeout(m.root, ceiling)
	state := prepare(m.state)
	state.InProgress = true
	m.state = state
	m.cancel = cancel
	m.gateRelease = release
	m.wg.Add(1)
	m.mu.Unlock()
	go func() {
		defer func() {
			// The worker has published its terminal state before this lifecycle
			// cleanup runs. Clear active fields while holding the manager lock,
			// then release the shared gate before allowing Shutdown to return.
			cancel()
			m.mu.Lock()
			m.cancel = nil
			gateRelease := m.gateRelease
			m.gateRelease = nil
			m.mu.Unlock()
			if gateRelease != nil {
				gateRelease()
			}
			m.wg.Done()
		}()
		worker(ctx)
	}()
	return state, nil
}

func (m *Manager) StartInstall(parent context.Context) (State, error) {
	if m == nil {
		return State{}, ErrIneligible
	}
	if parent == nil {
		parent = context.Background()
	}
	m.mu.RLock()
	if m.closing || m.cancel != nil || m.state.InProgress {
		state := m.state
		m.mu.RUnlock()
		return state, ErrBusy
	}
	if err := parent.Err(); err != nil {
		state := m.state
		m.mu.RUnlock()
		return state, err
	}
	m.mu.RUnlock()
	if m.client.HasExplicitOverride() {
		return m.stateCopy(), fmt.Errorf("%w: explicit RoboRev path", ErrIneligible)
	}
	current := m.Status(parent)
	if current.InProgress {
		return current, ErrBusy
	}
	if current.Installed && roborevcompat.IsSupportedVersion(current.Version) {
		return current, nil
	}
	if isTerminalSetupPhase(current.Phase) && !current.CanInstall && !(current.Phase == PhaseFailed && current.CanRetry) {
		return current, fmt.Errorf("%w: setup state %s is not eligible", ErrIneligible, current.Phase)
	}
	if !current.Installed && current.Binary != "" {
		if _, err := os.Stat(current.Binary); err == nil {
			return current, fmt.Errorf("%w: existing RoboRev binary could not be strictly verified", ErrIneligible)
		}
	}
	if current.Installed && current.Version != "" && !roborevcompat.IsSupportedVersion(current.Version) {
		return current, fmt.Errorf("%w: installed version %s", ErrIneligible, current.Version)
	}
	var release func()
	m.mu.RLock()
	gate := m.gate
	m.mu.RUnlock()
	if gate != nil {
		var gateErr error
		release, gateErr = gate.AcquireSetup()
		if gateErr != nil {
			return current, fmt.Errorf("%w: %v", ErrBusy, gateErr)
		}
	}
	return m.acceptAsync(parent, release, m.setupCeiling, func(_ State) State {
		state := m.newState(PhaseChecking)
		state.Progress = 5
		state.Binary = current.Binary
		state.Version = current.Version
		state.Installed = current.Installed
		state.Supported = current.Supported
		state.Message = "Checking RoboRev setup"
		state.InstallAvailable = true
		state.CanInstall = true
		return state
	}, m.install)
}

func (m *Manager) install(ctx context.Context) {
	path, err := m.installer.Install(ctx)
	bound := false
	if err != nil {
		// If another process won the no-overwrite publication race, verify the
		// winner before accepting it. Never replace or delete that file.
		if errors.Is(err, roborevcompat.ErrExistingBinary) {
			winner := m.installer.Target()
			if verifyErr := m.client.Rebind(ctx, winner); verifyErr == nil {
				path, err, bound = winner, nil, true
			}
		}
		if err != nil {
			m.finishError(ctx, err, "")
			return
		}
	}
	m.progress("binding", 90)
	if !bound {
		if err := m.client.Rebind(ctx, path); err != nil {
			m.finishError(ctx, err, path)
			return
		}
	}
	installation := m.client.Installation(ctx)
	_ = m.completeBound(ctx, path, installation)
}

func (m *Manager) completeBound(ctx context.Context, path string, installation roborev.Installation) error {
	return m.completeBoundMode(ctx, path, installation, false)
}

func (m *Manager) completeBoundBlocked(ctx context.Context, path string, installation roborev.Installation) error {
	return m.completeBoundMode(ctx, path, installation, true)
}

func (m *Manager) completeBoundMode(ctx context.Context, path string, installation roborev.Installation, blockedRecovery bool) error {
	finishCanceled := func(err error) {
		if blockedRecovery {
			m.finishBlockedCanceled(path, err)
			return
		}
		if installation.Installed && roborevcompat.IsSupportedVersion(installation.Version) {
			m.finishDaemonError(path, installation, err)
			return
		}
		m.finishError(ctx, err, path)
	}
	if err := ctx.Err(); err != nil {
		finishCanceled(err)
		return err
	}
	if !installation.Installed || !roborevcompat.IsSupportedVersion(installation.Version) {
		detail := installation.Error
		if detail == "" {
			detail = installation.Version
		}
		err := fmt.Errorf("%w: installed binary failed strict v0.64.0 verification: %s", roborevcompat.ErrUnsupportedVersion, detail)
		m.finishError(ctx, err, path)
		return err
	}
	if !daemonRunning(installation.Status) {
		if starter, ok := m.client.(daemonStarter); ok {
			if err := ctx.Err(); err != nil {
				finishCanceled(err)
				return err
			}
			m.progress("starting_daemon", 92)
			if err := starter.StartDaemon(ctx); err != nil {
				if ctx.Err() != nil {
					finishCanceled(ctx.Err())
					return ctx.Err()
				}
				healthy := m.client.Installation(ctx)
				if ctx.Err() != nil {
					finishCanceled(ctx.Err())
					return ctx.Err()
				}
				if daemonRunning(healthy.Status) {
					m.finishReady(path, healthy)
					return nil
				}
				m.finishDaemonError(path, installation, err)
				return err
			}
			if err := ctx.Err(); err != nil {
				finishCanceled(err)
				return err
			}
			m.progress("waiting_for_daemon", 95)
			if err := m.waitForDaemon(ctx); err != nil {
				if ctx.Err() != nil {
					finishCanceled(ctx.Err())
					return ctx.Err()
				}
				m.finishDaemonError(path, installation, err)
				return err
			}
			if err := ctx.Err(); err != nil {
				finishCanceled(err)
				return err
			}
			previous := installation
			probe := m.client.Installation(ctx)
			if err := ctx.Err(); err != nil {
				installation = previous
				finishCanceled(err)
				return err
			}
			installation = probe
		}
	}
	m.finishReady(path, installation)
	return nil
}

func (m *Manager) finishReady(path string, installation roborev.Installation) {
	m.mu.Lock()
	state := m.newState(PhaseReady)
	state.Installed = true
	state.Supported = true
	state.InProgress = false
	state.Progress = 100
	state.Binary = path
	state.Version = installation.Version
	state.Error = installation.Error
	state.Message = "RoboRev is ready"
	compatible := true
	state.Compatible = &compatible
	panelCompatible := installation.Capabilities.PanelReview && installation.Capabilities.FixCommand && installation.Capabilities.JobOutputStatusAPI
	state.PanelRefineCompatible = &panelCompatible
	state.DaemonRunning = daemonRunning(installation.Status)
	state.CanStartDaemon = !state.DaemonRunning
	state.CanRetry = false
	state.InstallAvailable = false
	state.CanInstall = false
	state.CompletedAt = ptrTime(time.Now().UTC())
	state.UpdatedAt = time.Now().UTC()
	m.state = state
	m.mu.Unlock()
}

func (m *Manager) waitForDaemon(ctx context.Context) error {
	wait := m.wait
	if wait == nil {
		wait = waitSetupContext
	}
	for _, delay := range []time.Duration{100 * time.Millisecond, 250 * time.Millisecond, 500 * time.Millisecond, time.Second, time.Second} {
		if err := ctx.Err(); err != nil {
			return err
		}
		installation := m.client.Installation(ctx)
		if err := ctx.Err(); err != nil {
			return err
		}
		if daemonRunning(installation.Status) {
			return nil
		}
		if err := wait(ctx, delay); err != nil {
			return err
		}
	}
	return errors.New("daemon health did not become ready")
}

func waitSetupContext(ctx context.Context, duration time.Duration) error {
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func (m *Manager) finishDaemonError(path string, installation roborev.Installation, err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	state := m.newState(PhaseFailed)
	state.Installed, state.Supported = true, true
	state.Binary, state.Version = path, installation.Version
	state.DaemonRunning = false
	state.CanStartDaemon, state.CanRetry = true, true
	state.Retryable = true
	state.Error, state.Message = err.Error(), "RoboRev installed, but the daemon is not ready"
	state.FailureCode = "daemon_start_failed"
	state.CompletedAt = ptrTime(time.Now().UTC())
	m.state = state
}

func (m *Manager) finishBlockedCanceled(path string, err error) {
	if err == nil {
		err = context.Canceled
	}
	m.mu.Lock()
	state := m.newState(PhaseBlocked)
	state.Installed = true
	state.Supported = false
	state.Binary = path
	state.Blocked = true
	state.Error = fmt.Errorf("blocked RoboRev recovery canceled: %w", err).Error()
	state.Message = "Windows Application Control blocked the verified RoboRev binary; recovery can be retried"
	state.FailureCode = "application_control"
	compatible := false
	state.Compatible = &compatible
	state.PanelRefineCompatible = &compatible
	state.Retryable, state.CanRetry = true, true
	state.InstallAvailable, state.CanInstall = false, false
	state.CanStartDaemon = false
	state.CompletedAt = ptrTime(time.Now().UTC())
	state.UpdatedAt = time.Now().UTC()
	m.state = state
	m.mu.Unlock()
}

func (m *Manager) finishError(ctx context.Context, err error, path string) {
	canceled := ctx.Err() != nil
	if canceled {
		err = fmt.Errorf("setup canceled: %w", ctx.Err())
	}
	err = classifyInstallError(err)
	m.mu.Lock()
	m.state.InProgress = false
	m.state.Phase, m.state.Status = PhaseFailed, PhaseFailed
	m.state.Error = err.Error()
	m.state.FailureCode = "install_failed"
	m.state.FailureCode = failureCodeForError(err)
	m.state.Blocked = false
	if errors.Is(err, ErrApplicationControl) {
		if regularFile(path) {
			state := m.newState(PhaseBlocked)
			state.Installed = true
			state.Binary = path
			state.Blocked = true
			state.FailureCode = "application_control"
			state.Error = err.Error()
			state.Message = "Windows Application Control blocked the verified RoboRev binary"
			compatible := false
			state.Compatible = &compatible
			state.PanelRefineCompatible = &compatible
			state.Retryable, state.CanRetry = true, true
			state.InstallAvailable, state.CanInstall = false, false
			state.CompletedAt = ptrTime(time.Now().UTC())
			state.UpdatedAt = time.Now().UTC()
			m.state = state
			m.mu.Unlock()
			return
		}
		m.state.Blocked = false
		m.state.FailureCode = "application_control"
	}
	m.state.Retryable = true
	m.state.CanRetry = true
	m.state.CanInstall = false
	m.state.InstallAvailable = false
	m.state.Message = "RoboRev setup failed; retry is available"
	m.state.CompletedAt = ptrTime(time.Now().UTC())
	if canceled || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		m.state.Phase, m.state.Status = PhaseCanceled, PhaseCanceled
		m.state.FailureCode = "canceled"
		m.state.Retryable = false
		m.state.CanRetry = false
	}
	if errors.Is(err, roborevcompat.ErrDownloadHash) || errors.Is(err, roborevcompat.ErrDownloadSize) || errors.Is(err, roborevcompat.ErrArchiveContract) || errors.Is(err, roborevcompat.ErrArchiveCRC) || errors.Is(err, roborevcompat.ErrUnsupportedVersion) {
		m.state.Retryable = false
		m.state.CanRetry = false
	}
	if !isRetryableFailureCode(m.state.FailureCode) {
		m.state.Retryable = false
		m.state.CanRetry = false
	}
	m.state.UpdatedAt = time.Now().UTC()
	m.mu.Unlock()
}

func regularFile(path string) bool {
	if strings.TrimSpace(path) == "" {
		return false
	}
	info, err := os.Stat(path)
	return err == nil && info.Mode().IsRegular()
}

func failureCodeForError(err error) string {
	switch {
	case errors.Is(err, ErrApplicationControl):
		return "application_control"
	case errors.Is(err, roborevcompat.ErrDownloadHash):
		return "checksum_mismatch"
	case errors.Is(err, roborevcompat.ErrDownloadSize):
		return "size_mismatch"
	case errors.Is(err, roborevcompat.ErrDownloadContract):
		return "download_failed"
	case errors.Is(err, roborevcompat.ErrArchiveCRC):
		return "archive_corrupt"
	case errors.Is(err, roborevcompat.ErrArchiveContract):
		return "invalid_archive"
	case errors.Is(err, roborevcompat.ErrRedirectPolicy):
		return "unsafe_redirect"
	case errors.Is(err, roborevcompat.ErrUnsupportedVersion):
		return "unsupported_version"
	case errors.Is(err, roborevcompat.ErrInstallPermission):
		return "permission_denied"
	case errors.Is(err, roborevcompat.ErrExistingBinary):
		return "existing_binary"
	case errors.Is(err, roborevcompat.ErrUnsupportedPlatform):
		return "unsupported_platform"
	case errors.Is(err, roborevcompat.ErrInstallPolicy):
		return "unsafe_archive"
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		return "canceled"
	default:
		return "install_failed"
	}
}

func classifyInstallError(err error) error {
	if err == nil {
		return nil
	}
	if isApplicationControl(err) {
		return fmt.Errorf("%w: %v", ErrApplicationControl, err)
	}
	if os.IsPermission(err) || errors.Is(err, syscall.EACCES) || errors.Is(err, syscall.EPERM) {
		return fmt.Errorf("%w: %v", roborevcompat.ErrInstallPermission, err)
	}
	return err
}

func isApplicationControl(err error) bool {
	var errno syscall.Errno
	if !errors.As(err, &errno) {
		return false
	}
	return errno == syscall.Errno(1260) || errno == syscall.Errno(786)
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

func isRetryableFailureCode(code string) bool {
	switch code {
	case "checksum_mismatch", "size_mismatch", "invalid_archive", "archive_corrupt", "unsafe_redirect", "unsafe_archive", "invalid_binary", "unsupported_version", "unsupported_platform", "application_control", "explicit_override", "existing_binary", "canceled":
		return false
	default:
		return code == "" || code == "permission_denied" || code == "download_failed" || code == "daemon_start_failed"
	}
}

func isTerminalSetupPhase(phase Phase) bool {
	return phase == PhaseFailed || phase == PhaseBlocked || phase == PhaseCanceled || phase == PhaseUnsupported
}

func ptrTime(value time.Time) *time.Time { return &value }

func daemonRunning(raw json.RawMessage) bool {
	var status struct {
		Running *bool `json:"running"`
	}
	return json.Unmarshal(raw, &status) == nil && status.Running != nil && *status.Running
}

func (m *Manager) stateCopy() State { m.mu.RLock(); defer m.mu.RUnlock(); return m.state }

// Retry resumes the last setup operation. A successfully installed binary
// with an unhealthy daemon takes the daemon-only path and never downloads
// another archive.
func (m *Manager) Retry(ctx context.Context) (State, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	state := m.stateCopy()
	if state.InProgress {
		return state, ErrBusy
	}
	if state.Blocked && state.Binary != "" {
		return m.retryBlocked(ctx, state.Binary)
	}
	if !state.CanRetry && isTerminalSetupPhase(state.Phase) {
		return state, fmt.Errorf("%w: failure is not retryable", ErrIneligible)
	}
	if state.Installed && state.Supported {
		if state.DaemonRunning {
			return state, nil
		}
		starter, ok := m.client.(daemonStarter)
		if !ok {
			return state, errors.New("RoboRev daemon start is unavailable")
		}
		return m.startDaemonOnly(ctx, starter, state)
	}
	return m.StartInstall(ctx)
}

// retryBlocked performs verification-only recovery for a policy-blocked
// managed binary. It never invokes the installer or downloader.
func (m *Manager) retryBlocked(parent context.Context, path string) (State, error) {
	if parent == nil {
		parent = context.Background()
	}
	if err := parent.Err(); err != nil {
		return m.stateCopy(), err
	}
	if !regularFile(path) {
		m.mu.Lock()
		m.state.InProgress = false
		m.state.Phase, m.state.Status = PhaseFailed, PhaseFailed
		m.state.Installed, m.state.Blocked = false, false
		m.state.FailureCode = "invalid_binary"
		m.state.Error = "the blocked RoboRev binary is no longer present"
		m.state.Message = "The blocked RoboRev binary must be restored before verification can retry"
		m.state.CanInstall, m.state.InstallAvailable = false, false
		m.state.CanRetry, m.state.Retryable = false, false
		m.state.UpdatedAt = time.Now().UTC()
		m.mu.Unlock()
		return m.stateCopy(), ErrIneligible
	}
	var release func()
	m.mu.RLock()
	gate := m.gate
	m.mu.RUnlock()
	if gate != nil {
		var err error
		release, err = gate.AcquireSetup()
		if err != nil {
			return m.stateCopy(), fmt.Errorf("%w: %v", ErrBusy, err)
		}
	}
	return m.acceptAsync(parent, release, m.recoveryCeiling, func(current State) State {
		current.Phase, current.Status = PhaseBinding, PhaseBinding
		current.Binary = path
		current.Installed = true
		current.Blocked = true
		current.CanRetry, current.CanInstall, current.InstallAvailable = false, false, false
		current.UpdatedAt = time.Now().UTC()
		return current
	}, func(ctx context.Context) {
		if err := ctx.Err(); err != nil {
			m.finishBlockedCanceled(path, err)
			return
		}
		if err := m.client.Rebind(ctx, path); err != nil {
			if ctx.Err() != nil {
				m.finishBlockedCanceled(path, ctx.Err())
				return
			}
			m.finishError(ctx, err, path)
			return
		}
		if err := ctx.Err(); err != nil {
			m.finishBlockedCanceled(path, err)
			return
		}
		installation := m.client.Installation(ctx)
		if err := ctx.Err(); err != nil {
			m.finishBlockedCanceled(path, err)
			return
		}
		_ = m.completeBoundBlocked(ctx, path, installation)
	})
}

func (m *Manager) startDaemonOnly(parent context.Context, starter daemonStarter, current State) (State, error) {
	if parent == nil {
		parent = context.Background()
	}
	var release func()
	m.mu.RLock()
	gate := m.gate
	m.mu.RUnlock()
	if gate != nil {
		var err error
		release, err = gate.AcquireSetup()
		if err != nil {
			return m.stateCopy(), fmt.Errorf("%w: %v", ErrBusy, err)
		}
	}
	return m.acceptAsync(parent, release, m.recoveryCeiling, func(currentState State) State {
		if currentState.Binary == "" {
			currentState.Binary = current.Binary
		}
		if currentState.Version == "" {
			currentState.Version = current.Version
		}
		currentState.Installed, currentState.Supported = true, true
		currentState.Phase, currentState.Status = PhaseStartingDaemon, PhaseStartingDaemon
		currentState.CanRetry, currentState.CanStartDaemon = false, false
		currentState.UpdatedAt = time.Now().UTC()
		return currentState
	}, func(ctx context.Context) {
		path := current.Binary
		version := current.Version
		installation := roborev.Installation{Installed: true, Binary: path, Version: version}
		if err := ctx.Err(); err != nil {
			m.finishDaemonError(path, installation, err)
			return
		}
		if err := starter.StartDaemon(ctx); err != nil {
			if ctx.Err() != nil {
				m.finishDaemonError(path, installation, ctx.Err())
				return
			}
			previous := installation
			probe := m.client.Installation(ctx)
			if ctx.Err() != nil {
				m.finishDaemonError(path, previous, ctx.Err())
				return
			}
			installation = probe
			if daemonRunning(installation.Status) {
				m.finishReady(path, installation)
				return
			}
			m.finishDaemonError(path, installation, err)
			return
		}
		if err := m.waitForDaemon(ctx); err != nil {
			if ctx.Err() != nil {
				m.finishDaemonError(path, installation, ctx.Err())
				return
			}
			previous := installation
			probe := m.client.Installation(ctx)
			if ctx.Err() != nil {
				m.finishDaemonError(path, previous, ctx.Err())
				return
			}
			installation = probe
			m.finishDaemonError(path, installation, err)
			return
		}
		if err := ctx.Err(); err != nil {
			m.finishDaemonError(path, installation, err)
			return
		}
		previous := installation
		probe := m.client.Installation(ctx)
		if err := ctx.Err(); err != nil {
			m.finishDaemonError(path, previous, err)
			return
		}
		installation = probe
		m.finishReady(path, installation)
	})
}

func (m *Manager) Shutdown(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}
	m.mu.Lock()
	m.closing = true
	rootCancel := m.rootCancel
	activeCancel := m.cancel
	m.mu.Unlock()
	if rootCancel != nil {
		rootCancel()
	}
	if activeCancel != nil {
		activeCancel()
	}
	done := make(chan struct{})
	go func() { m.wg.Wait(); close(done) }()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
