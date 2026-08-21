package roborev

import (
	"bytes"
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"
	"unicode"

	proc "github.com/froze/roborev-dashboard/internal/process"
	"github.com/froze/roborev-dashboard/internal/roborevcompat"
)

const (
	AnalysisScopeBranch     = "branch"
	AnalysisScopeRepository = "repository"
	analysisListLimit       = 256 << 10
	analysisPromptLimit     = 2 << 20
	analysisReceiptLimit    = 2 << 20
	analysisMaxTypes        = 64
	analysisMaxTypeName     = 64
	analysisMaxDescription  = 4096
	analysisMaxFiles        = 4096
	analysisMaxFileName     = 4096
	analysisMaxAgent        = 256
	analysisMaxFileBytes    = 1 << 20
)

var (
	ErrUnsupportedAnalyze       = errors.New("RoboRev v0.64 analyze capability is unavailable")
	ErrMalformedAnalysisCatalog = errors.New("malformed RoboRev analysis type catalog")
	ErrMalformedAnalysisReceipt = errors.New("malformed RoboRev analysis receipt")
	// ErrAmbiguousAnalysisEnqueue is returned when the process may have sent an
	// enqueue request but its receipt could not be validated. Callers must not
	// report this as a confirmed failure or retry automatically.
	ErrAmbiguousAnalysisEnqueue = errors.New("RoboRev analysis enqueue result is ambiguous")
)

// AnalysisService is intentionally separate from Service. Existing server
// fakes and adapters do not need to grow an Analyze method until they expose
// the version-gated capability.
type AnalysisService interface {
	AnalysisTypes(context.Context) ([]AnalysisType, error)
	AnalysisPrompt(context.Context, string) (string, error)
	Analyze(context.Context, AnalyzeRequest) (AnalysisReceipt, error)
}

type AnalysisType struct {
	Name        string `json:"name"`
	Description string `json:"description"`
}

type AnalyzeRequest struct {
	RepositoryPath string
	AnalysisType   string
	Scope          string
}

type AnalysisJob struct {
	ID    int64  `json:"id"`
	Agent string `json:"agent"`
}

type AnalysisReceipt struct {
	Jobs         []AnalysisJob `json:"jobs"`
	AnalysisType string        `json:"analysis_type"`
	Files        []string      `json:"files"`
}

func validAnalysisType(value string) bool {
	if len(value) == 0 || len(value) > analysisMaxTypeName {
		return false
	}
	for index, char := range value {
		if (char < 'a' || char > 'z') && (char < '0' || char > '9') && char != '-' {
			return false
		}
		if index == 0 && (char < 'a' || char > 'z') {
			return false
		}
	}
	return true
}

func validAnalysisScope(scope string) bool {
	return scope == AnalysisScopeBranch || scope == AnalysisScopeRepository
}

func ValidAnalysisScope(scope string) bool { return validAnalysisScope(scope) }

// AnalysisArgs is the sole production builder for the supported analysis
// command. The browser supplies only the semantic scope enum; ./... and the
// branch flag are generated here.
func AnalysisArgs(analysisType, scope string) ([]string, error) {
	if !validAnalysisType(analysisType) {
		return nil, errors.New("invalid analysis type")
	}
	if !validAnalysisScope(scope) {
		return nil, errors.New("invalid analysis scope")
	}
	args := []string{"analyze", analysisType}
	if scope == AnalysisScopeBranch {
		args = append(args, "--branch=HEAD")
	} else {
		args = append(args, "./...")
	}
	return append(args, "--json"), nil
}

func AnalysisListArgs() []string { return []string{"analyze", "--list"} }

func AnalysisPromptArgs(analysisType string) ([]string, error) {
	if !validAnalysisType(analysisType) {
		return nil, errors.New("invalid analysis type")
	}
	return []string{"analyze", analysisType, "--show-prompt"}, nil
}

