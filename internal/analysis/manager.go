// Package analysis owns the short-lived, introspection-only catalog and prompt
// adapters. v0.64 analysis enqueue is intentionally disabled because the
// upstream command runs analysis jobs in agentic mode.
package analysis

import (
	"context"
	"errors"
	"strings"

	"github.com/froze/roborev-dashboard/internal/roborev"
	"github.com/froze/roborev-dashboard/internal/roborevcompat"
)

type ErrorKind string

const (
	ErrorInvalid     ErrorKind = "invalid"
	ErrorUnavailable ErrorKind = "unavailable"
)

type Error struct {
	Kind ErrorKind
	Err  error
}

func (e *Error) Error() string {
	if e == nil || e.Err == nil {
		return string(ErrorUnavailable)
	}
	return e.Err.Error()
}

func (e *Error) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
}

func typed(kind ErrorKind, err error) error { return &Error{Kind: kind, Err: err} }

type Service interface {
	roborev.AnalysisService
	Installation(context.Context) roborev.Installation
}

type Dependencies struct {
	RoboRev Service
}

type Manager struct {
	service Service
}

func NewManager(deps Dependencies) (*Manager, error) {
	if deps.RoboRev == nil {
		return nil, errors.New("analysis service is required")
	}
	return &Manager{service: deps.RoboRev}, nil
}

func analysisCapability(installation roborev.Installation) error {
	if !installation.Installed || !roborevcompat.IsSupportedVersion(installation.Version) || !installation.Capabilities.AnalyzeIntrospection {
		return roborev.ErrUnsupportedAnalyze
	}
	return nil
}

func (m *Manager) Types(ctx context.Context) ([]roborev.AnalysisType, error) {
	if m == nil || m.service == nil {
		return nil, typed(ErrorUnavailable, errors.New("analysis manager is unavailable"))
	}
	if err := analysisCapability(m.service.Installation(ctx)); err != nil {
		return nil, typed(ErrorUnavailable, err)
	}
	types, err := m.service.AnalysisTypes(ctx)
	if err != nil {
		return nil, typed(ErrorUnavailable, err)
	}
	if len(types) == 0 {
		return nil, typed(ErrorUnavailable, errors.New("RoboRev analysis type catalog is empty"))
	}
	seen := make(map[string]struct{}, len(types))
	for _, item := range types {
		if !validType(item.Name) || strings.TrimSpace(item.Description) == "" {
			return nil, typed(ErrorUnavailable, errors.New("RoboRev analysis type catalog is invalid"))
		}
		if _, exists := seen[item.Name]; exists {
			return nil, typed(ErrorUnavailable, errors.New("RoboRev analysis type catalog contains duplicates"))
		}
		seen[item.Name] = struct{}{}
	}
	return types, nil
}

func validType(value string) bool {
	if len(value) == 0 || len(value) > 64 || value[0] < 'a' || value[0] > 'z' {
		return false
	}
	for _, char := range value {
		if (char < 'a' || char > 'z') && (char < '0' || char > '9') && char != '-' {
			return false
		}
	}
	return true
}

func findType(types []roborev.AnalysisType, name string) bool {
	for _, item := range types {
		if item.Name == name {
			return true
		}
	}
	return false
}

func (m *Manager) Prompt(ctx context.Context, analysisType string) (string, error) {
	if m == nil || m.service == nil {
		return "", typed(ErrorUnavailable, errors.New("analysis manager is unavailable"))
	}
	if !validType(analysisType) {
		return "", typed(ErrorInvalid, errors.New("invalid analysis type"))
	}
	types, err := m.Types(ctx)
	if err != nil {
		return "", err
	}
	if !findType(types, analysisType) {
		return "", typed(ErrorInvalid, errors.New("analysis type is not currently available"))
	}
	prompt, err := m.service.AnalysisPrompt(ctx, analysisType)
	if err != nil {
		return "", typed(ErrorUnavailable, err)
	}
	if strings.TrimSpace(prompt) == "" {
		return "", typed(ErrorUnavailable, errors.New("analysis prompt is empty"))
	}
	return prompt, nil
}
