package roborev

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	proc "github.com/froze/roborev-dashboard/internal/process"
)

func TestAnalysisArgsUseExactV064Contracts(t *testing.T) {
	for _, test := range []struct {
		name  string
		scope string
		want  []string
	}{
		{name: "branch", scope: AnalysisScopeBranch, want: []string{"analyze", "refactor", "--branch=HEAD", "--json"}},
		{name: "repository", scope: AnalysisScopeRepository, want: []string{"analyze", "refactor", "./...", "--json"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			got, err := AnalysisArgs("refactor", test.scope)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got, test.want) {
				t.Fatalf("args = %#v, want %#v", got, test.want)
			}
		})
	}
	if _, err := AnalysisArgs("../secret", AnalysisScopeRepository); err == nil {
		t.Fatal("unsafe analysis type accepted")
	}
}

func TestParseAnalysisTypesStrictCatalog(t *testing.T) {
	output := "Available analysis types:\n\n  refactor    Suggest refactoring opportunities\n  security Find concrete security risks in existing code\n"
	got, err := ParseAnalysisTypes(output)
	if err != nil {
		t.Fatal(err)
	}
	if want := []AnalysisType{{Name: "refactor", Description: "Suggest refactoring opportunities"}, {Name: "security", Description: "Find concrete security risks in existing code"}}; !reflect.DeepEqual(got, want) {
		t.Fatalf("catalog = %#v, want %#v", got, want)
	}
	for name, malformed := range map[string]string{
		"header":    "refactor Find things",
		"preamble":  "notice\nAvailable analysis types:\n  refactor One\n",
		"duplicate": "Available analysis types:\n  refactor One\n  refactor Two\n",
		"slug":      "Available analysis types:\n  ../bad One\n",
		"indent":    "Available analysis types:\nrefactor One\n",
		"empty":     "Available analysis types:\n  refactor\n",
		"control":   "Available analysis types:\n  refactor One\x00\n",
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := ParseAnalysisTypes(malformed); !errors.Is(err, ErrMalformedAnalysisCatalog) {
				t.Fatalf("error = %v", err)
			}
		})
	}
	if _, err := ParseAnalysisTypes("Available analysis types:\n  refactor One\n" + strings.Repeat("x", analysisListLimit)); err == nil {
		t.Fatal("oversized catalog accepted")
	}
}

func TestDecodeAnalysisReceiptRequiresSingleValidatedJob(t *testing.T) {
	valid := `{"jobs":[{"id":123,"agent":"codex"}],"analysis_type":"refactor","files":["./..."]}`
	receipt, err := AnalysisReceiptFromJSON(valid, "refactor")
	if err != nil || receipt.Jobs[0].ID != 123 {
		t.Fatalf("receipt = %#v, error = %v", receipt, err)
	}
	for name, raw := range map[string]string{
		"trailing":      valid + " {}",
		"wrong type":    strings.Replace(valid, `"refactor"`, `"security"`, 1),
		"zero id":       strings.Replace(valid, `123`, `0`, 1),
		"empty agent":   strings.Replace(valid, `"codex"`, `""`, 1),
		"missing files": `{"jobs":[{"id":123,"agent":"codex"}],"analysis_type":"refactor"}`,
		"empty files":   `{"jobs":[{"id":123,"agent":"codex"}],"analysis_type":"refactor","files":[]}`,
		"two jobs":      `{"jobs":[{"id":123,"agent":"codex"},{"id":124,"agent":"codex"}],"analysis_type":"refactor","files":[]}`,
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := AnalysisReceiptFromJSON(raw, "refactor"); !errors.Is(err, ErrMalformedAnalysisReceipt) {
				t.Fatalf("error = %v", err)
			}
		})
	}
}

type analysisRunner struct {
	calls      []proc.Command
	processErr error
}

func (r *analysisRunner) Run(_ context.Context, command proc.Command) (proc.Result, error) {
	r.calls = append(r.calls, command)
	switch strings.Join(command.Args, " ") {
	case "version --json":
		return proc.Result{Stdout: `{"name":"roborev","version":"v0.64.0"}`}, nil
	case "analyze --list":
		return proc.Result{Stdout: "Available analysis types:\n  refactor Suggest refactoring opportunities\n"}, nil
	case "analyze refactor --show-prompt":
		return proc.Result{Stdout: "prompt"}, nil
	case "analyze refactor --branch=HEAD --json":
		if r.processErr != nil {
			return proc.Result{}, r.processErr
		}
		return proc.Result{Stdout: `{"jobs":[{"id":42,"agent":"codex"}],"analysis_type":"refactor","files":["./..."]}`}, nil
	default:
		return proc.Result{}, errors.New("unexpected command")
	}
}

