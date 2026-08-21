package analysis

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/froze/roborev-dashboard/internal/roborev"
	"github.com/froze/roborev-dashboard/internal/roborevcompat"
)

type fakeService struct {
	installation  roborev.Installation
	types         []roborev.AnalysisType
	prompt        string
	typesCalls    int
	promptCalls   int
	typesErr      error
	promptErr     error
	promptRequest string
}

func (f *fakeService) Installation(context.Context) roborev.Installation { return f.installation }

func (f *fakeService) AnalysisTypes(context.Context) ([]roborev.AnalysisType, error) {
	f.typesCalls++
	if f.typesErr != nil {
		return nil, f.typesErr
	}
	return append([]roborev.AnalysisType(nil), f.types...), nil
}

func (f *fakeService) AnalysisPrompt(_ context.Context, analysisType string) (string, error) {
	f.promptCalls++
	f.promptRequest = analysisType
	if f.promptErr != nil {
		return "", f.promptErr
	}
	return f.prompt, nil
}

func testManager(t *testing.T) (*Manager, *fakeService) {
	t.Helper()
	service := &fakeService{
		installation: roborev.Installation{
			Installed: true,
			Version:   roborevcompat.Manifest064().Version,
			Status:    json.RawMessage(`{"running":true,"known":true}`),
			Capabilities: roborev.Capabilities{
				AnalyzeIntrospection: true,
			},
		},
		types:  []roborev.AnalysisType{{Name: "refactor", Description: "Suggest refactoring opportunities"}},
		prompt: "prompt template",
	}
	manager, err := NewManager(Dependencies{RoboRev: service})
	if err != nil {
		t.Fatal(err)
	}
	return manager, service
}

func TestTypesValidatesCatalogWithoutExecutionSurface(t *testing.T) {
	manager, service := testManager(t)
	types, err := manager.Types(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(types) != 1 || types[0].Name != "refactor" || service.typesCalls != 1 {
		t.Fatalf("types=%#v calls=%d", types, service.typesCalls)
	}
}

func TestPromptRequiresFreshDiscoveredType(t *testing.T) {
	manager, service := testManager(t)
	prompt, err := manager.Prompt(context.Background(), "refactor")
	if err != nil || prompt != "prompt template" || service.promptRequest != "refactor" {
		t.Fatalf("prompt=%q err=%v request=%q", prompt, err, service.promptRequest)
	}
	service.types = []roborev.AnalysisType{{Name: "security", Description: "Find risks"}}
	if _, err := manager.Prompt(context.Background(), "refactor"); err == nil {
		t.Fatal("stale/unknown type was accepted")
	}
	if service.promptCalls != 1 {
		t.Fatalf("prompt calls=%d, want no service call for unknown type", service.promptCalls)
	}
}

func TestCatalogAndPromptRejectUnavailableCapability(t *testing.T) {
	manager, service := testManager(t)
	service.installation.Capabilities.AnalyzeIntrospection = false
	if _, err := manager.Types(context.Background()); !errors.Is(err, roborev.ErrUnsupportedAnalyze) {
		t.Fatalf("types error=%v, want unsupported", err)
	}
	if service.typesCalls != 0 {
		t.Fatal("catalog service called when capability was unavailable")
	}
	if _, err := manager.Prompt(context.Background(), "refactor"); !errors.Is(err, roborev.ErrUnsupportedAnalyze) {
		t.Fatalf("prompt error=%v, want unsupported", err)
	}
}

func TestPromptRejectsInvalidTypeAndPropagatesServiceError(t *testing.T) {
	manager, service := testManager(t)
	if _, err := manager.Prompt(context.Background(), "../secret"); err == nil {
		t.Fatal("invalid type was accepted")
	}
	service.promptErr = errors.New("prompt transport failed")
	if _, err := manager.Prompt(context.Background(), "refactor"); !errors.Is(err, service.promptErr) {
		t.Fatalf("prompt error=%v, want service error", err)
	}
}
