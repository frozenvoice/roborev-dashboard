package roborev

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode"

	proc "github.com/froze/roborev-dashboard/internal/process"
	"github.com/froze/roborev-dashboard/internal/roborevcompat"
)

const (
	analysisListLimit      = 256 << 10
	analysisPromptLimit    = 2 << 20
	analysisMaxTypes       = 64
	analysisMaxTypeName    = 64
	analysisMaxDescription = 4096
)

var (
	ErrUnsupportedAnalyze       = errors.New("RoboRev v0.64 analyze introspection is unavailable")
	ErrMalformedAnalysisCatalog = errors.New("malformed RoboRev analysis type catalog")
)

// AnalysisService is deliberately limited to the non-executing catalog and
// prompt inspection surface. v0.64 Analyze enqueue is agentic upstream and is
// intentionally not reachable through the Dashboard adapter.
type AnalysisService interface {
	AnalysisTypes(context.Context) ([]AnalysisType, error)
	AnalysisPrompt(context.Context, string) (string, error)
}

type AnalysisType struct {
	Name        string `json:"name"`
	Description string `json:"description"`
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
	if !capabilities(version).AnalyzeIntrospection {
		return Installation{}, ErrUnsupportedAnalyze
	}
	return Installation{Installed: true, Binary: c.BinaryPath(), Version: version, Capabilities: capabilities(version)}, nil
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
		return "", errors.New("analysis prompt output exceeded bounded limit")
	}
	if strings.TrimSpace(result.Stdout) == "" {
		return "", errors.New("analysis prompt is empty")
	}
	return result.Stdout, nil
}

func (c *Client) bindingUnchanged(binary string, generation uint64) bool {
	currentBinary, _, currentGeneration := c.snapshot()
	return currentBinary == binary && currentGeneration == generation
}

func (c *Client) runSnapshot(ctx context.Context, binary string, args []string, timeout time.Duration, limit int) (proc.Result, error) {
	if binary == "" {
		return proc.Result{}, errors.New("roborev is not installed")
	}
	result, err := c.Runner.Run(ctx, proc.Command{Executable: binary, Args: args, Timeout: timeout, OutputLimit: limit})
	if err != nil {
		message := strings.TrimSpace(result.Stderr)
		if message == "" {
			message = err.Error()
		}
		return result, fmt.Errorf("%s: %w", message, err)
	}
	return result, nil
}
