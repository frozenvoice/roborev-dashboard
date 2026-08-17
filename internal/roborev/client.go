package roborev

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	proc "github.com/froze/roborev-dashboard/internal/process"
)

type Service interface {
	Installation(context.Context) Installation
	List(context.Context, string, int) (json.RawMessage, error)
	Show(context.Context, int64) (json.RawMessage, error)
	PanelMembers(context.Context, string) (json.RawMessage, error)
	Prompt(context.Context, int64) (string, error)
	Log(context.Context, int64) (string, error)
	Review(context.Context, ReviewRequest) (json.RawMessage, error)
	Rerun(context.Context, int64) error
	Comment(context.Context, int64, string) error
	Close(context.Context, int64, bool) error
	Cancel(context.Context, int64) error
	StartDaemon(context.Context) error
	ReviewCommand(ReviewRequest) (CommandPreview, error)
}

type Client struct {
	Binary    string
	Runner    proc.Runner
	DaemonURL string
	HTTP      *http.Client
}

func ResolveBinary(override string) (string, error) {
	if strings.TrimSpace(override) != "" {
		abs, err := filepath.Abs(override)
		if err != nil {
			return "", fmt.Errorf("resolve RoboRev override: %w", err)
		}
		info, err := os.Stat(abs)
		if err != nil || info.IsDir() {
			return "", fmt.Errorf("RoboRev override is not an executable file: %s", abs)
		}
		return abs, nil
	}
	path, err := exec.LookPath("roborev")
	if err != nil {
		return "", errors.New("roborev executable not found on PATH")
	}
	return filepath.Abs(path)
}

func NewClient(binary string, runner proc.Runner, daemonAddress string) (*Client, error) {
	if runner == nil {
		return nil, errors.New("process runner is required")
	}
	if daemonAddress == "" {
		daemonAddress = "127.0.0.1:7373"
	}
	if !strings.Contains(daemonAddress, "://") {
		daemonAddress = "http://" + daemonAddress
	}
	parsed, err := url.Parse(daemonAddress)
	if err != nil {
		return nil, fmt.Errorf("parse daemon address: %w", err)
	}
	host := parsed.Hostname()
	ip := net.ParseIP(host)
	if host != "localhost" && (ip == nil || !ip.IsLoopback()) {
		return nil, errors.New("RoboRev daemon address must be loopback")
	}
	return &Client{Binary: binary, Runner: runner, DaemonURL: strings.TrimRight(daemonAddress, "/"), HTTP: &http.Client{Timeout: 15 * time.Second}}, nil
}

func (c *Client) run(ctx context.Context, args []string, timeout time.Duration, limit int) (proc.Result, error) {
	if c.Binary == "" {
		return proc.Result{}, errors.New("roborev is not installed")
	}
	result, err := c.Runner.Run(ctx, proc.Command{Executable: c.Binary, Args: args, Timeout: timeout, OutputLimit: limit})
	if err != nil {
		message := strings.TrimSpace(result.Stderr)
		if message == "" {
			message = err.Error()
		}
		return result, errors.New(message)
	}
	return result, nil
}

func decodeRaw(output string) (json.RawMessage, error) {
	raw := json.RawMessage(strings.TrimSpace(output))
	if len(raw) == 0 || !json.Valid(raw) {
		return nil, errors.New("RoboRev returned malformed JSON")
	}
	return raw, nil
}

func capabilities(version string) Capabilities {
	parts := strings.Split(strings.TrimPrefix(version, "v"), ".")
	minor := 0
	if len(parts) > 1 {
		minor, _ = strconv.Atoi(parts[1])
	}
	v64 := len(parts) > 0 && parts[0] == "0" && minor >= 64
	return Capabilities{ReviewEnqueueAPI: v64, FrozenRerunAPI: v64, PanelReview: v64, PanelMembersAPI: v64, PromptLazyLoad: true, RawLog: true}
}

func (c *Client) Installation(ctx context.Context) Installation {
	if c.Binary == "" {
		return Installation{Installed: false, Error: "roborev executable not found", Capabilities: Capabilities{}}
	}
	result, err := c.run(ctx, []string{"version", "--json"}, 5*time.Second, 64<<10)
	if err != nil {
		return Installation{Installed: false, Binary: c.Binary, Error: err.Error()}
	}
	var version struct {
		Version string `json:"version"`
	}
	if json.Unmarshal([]byte(result.Stdout), &version) != nil || version.Version == "" {
		return Installation{Installed: false, Binary: c.Binary, Error: "invalid roborev version response"}
	}
	installation := Installation{Installed: true, Binary: c.Binary, Version: version.Version, Capabilities: capabilities(version.Version), Status: json.RawMessage(`{"running":false}`)}
	if installation.Capabilities.ReviewEnqueueAPI {
		if health, healthErr := c.health(ctx); healthErr == nil {
			status, _ := json.Marshal(map[string]any{"running": true, "health": health})
			installation.Status = status
		}
	}
	return installation
}

func (c *Client) health(ctx context.Context) (json.RawMessage, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.DaemonURL+"/api/health", nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 256<<10))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("health endpoint returned %d", resp.StatusCode)
	}
	return decodeRaw(string(data))
}