// ParseAnalysisTypes parses the intentionally human-readable v0.64 catalog.
// It accepts flexible indentation but requires the expected header and one
// safe-slug/description entry per non-empty body line.
func ParseAnalysisTypes(output string) ([]AnalysisType, error) {
	if len(output) == 0 || len(output) > analysisListLimit {
		return nil, fmt.Errorf("%w: output exceeds %d bytes", ErrMalformedAnalysisCatalog, analysisListLimit)
	}
	for _, char := range output {
		if unicode.IsControl(char) && char != '\n' && char != '\r' && char != '\t' {
			return nil, fmt.Errorf("%w: control character in output", ErrMalformedAnalysisCatalog)
		}
	}
	lines := strings.Split(strings.ReplaceAll(output, "\r\n", "\n"), "\n")
	header := -1
	for index, line := range lines {
		if strings.TrimSpace(line) == "" {
			continue
		}
		if strings.TrimSpace(line) == "Available analysis types:" {
			header = index
		}
		break
	}
	if header < 0 {
		return nil, fmt.Errorf("%w: expected header", ErrMalformedAnalysisCatalog)
	}
	entries := make([]AnalysisType, 0, len(lines)-header-1)
	seen := make(map[string]struct{})
	for _, raw := range lines[header+1:] {
		line := strings.TrimSpace(raw)
		if line == "" {
			continue
		}
		leading := len(raw) - len(strings.TrimLeft(raw, " \t"))
		if leading == 0 || leading > 32 {
			return nil, fmt.Errorf("%w: unreasonable entry indentation", ErrMalformedAnalysisCatalog)
		}
		fields := strings.Fields(line)
		if len(fields) < 2 {
			return nil, fmt.Errorf("%w: entry must include a description", ErrMalformedAnalysisCatalog)
		}
		name := fields[0]
		description := strings.TrimSpace(strings.TrimPrefix(line, name))
		if !validAnalysisType(name) || description == "" || len(description) > analysisMaxDescription {
			return nil, fmt.Errorf("%w: invalid entry", ErrMalformedAnalysisCatalog)
		}
		for _, char := range description {
			if unicode.IsControl(char) {
				return nil, fmt.Errorf("%w: control character in description", ErrMalformedAnalysisCatalog)
			}
		}
		if _, exists := seen[name]; exists {
			return nil, fmt.Errorf("%w: duplicate analysis type %q", ErrMalformedAnalysisCatalog, name)
		}
		seen[name] = struct{}{}
		entries = append(entries, AnalysisType{Name: name, Description: description})
		if len(entries) > analysisMaxTypes {
			return nil, fmt.Errorf("%w: too many analysis types", ErrMalformedAnalysisCatalog)
		}
	}
	if len(entries) == 0 {
		return nil, fmt.Errorf("%w: catalog is empty", ErrMalformedAnalysisCatalog)
	}
	return entries, nil
}

