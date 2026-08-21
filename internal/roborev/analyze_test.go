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

func TestAnalysisIntrospectionUsesExactV064Commands(t *testing.T) {
	if got := AnalysisListArgs(); !reflect.DeepEqual(got, []string{"analyze", "--list"}) {
		t.Fatalf("list args = %#v", got)
	}
	got, err := AnalysisPromptArgs("refactor")
	if err != nil || !reflect.DeepEqual(got, []string{"analyze", "refactor", "--show-prompt"}) {
		t.Fatalf("prompt args = %#v error=%v", got, err)
	}
	if _, err := AnalysisPromptArgs("../secret"); err == nil {
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
}

type analysisRunner struct {
	calls []proc.Command
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
	default:
		return proc.Result{}, errors.New("unexpected command")
	}
}

type bindingRaceRunner struct {
	client *Client
	inner  analysisRunner
}

func (r *bindingRaceRunner) Run(ctx context.Context, command proc.Command) (proc.Result, error) {
	if strings.Join(command.Args, " ") == "version --json" {
		r.client.bindingMu.Lock()
		r.client.binding.generation++
		r.client.bindingMu.Unlock()
	}
	return r.inner.Run(ctx, command)
}

func TestClientAnalysisAdaptersUseIntrospectionOnly(t *testing.T) {
	daemon := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/health" {
			t.Fatalf("unexpected daemon path %s", r.URL.Path)
		}
		_, _ = w.Write([]byte(`{"healthy":true,"version":"v0.64.0","$schema":"any-string"}`))
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
	if prompt, err := client.AnalysisPrompt(context.Background(), "refactor"); err != nil || prompt != "prompt" {
		t.Fatalf("prompt = %q, error = %v", prompt, err)
	}
	for _, call := range runner.calls {
		switch strings.Join(call.Args, " ") {
		case "version --json", "analyze --list", "analyze refactor --show-prompt":
		default:
			t.Fatalf("unexpected analysis execution args: %#v", call.Args)
		}
	}
}

func TestDaemonHealthSchemaIsOptionalButTypedWhenPresent(t *testing.T) {
	tests := []struct {
		name string
		body string
		want bool
	}{
		{name: "missing schema accepted", body: `{"healthy":true,"version":"v0.64.0"}`, want: true},
		{name: "arbitrary string accepted", body: `{"healthy":true,"version":"v0.64.0","$schema":"custom"}`, want: true},
		{name: "null rejected", body: `{"healthy":true,"version":"v0.64.0","$schema":null}`},
		{name: "number rejected", body: `{"healthy":true,"version":"v0.64.0","$schema":7}`},
		{name: "unknown field rejected", body: `{"healthy":true,"version":"v0.64.0","unknown":true}`},
		{name: "trailing JSON rejected", body: `{"healthy":true,"version":"v0.64.0"}{}`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(test.body)) }))
			defer server.Close()
			client, err := NewClient("roborev", &analysisRunner{}, server.URL)
			if err != nil {
				t.Fatal(err)
			}
			online, healthErr := client.DaemonHealth(context.Background())
			if !online || (healthErr == nil) != test.want {
				t.Fatalf("health online=%v err=%v, want online=true err=%v", online, healthErr, !test.want)
			}
		})
	}
}

func TestClientAnalysisTypesRejectsRebindBeforeCommand(t *testing.T) {
	var client *Client
	runner := &bindingRaceRunner{}
	var err error
	client, err = NewClient("roborev", runner, "http://127.0.0.1:7373")
	if err != nil {
		t.Fatal(err)
	}
	runner.client = client
	if _, err := client.AnalysisTypes(context.Background()); !errors.Is(err, ErrBindingChanged) {
		t.Fatalf("types error=%v, want binding change", err)
	}
	for _, call := range runner.inner.calls {
		if strings.Join(call.Args, " ") == "analyze --list" {
			t.Fatal("list command ran after binding changed")
		}
	}
}