func TestClientAnalyzeProcessErrorsAreAmbiguous(t *testing.T) {
	daemon := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"$schema":"http://127.0.0.1:7373/HealthStatus.json","healthy":true,"version":"v0.64.0"}`))
	}))
	defer daemon.Close()
	runner := &analysisRunner{processErr: context.DeadlineExceeded}
	client, err := NewClient("roborev", runner, daemon.URL)
	if err != nil {
		t.Fatal(err)
	}
	_, err = client.Analyze(context.Background(), AnalyzeRequest{RepositoryPath: `C:\repo`, AnalysisType: "refactor", Scope: AnalysisScopeBranch})
	if !errors.Is(err, ErrAmbiguousAnalysisEnqueue) {
		t.Fatalf("error = %v, want ambiguous enqueue error", err)
	}
}

func TestClientAnalyzeUsesStrictHealthyMatchingDaemonPreflight(t *testing.T) {
	tests := []struct {
		name string
		body string
		want error
	}{
		{name: "malformed", body: `{`},
		{name: "unhealthy", body: `{"$schema":"http://127.0.0.1:7373/HealthStatus.json","healthy":false,"version":"v0.64.0"}`, want: ErrDaemonUnhealthy},
		{name: "missing version", body: `{"$schema":"http://127.0.0.1:7373/HealthStatus.json","healthy":true}`},
		{name: "mismatched version", body: `{"$schema":"http://127.0.0.1:7373/HealthStatus.json","healthy":true,"version":"v0.65.0"}`},
		{name: "missing schema", body: `{"healthy":true,"version":"v0.64.0"}`},
		{name: "mismatched schema", body: `{"$schema":"http://example.invalid/HealthStatus.json","healthy":true,"version":"v0.64.0"}`},
		{name: "unknown field", body: `{"$schema":"http://127.0.0.1:7373/HealthStatus.json","healthy":true,"version":"v0.64.0","extra":true}`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			daemon := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				_, _ = w.Write([]byte(test.body))
			}))
			defer daemon.Close()
			client, err := NewClient("roborev", &analysisRunner{}, daemon.URL)
			if err != nil {
				t.Fatal(err)
			}
			_, err = client.Analyze(context.Background(), AnalyzeRequest{RepositoryPath: `C:\repo`, AnalysisType: "refactor", Scope: AnalysisScopeBranch})
			if err == nil {
				t.Fatal("invalid daemon preflight was accepted")
			}
			if test.want != nil && !errors.Is(err, test.want) {
				t.Fatalf("error = %v, want %v", err, test.want)
			}
		})
	}
}

func TestClientAnalyzeRejectsRebindDuringHealthPreflight(t *testing.T) {
	var client *Client
	daemon := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		client.bindingMu.Lock()
		client.binding.generation++
		client.bindingMu.Unlock()
		_, _ = w.Write([]byte(`{"$schema":"http://127.0.0.1:7373/HealthStatus.json","healthy":true,"version":"v0.64.0"}`))
	}))
	defer daemon.Close()
	runner := &analysisRunner{}
	var err error
	client, err = NewClient("roborev", runner, daemon.URL)
	if err != nil {
		t.Fatal(err)
	}
	_, err = client.Analyze(context.Background(), AnalyzeRequest{RepositoryPath: `C:\repo`, AnalysisType: "refactor", Scope: AnalysisScopeBranch})
	if !errors.Is(err, ErrBindingChanged) {
		t.Fatalf("error = %v, want binding change", err)
	}
	for _, call := range runner.calls {
		if strings.HasPrefix(strings.Join(call.Args, " "), "analyze ") {
			t.Fatal("Analyze command ran after binding changed")
		}
	}
}

func TestClientAnalysisAdaptersUseTypedCommandsAndDaemonHealth(t *testing.T) {
	daemon := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/health" {
			t.Fatalf("unexpected daemon path %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"$schema":"http://127.0.0.1:7373/HealthStatus.json","healthy":true,"version":"v0.64.0"}`))
	}))
	defer daemon.Close()
	runner := &analysisRunner{}
	client, err := NewClient("roborev", runner, daemon.URL)
	if err != nil {
		t.Fatal(err)
	}
	types, err := client.AnalysisTypes(context.Background())
	if err != nil || len(types) != 1 || types[0].Name != "refactor" {
		t.Fatalf("types = %#v, error = %v", types, err)
	}
	if _, err := client.AnalysisPrompt(context.Background(), "refactor"); err != nil {
		t.Fatal(err)
	}
	receipt, err := client.Analyze(context.Background(), AnalyzeRequest{RepositoryPath: `C:\repo`, AnalysisType: "refactor", Scope: AnalysisScopeBranch})
	if err != nil || receipt.Jobs[0].ID != 42 {
		t.Fatalf("receipt = %#v, error = %v", receipt, err)
	}
	last := runner.calls[len(runner.calls)-1]
	if last.Dir != `C:\repo` || !reflect.DeepEqual(last.Args, []string{"analyze", "refactor", "--branch=HEAD", "--json"}) {
		t.Fatalf("command = %#v", last)
	}
}
