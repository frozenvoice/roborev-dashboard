package roborev

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/froze/roborev-dashboard/internal/gitx"
	"github.com/froze/roborev-dashboard/internal/insights"
	proc "github.com/froze/roborev-dashboard/internal/process"
	"github.com/froze/roborev-dashboard/internal/roborevcompat"
)

const insightsLaunchTimeout = 35 * time.Second

// InsightsPreflight performs fresh, typed validation. It intentionally does
// not reuse Installation's cached/version-projection result: preview and the
// launch boundary must bind the exact executable generation and daemon
// version immediately before the subprocess is entered.
func (c *Client) InsightsPreflight(ctx context.Context, request insights.Request) (insights.Preflight, error) {
	if c == nil {
		return insights.Preflight{}, errors.New("RoboRev client is unavailable")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if err := request.Validate(); err != nil {
		return insights.Preflight{}, err
	}
	binary, _, generation := c.snapshot()
	if strings.TrimSpace(binary) == "" {
		return insights.Preflight{}, errors.New("RoboRev executable is not bound")
	}
	info, err := os.Stat(binary)
	if err != nil || !info.Mode().IsRegular() {
		return insights.Preflight{}, errors.New("RoboRev executable binding is unavailable")
	}
	versionResult, err := c.Runner.Run(ctx, proc.Command{Executable: binary, Args: []string{"version", "--json"}, Timeout: 5 * time.Second, OutputLimit: insights.MaxReceiptBytes})
	if err != nil || !versionResult.Started || versionResult.StdoutTruncated || versionResult.StderrTruncated {
		if err == nil {
			err = errors.New("version probe failed")
		}
		return insights.Preflight{}, fmt.Errorf("RoboRev version preflight failed: %w", err)
	}
	cliVersion, err := decodeExactVersion(versionResult.Stdout)
	if err != nil {
		return insights.Preflight{}, err
	}
	if !roborevcompat.IsSupportedVersion(cliVersion) {
		return insights.Preflight{}, fmt.Errorf("RoboRev %s does not support Guideline Insights", cliVersion)
	}
	health, err := c.healthStatus(ctx)
	if err != nil {
		return insights.Preflight{}, fmt.Errorf("RoboRev daemon health preflight failed: %w", err)
	}
	if !health.Healthy {
		return insights.Preflight{}, ErrDaemonUnhealthy
	}
	if !roborevcompat.IsMatchingSupportedVersion(cliVersion, health.Version) {
		return insights.Preflight{}, fmt.Errorf("RoboRev CLI/daemon version mismatch: %s/%s", cliVersion, health.Version)
	}
	if !c.generationCurrent(generation) {
		return insights.Preflight{}, ErrBindingChanged
	}
	command, err := insights.BuildCommand(binary, request)
	if err != nil {
		return insights.Preflight{}, err
	}
	return insights.Preflight{Command: command, Binding: fmt.Sprintf("%d:%s", generation, binary), CLIVersion: cliVersion, DaemonVersion: health.Version}, nil
}

func decodeExactVersion(raw string) (string, error) {
	decoder := json.NewDecoder(bytes.NewReader([]byte(raw)))
	decoder.DisallowUnknownFields()
	var value struct {
		Name    string `json:"name"`
		Version string `json:"version"`
	}
	if err := decoder.Decode(&value); err != nil {
		return "", fmt.Errorf("malformed RoboRev version response: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return "", errors.New("malformed RoboRev version response: trailing JSON")
	}
	if err := roborevcompat.ValidateVersionIdentity(value.Name, value.Version); err != nil {
		return "", err
	}
	return value.Version, nil
}

// Insights is at-most-once at the Dashboard boundary. It revalidates the
// token-bound preflight and then performs exactly one shell-free daemon HTTP
// enqueue. Any transport or receipt error after Do is entered is intentionally
// returned as Unverified when the enqueue outcome is ambiguous.
func (c *Client) Insights(ctx context.Context, request insights.Request, expected insights.Preflight) (insights.LaunchResult, error) {
	if c == nil {
		return insights.LaunchResult{}, errors.New("RoboRev client is unavailable")
	}
	fresh, err := c.InsightsPreflight(ctx, request)
	if err != nil {
		return insights.LaunchResult{}, err
	}
	if fresh.Binding != expected.Binding || fresh.CLIVersion != expected.CLIVersion || fresh.DaemonVersion != expected.DaemonVersion || fresh.Command.Executable != expected.Command.Executable || !sameArgs(fresh.Command.Args, expected.Command.Args) || fresh.Command.Dir != expected.Command.Dir {
		return insights.LaunchResult{}, ErrBindingChanged
	}
	_, daemonURL, generation := c.snapshot()
	if strings.TrimSpace(daemonURL) == "" || generation == 0 || fresh.Binding != fmt.Sprintf("%d:%s", generation, fresh.Command.Executable) {
		return insights.LaunchResult{}, ErrBindingChanged
	}
	if !c.generationCurrent(generation) {
		return insights.LaunchResult{}, ErrBindingChanged
	}

	since := insightsSince(request.Window)
	payload := struct {
		RepoPath  string `json:"repo_path"`
		GitRef    string `json:"git_ref"`
		Branch    string `json:"branch,omitempty"`
		Since     string `json:"since"`
		Agent     string `json:"agent,omitempty"`
		Model     string `json:"model,omitempty"`
		Reasoning string `json:"reasoning,omitempty"`
		JobType   string `json:"job_type"`
	}{RepoPath: request.RepoPath, GitRef: "insights", Branch: request.Branch, Since: since, JobType: "insights"}
	body, err := json.Marshal(payload)
	if err != nil {
		return insights.LaunchResult{}, errors.New("RoboRev Insights request could not be encoded")
	}
	requestCtx := ctx
	if requestCtx == nil {
		requestCtx = context.Background()
	}
	requestCtx, cancel := context.WithTimeout(requestCtx, insightsLaunchTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(requestCtx, http.MethodPost, daemonURL+"/api/enqueue", bytes.NewReader(body))
	if err != nil {
		return insights.LaunchResult{}, errors.New("RoboRev Insights request could not be built")
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept-Encoding", "identity")
	// noRedirectHTTPClient intentionally preserves the configured client's
	// transport and timeout. Insights has a longer, explicit enqueue policy
	// than the default client timeout, so override only the timeout on this
	// request-local clone while retaining redirect protection.
	httpClient := noRedirectHTTPClient(c.HTTP)
	httpClient.Timeout = insightsLaunchTimeout
	resp, err := httpClient.Do(req)
	if err != nil {
		return insights.LaunchResult{Unverified: true, Message: insightsAmbiguousMessage}, nil
	}
	defer resp.Body.Close()
	// Do has been entered: a transport or response failure after this point
	// cannot prove whether the daemon enqueued the job. Never retry it.
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		if resp.StatusCode >= 500 && resp.StatusCode <= 599 {
			return insights.LaunchResult{Unverified: true, Message: insightsAmbiguousMessage}, nil
		}
		return insights.LaunchResult{}, fmt.Errorf("RoboRev daemon returned %d", resp.StatusCode)
	}
	mediaType, _, mediaErr := mime.ParseMediaType(resp.Header.Get("Content-Type"))
	if mediaErr != nil || !strings.EqualFold(mediaType, "application/json") {
		return insights.LaunchResult{Unverified: true, Message: insightsAmbiguousMessage}, nil
	}
	data, readErr := io.ReadAll(io.LimitReader(resp.Body, insights.MaxReceiptBytes+1))
	if readErr != nil || len(data) > insights.MaxReceiptBytes || !utf8.Valid(data) {
		return insights.LaunchResult{Unverified: true, Message: insightsAmbiguousMessage}, nil
	}
	if !c.generationCurrent(generation) {
		return insights.LaunchResult{Unverified: true, Message: "Launch outcome could not be verified because the RoboRev binding changed. A RoboRev Insights job may already have been queued."}, nil
	}
	receipt, parseErr := parseInsightsEnqueueResponse(data, resp.StatusCode, since, request)
	if parseErr != nil {
		return insights.LaunchResult{Unverified: true, Message: insightsAmbiguousMessage}, nil
	}
	return insights.LaunchResult{Receipt: &receipt}, nil
}

const insightsAmbiguousMessage = "Launch outcome could not be verified. A RoboRev Insights job may already have been queued."

func insightsSince(window insights.Window) string {
	days := 30
	switch window {
	case insights.Window7d:
		days = 7
	case insights.Window90d:
		days = 90
	}
	return time.Now().UTC().AddDate(0, 0, -days).Format(time.RFC3339)
}

// parseInsightsEnqueueResponse extracts only the stable fields needed by the
// browser workflow. The 201 body is a full ReviewJob and may contain paths,
// prompts, command lines, errors, or token data; none of those values are
// decoded into a returned error or DTO.
func parseInsightsEnqueueResponse(raw []byte, status int, since string, request insights.Request) (insights.Receipt, error) {
	if err := rejectDuplicateJSONKeys(raw); err != nil {
		return insights.Receipt{}, err
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var object map[string]json.RawMessage
	if err := decoder.Decode(&object); err != nil || object == nil {
		return insights.Receipt{}, errors.New("malformed Insights enqueue response")
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return insights.Receipt{}, errors.New("malformed Insights enqueue response")
	}
	if status == http.StatusOK {
		var skipped bool
		var reason string
		if err := json.Unmarshal(object["skipped"], &skipped); err != nil || !skipped || json.Unmarshal(object["reason"], &reason) != nil || strings.TrimSpace(reason) == "" {
			return insights.Receipt{}, errors.New("malformed Insights skip response")
		}
		encoded, _ := json.Marshal(struct {
			Skipped bool   `json:"skipped"`
			Reason  string `json:"reason"`
			Since   string `json:"since"`
		}{true, reason, since})
		return insights.ParseReceipt(encoded)
	}
	var id json.Number
	if err := json.Unmarshal(object["id"], &id); err != nil {
		return insights.Receipt{}, errors.New("malformed Insights job response")
	}
	jobID, err := strconv.ParseInt(string(id), 10, 64)
	if err != nil || jobID <= 0 || jobID > insights.MaxJobID {
		return insights.Receipt{}, errors.New("malformed Insights job ID")
	}
	var agent string
	if err := json.Unmarshal(object["agent"], &agent); err != nil || strings.TrimSpace(agent) == "" || len(agent) > insights.MaxReceiptString || !utf8.ValidString(agent) {
		return insights.Receipt{}, errors.New("malformed Insights agent")
	}
	if value, ok := object["job_type"]; ok {
		var jobType string
		if json.Unmarshal(value, &jobType) != nil || jobType != "insights" {
			return insights.Receipt{}, errors.New("malformed Insights job type")
		}
	}
	if value, ok := object["repo_path"]; ok {
		var path string
		if json.Unmarshal(value, &path) != nil || !gitx.SameRepositoryPath(path, request.RepoPath) {
			return insights.Receipt{}, errors.New("malformed Insights repository")
		}
	}
	if value, ok := object["branch"]; ok {
		var branch string
		if json.Unmarshal(value, &branch) != nil || branch != request.Branch {
			return insights.Receipt{}, errors.New("malformed Insights branch")
		}
	}
	encoded, _ := json.Marshal(struct {
		JobID int64  `json:"job_id"`
		Agent string `json:"agent"`
		Since string `json:"since"`
	}{jobID, agent, since})
	return insights.ParseReceipt(encoded)
}

func rejectDuplicateJSONKeys(raw []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	if err := walkJSONValue(decoder); err != nil {
		return errors.New("malformed Insights enqueue response")
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return errors.New("malformed Insights enqueue response")
	}
	return nil
}

func walkJSONValue(decoder *json.Decoder) error {
	token, err := decoder.Token()
	if err != nil {
		return err
	}
	delimiter, ok := token.(json.Delim)
	if !ok {
		return nil
	}
	switch delimiter {
	case '{':
		seen := map[string]struct{}{}
		for decoder.More() {
			key, err := decoder.Token()
			if err != nil {
				return err
			}
			name, ok := key.(string)
			if !ok {
				return errors.New("object key is not a string")
			}
			if _, exists := seen[name]; exists {
				return errors.New("duplicate object key")
			}
			seen[name] = struct{}{}
			if err := walkJSONValue(decoder); err != nil {
				return err
			}
		}
	case '[':
		for decoder.More() {
			if err := walkJSONValue(decoder); err != nil {
				return err
			}
		}
	default:
		return errors.New("unexpected JSON delimiter")
	}
	_, err = decoder.Token()
	return err
}

func sameArgs(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}