func decodeAnalysisReceipt(raw string, requestedType string) (AnalysisReceipt, error) {
	if !validAnalysisType(requestedType) {
		return AnalysisReceipt{}, fmt.Errorf("%w: requested analysis type is invalid", ErrMalformedAnalysisReceipt)
	}
	if len(raw) == 0 || len(raw) > analysisReceiptLimit {
		return AnalysisReceipt{}, fmt.Errorf("%w: output exceeds %d bytes", ErrMalformedAnalysisReceipt, analysisReceiptLimit)
	}
	decoder := json.NewDecoder(bytes.NewReader([]byte(raw)))
	var receipt struct {
		Jobs         []AnalysisJob `json:"jobs"`
		AnalysisType *string       `json:"analysis_type"`
		Files        *[]string     `json:"files"`
	}
	if err := decoder.Decode(&receipt); err != nil {
		return AnalysisReceipt{}, fmt.Errorf("%w: invalid JSON", ErrMalformedAnalysisReceipt)
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return AnalysisReceipt{}, fmt.Errorf("%w: trailing JSON value", ErrMalformedAnalysisReceipt)
	}
	if receipt.AnalysisType == nil || *receipt.AnalysisType != requestedType || receipt.Files == nil || len(receipt.Jobs) != 1 {
		return AnalysisReceipt{}, fmt.Errorf("%w: receipt identity or job count is invalid", ErrMalformedAnalysisReceipt)
	}
	if len(*receipt.Files) == 0 || len(*receipt.Files) > analysisMaxFiles {
		return AnalysisReceipt{}, fmt.Errorf("%w: too many files", ErrMalformedAnalysisReceipt)
	}
	seen := make(map[int64]struct{}, len(receipt.Jobs))
	for _, job := range receipt.Jobs {
		if job.ID <= 0 || strings.TrimSpace(job.Agent) == "" || len(job.Agent) > analysisMaxAgent {
			return AnalysisReceipt{}, fmt.Errorf("%w: invalid job", ErrMalformedAnalysisReceipt)
		}
		for _, char := range job.Agent {
			if unicode.IsControl(char) {
				return AnalysisReceipt{}, fmt.Errorf("%w: control character in agent", ErrMalformedAnalysisReceipt)
			}
		}
		if _, exists := seen[job.ID]; exists {
			return AnalysisReceipt{}, fmt.Errorf("%w: duplicate job ID", ErrMalformedAnalysisReceipt)
		}
		seen[job.ID] = struct{}{}
	}
	fileBytes := 0
	for _, file := range *receipt.Files {
		if strings.TrimSpace(file) == "" || len(file) > analysisMaxFileName {
			return AnalysisReceipt{}, fmt.Errorf("%w: invalid file", ErrMalformedAnalysisReceipt)
		}
		for _, char := range file {
			if unicode.IsControl(char) {
				return AnalysisReceipt{}, fmt.Errorf("%w: control character in file", ErrMalformedAnalysisReceipt)
			}
		}
		fileBytes += len(file)
		if fileBytes > analysisMaxFileBytes {
			return AnalysisReceipt{}, fmt.Errorf("%w: file list exceeds bounded size", ErrMalformedAnalysisReceipt)
		}
	}
	return AnalysisReceipt{Jobs: append([]AnalysisJob(nil), receipt.Jobs...), AnalysisType: *receipt.AnalysisType, Files: append([]string(nil), (*receipt.Files)...)}, nil
}

// AnalysisReceiptFromJSON is exported for deterministic adapter tests and
// keeps receipt validation at the process boundary.
func AnalysisReceiptFromJSON(raw string, requestedType string) (AnalysisReceipt, error) {
	return decodeAnalysisReceipt(raw, requestedType)
}

func DecodeAnalysisReceipt(raw json.RawMessage, requestedType string) (AnalysisReceipt, error) {
	return decodeAnalysisReceipt(string(raw), requestedType)
}

func ParseAnalysisCatalog(output string) ([]AnalysisType, error) {
	return ParseAnalysisTypes(output)
}

func (c *Client) analysisAvailable(ctx context.Context) (Installation, error) {
	version, err := c.detectVersion(ctx)
	if err != nil {
		return Installation{}, err
	}
	if !roborevcompat.IsSupportedVersion(version) {
		return Installation{}, fmt.Errorf("%w: %s", ErrUnsupportedAnalyze, version)
	}
	if !capabilities(version).AnalyzeCommand {
		return Installation{}, ErrUnsupportedAnalyze
	}
	return Installation{Installed: true, Binary: c.BinaryPath(), Version: version, Capabilities: capabilities(version)}, nil
}

func (c *Client) analysisReady(ctx context.Context) (Installation, error) {
	installation, err := c.analysisAvailable(ctx)
	if err != nil {
		return Installation{}, err
	}
	health, err := c.healthStatus(ctx)
	if err != nil {
		return Installation{}, fmt.Errorf("daemon health preflight failed: %w", err)
	}
	if !health.Healthy {
		return Installation{}, ErrDaemonUnhealthy
	}
	if !roborevcompat.IsSupportedVersion(strings.TrimSpace(health.Version)) || strings.TrimSpace(health.Version) != installation.Version {
		return Installation{}, fmt.Errorf("%w: daemon=%s cli=%s", ErrUnsupportedAnalyze, strings.TrimSpace(health.Version), installation.Version)
	}
	return installation, nil
}