func (c *Client) List(ctx context.Context, repo string, limit int) (json.RawMessage, error) {
	result, err := c.run(ctx, ListArgs(repo, limit), 10*time.Second, 8<<20)
	if err != nil {
		return nil, err
	}
	return decodeRaw(result.Stdout)
}

func (c *Client) Show(ctx context.Context, id int64) (json.RawMessage, error) {
	args, err := ShowArgs(id)
	if err != nil {
		return nil, err
	}
	result, err := c.run(ctx, args, 10*time.Second, 8<<20)
	if err != nil {
		return nil, err
	}
	return decodeRaw(result.Stdout)
}

func (c *Client) PanelMembers(ctx context.Context, runUUID string) (json.RawMessage, error) {
	if len(runUUID) != 36 {
		return nil, errors.New("invalid panel run UUID")
	}
	for _, char := range runUUID {
		if !(char == '-' || char >= '0' && char <= '9' || char >= 'a' && char <= 'f' || char >= 'A' && char <= 'F') {
			return nil, errors.New("invalid panel run UUID")
		}
	}
	installation := c.Installation(ctx)
	if !installation.Installed || !installation.Capabilities.PanelMembersAPI {
		return nil, fmt.Errorf("RoboRev %s does not support panel member listing", installation.Version)
	}
	endpoint := c.DaemonURL + "/api/jobs?panel_run=" + url.QueryEscape(runUUID) + "&limit=0&omit_prompt=true"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, fmt.Errorf("RoboRev daemon request: %w", err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("RoboRev daemon returned %d: %s", resp.StatusCode, strings.TrimSpace(string(data)))
	}
	return decodeRaw(string(data))
}

func (c *Client) Prompt(ctx context.Context, id int64) (string, error) {
	args, err := PromptArgs(id)
	if err != nil {
		return "", err
	}
	result, err := c.run(ctx, args, 10*time.Second, 8<<20)
	if err != nil {
		return "", err
	}
	return result.Stdout, nil
}

func (c *Client) Log(ctx context.Context, id int64) (string, error) {
	args, err := LogArgs(id)
	if err != nil {
		return "", err
	}
	result, err := c.run(ctx, args, 15*time.Second, 8<<20)
	if err != nil {
		return "", err
	}
	return result.Stdout, nil
}

func (c *Client) postJSON(ctx context.Context, path string, body any) (json.RawMessage, error) {
	payload, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.DaemonURL+path, bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, fmt.Errorf("RoboRev daemon request: %w", err)
	}
	defer resp.Body.Close()
	data, readErr := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if readErr != nil {
		return nil, readErr
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("RoboRev daemon returned %d: %s", resp.StatusCode, strings.TrimSpace(string(data)))
	}
	if len(bytes.TrimSpace(data)) == 0 {
		return json.RawMessage(`{}`), nil
	}
	if !json.Valid(data) {
		return nil, errors.New("RoboRev daemon returned malformed JSON")
	}
	return json.RawMessage(data), nil
}

func (c *Client) Review(ctx context.Context, request ReviewRequest) (json.RawMessage, error) {
	installation := c.Installation(ctx)
	if !installation.Installed {
		return nil, errors.New("RoboRev is not installed or version detection failed")
	}
	if !installation.Capabilities.ReviewEnqueueAPI {
		return nil, fmt.Errorf("RoboRev %s does not support the version-gated enqueue API", installation.Version)
	}
	if _, err := ReviewPreview(c.Binary, request); err != nil {
		return nil, err
	}
	body := map[string]any{"repo_path": request.RepoPath, "git_ref": request.GitRef, "branch": request.Branch}
	if request.Panel != "" {
		body["panel"] = request.Panel
	}
	return c.postJSON(ctx, "/api/enqueue", body)
}

func (c *Client) Rerun(ctx context.Context, id int64) error {
	if err := validateJobID(id); err != nil {
		return err
	}
	installation := c.Installation(ctx)
	if !installation.Installed || !installation.Capabilities.FrozenRerunAPI {
		return fmt.Errorf("RoboRev %s does not support frozen-target rerun", installation.Version)
	}
	_, err := c.postJSON(ctx, "/api/job/rerun", map[string]int64{"job_id": id})
	return err
}

func (c *Client) Comment(ctx context.Context, id int64, comment string) error {
	args, err := CommentArgs(id, comment)
	if err != nil {
		return err
	}
	_, err = c.run(ctx, args, 15*time.Second, 1<<20)
	return err
}

func (c *Client) Close(ctx context.Context, id int64, reopen bool) error {
	args, err := CloseArgs(id, reopen)
	if err != nil {
		return err
	}
	_, err = c.run(ctx, args, 10*time.Second, 1<<20)
	return err
}

func (c *Client) Cancel(ctx context.Context, id int64) error {
	args, err := CancelArgs(id)
	if err != nil {
		return err
	}
	_, err = c.run(ctx, args, 10*time.Second, 1<<20)
	return err
}

func (c *Client) StartDaemon(ctx context.Context) error {
	_, err := c.run(ctx, []string{"daemon", "start"}, 30*time.Second, 1<<20)
	return err
}

func (c *Client) ReviewCommand(request ReviewRequest) (CommandPreview, error) {
	return ReviewPreview(c.Binary, request)
}
