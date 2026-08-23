package roborev

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"syscall"
	"testing"

	proc "github.com/froze/roborev-dashboard/internal/process"
	"github.com/froze/roborev-dashboard/internal/repoinventory"
)

type repositoryInventoryRunner struct{ version string }

func (r repositoryInventoryRunner) Run(_ context.Context, command proc.Command) (proc.Result, error) {
	if len(command.Args) == 2 && command.Args[0] == "version" && command.Args[1] == "--json" {
		return proc.Result{Stdout: fmt.Sprintf(`{"name":"roborev","version":%q}`, r.version)}, nil
	}
	return proc.Result{}, errors.New("unexpected process command")
}

func TestRepositoryInventoryUsesExactTypedTransport(t *testing.T) {
	var inventoryCalls int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/health":
			if r.Method != http.MethodGet {
				t.Errorf("health method=%s", r.Method)
			}
			_, _ = io.WriteString(w, `{"healthy":true,"version":"v0.64.0"}`)
		case "/api/repos":
			inventoryCalls++
			if r.Method != http.MethodGet || r.URL.RawQuery != "" {
				t.Errorf("inventory request method=%s query=%q", r.Method, r.URL.RawQuery)
			}
			body, err := io.ReadAll(r.Body)
			if err != nil {
				t.Errorf("read body: %v", err)
			}
			if len(body) != 0 || r.ContentLength != 0 {
				t.Errorf("inventory request body=%q content-length=%d", body, r.ContentLength)
			}
			_, _ = io.WriteString(w, `{"repos":[{"name":"tracked","root_path":"E:/tracked","identity":"ignored-by-dashboard","count":3}],"total_count":3}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	client, err := NewClient("roborev", repositoryInventoryRunner{version: "v0.64.0"}, server.URL)
	if err != nil {
		t.Fatal(err)
	}
	value, err := client.RepositoryInventory(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if inventoryCalls != 1 || len(value.Repositories) != 1 || value.TotalCount != 3 || value.Repositories[0].RootPath != "E:/tracked" || value.Repositories[0].Identity == "" {
		t.Fatalf("calls=%d inventory=%#v", inventoryCalls, value)
	}
}

func TestRepositoryInventoryHealthAndVersionGates(t *testing.T) {
	tests := []struct {
		name          string
		cliVersion    string
		health        string
		transportErr  error
		want          error
		wantInventory bool
	}{
		{name: "unsupported cli", cliVersion: "v0.65.0", health: `{"healthy":true,"version":"v0.64.0"}`, want: ErrUnsupportedRepositoryInventory},
		{name: "unhealthy daemon", cliVersion: "v0.64.0", health: `{"healthy":false,"version":"v0.64.0"}`, want: ErrRepositoryInventoryUnhealthy},
		{name: "daemon version mismatch", cliVersion: "v0.64.0", health: `{"healthy":true,"version":"v0.65.0"}`, want: ErrUnsupportedRepositoryInventory},
		{name: "invalid health", cliVersion: "v0.64.0", health: `{"healthy":true,"version":"v0.64.0","unexpected":true}`},
		{name: "healthy exact", cliVersion: "v0.64.0", health: `{"healthy":true,"version":"v0.64.0"}`, wantInventory: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			inventoryCalls := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/api/health":
					_, _ = io.WriteString(w, test.health)
				case "/api/repos":
					inventoryCalls++
					_, _ = io.WriteString(w, `{"repos":[],"total_count":0}`)
				default:
					http.NotFound(w, r)
				}
			}))
			defer server.Close()
			client, err := NewClient("roborev", repositoryInventoryRunner{version: test.cliVersion}, server.URL)
			if err != nil {
				t.Fatal(err)
			}
			if test.transportErr != nil {
				client.HTTP = &http.Client{Transport: errorRoundTripper{err: test.transportErr}}
			}
			_, callErr := client.RepositoryInventory(context.Background())
			if test.wantInventory {
				if callErr != nil || inventoryCalls != 1 {
					t.Fatalf("err=%v inventory calls=%d", callErr, inventoryCalls)
				}
				return
			}
			if callErr == nil || test.want != nil && !errors.Is(callErr, test.want) {
				t.Fatalf("err=%v want errors.Is(...,%v)", callErr, test.want)
			}
			if test.name == "invalid health" && errors.Is(callErr, ErrUnsupportedRepositoryInventory) {
				t.Fatalf("invalid health was misclassified as unsupported: %v", callErr)
			}
			if inventoryCalls != 0 {
				t.Fatalf("inventory call count=%d", inventoryCalls)
			}
		})
	}

	offlineClient, err := NewClient("roborev", repositoryInventoryRunner{version: "v0.64.0"}, "http://127.0.0.1:7373")
	if err != nil {
		t.Fatal(err)
	}
	offlineClient.HTTP = &http.Client{Transport: errorRoundTripper{err: fmt.Errorf("connection failed: %w", syscall.Errno(10061))}}
	if _, err := offlineClient.RepositoryInventory(context.Background()); err == nil || !errors.Is(err, ErrRepositoryInventoryOffline) {
		t.Fatalf("offline err=%v", err)
	}
}

type errorRoundTripper struct{ err error }

func (r errorRoundTripper) RoundTrip(*http.Request) (*http.Response, error) { return nil, r.err }

func TestRepositoryInventoryPreservesEmptyInventory(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/health" {
			_, _ = io.WriteString(w, `{"healthy":true,"version":"v0.64.0"}`)
			return
		}
		_, _ = io.WriteString(w, `{"repos":[],"total_count":0}`)
	}))
	defer server.Close()
	client, err := NewClient("roborev", repositoryInventoryRunner{version: "v0.64.0"}, server.URL)
	if err != nil {
		t.Fatal(err)
	}
	got, err := client.RepositoryInventory(context.Background())
	if err != nil || got.Repositories == nil || len(got.Repositories) != 0 || got.TotalCount != 0 {
		t.Fatalf("inventory=%#v err=%v", got, err)
	}
}

func TestRepositoryInventoryRejectsOversizedDaemonBody(t *testing.T) {
	var inventoryCalls int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/health":
			_, _ = io.WriteString(w, `{"healthy":true,"version":"v0.64.0"}`)
		case "/api/repos":
			inventoryCalls++
			_, _ = io.WriteString(w, `{"repos":[],"total_count":0,"padding":"`+strings.Repeat("x", repoinventory.MaxResponseBytes)+`"}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	client, err := NewClient("roborev", repositoryInventoryRunner{version: "v0.64.0"}, server.URL)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.RepositoryInventory(context.Background()); err == nil {
		t.Fatal("accepted oversized /api/repos response")
	}
	if inventoryCalls != 1 {
		t.Fatalf("inventory calls=%d, want exactly one", inventoryCalls)
	}
}

var _ RepositoryInventoryService = (*Client)(nil)