func (c *Client) AnalysisTypes(ctx context.Context) ([]AnalysisType, error) {
	binary, _, generation := c.snapshot()
	if _, err := c.analysisAvailable(ctx); err != nil {
		return nil, err
	}
	if !c.bindingUnchanged(binary, generation) {
		return nil, ErrBindingChanged
	}
	result, err := c.runSnapshot(ctx, binary, AnalysisListArgs(), 10*time.Second, analysisListLimit)
	if err != nil {
		return nil, err
	}
	if result.StdoutTruncated || result.StderrTruncated {
		return nil, fmt.Errorf("%w: bounded output was truncated", ErrMalformedAnalysisCatalog)
	}
	return ParseAnalysisTypes(result.Stdout)
}

func (c *Client) AnalysisPrompt(ctx context.Context, analysisType string) (string, error) {
	args, err := AnalysisPromptArgs(analysisType)
	if err != nil {
		return "", err
	}
	binary, _, generation := c.snapshot()
	if _, err := c.analysisAvailable(ctx); err != nil {
		return "", err
	}
	if !c.bindingUnchanged(binary, generation) {
		return "", ErrBindingChanged
	}
	result, err := c.runSnapshot(ctx, binary, args, 10*time.Second, analysisPromptLimit)
	if err != nil {
		return "", err
	}
	if result.StdoutTruncated || result.StderrTruncated {
		return "", fmt.Errorf("analysis prompt output exceeded bounded limit")
	}
	if strings.TrimSpace(result.Stdout) == "" {
		return "", errors.New("analysis prompt is empty")
	}
	return result.Stdout, nil
}

func (c *Client) Analyze(ctx context.Context, request AnalyzeRequest) (AnalysisReceipt, error) {
	args, err := AnalysisArgs(request.AnalysisType, request.Scope)
	if err != nil {
		return AnalysisReceipt{}, err
	}
	if strings.TrimSpace(request.RepositoryPath) == "" {
		return AnalysisReceipt{}, errors.New("repository path is required")
	}
	binary, _, generation := c.snapshot()
	if _, err := c.analysisReady(ctx); err != nil {
		return AnalysisReceipt{}, err
	}
	if !c.bindingUnchanged(binary, generation) {
		return AnalysisReceipt{}, ErrBindingChanged
	}
	result, runErr := c.runSnapshotDir(ctx, binary, args, request.RepositoryPath, 45*time.Second, analysisReceiptLimit)
	if runErr != nil {
		return AnalysisReceipt{}, fmt.Errorf("%w: %v", ErrAmbiguousAnalysisEnqueue, runErr)
	}
	if result.StdoutTruncated || result.StderrTruncated {
		return AnalysisReceipt{}, fmt.Errorf("%w: bounded output was truncated", ErrAmbiguousAnalysisEnqueue)
	}
	receipt, err := decodeAnalysisReceipt(result.Stdout, request.AnalysisType)
	if err != nil {
		return AnalysisReceipt{}, fmt.Errorf("%w: %v", ErrAmbiguousAnalysisEnqueue, err)
	}
	return receipt, nil
}

func (c *Client) bindingUnchanged(binary string, generation uint64) bool {
	currentBinary, _, currentGeneration := c.snapshot()
	return currentBinary == binary && currentGeneration == generation
}

func (c *Client) runSnapshot(ctx context.Context, binary string, args []string, timeout time.Duration, limit int) (proc.Result, error) {
	return c.runSnapshotDir(ctx, binary, args, "", timeout, limit)
}

func (c *Client) runSnapshotDir(ctx context.Context, binary string, args []string, dir string, timeout time.Duration, limit int) (proc.Result, error) {
	result, err := c.Runner.Run(ctx, proc.Command{Executable: binary, Args: args, Dir: dir, Timeout: timeout, OutputLimit: limit})
	if err != nil {
		message := strings.TrimSpace(result.Stderr)
		if message == "" {
			message = err.Error()
		}
		return result, fmt.Errorf("%s: %w", message, err)
	}
	return result, nil
}

func sameAnalysisReceiptType(receipt AnalysisReceipt, requested string) bool {
	return subtle.ConstantTimeCompare([]byte(receipt.AnalysisType), []byte(requested)) == 1
}
