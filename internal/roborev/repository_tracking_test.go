package roborev

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	proc "github.com/froze/roborev-dashboard/internal/process"
	"github.com/froze/roborev-dashboard/internal/repoinventory"
)

type trackingRoundTripFunc func(*http.Request) (*http.Response, error)

func (f trackingRoundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return f(request)
}

type trackingVersionRunner struct {
	commands []proc.Command
	stdout   string
}

func (r *trackingVersionRunner) Run(_ context.Context, command proc.Command) (proc.Result, error) {
	r.commands = append(r.commands, command)
	return proc.Result{Stdout: r.stdout}, nil
}

func trackingClient(t *testing.T, roundTrip http.RoundTripper) *Client {
	t.Helper()
	binary := filepath.Join(t.TempDir(), "roborev.exe")
	if err := os.WriteFile(binary, []byte("fixture"), 0o700); err != nil {
		t.Fatal(err)
	}
	client, err := NewClient(binary, rebindRunner{}, "127.0.0.1:7373")
	if err != nil {
		t.Fatal(err)
	}
	client.HTTP = &http.Client{Transport: roundTrip}
	return client
}

func TestRegisterRepositoryUsesExactBoundDaemonRequest(t *testing.T) {
	root := filepath.Join(t.TempDir(), "selected")
	if err := os.Mkdir(root, 0o755); err != nil {
		t.Fatal(err)
	}
	var gotMethod, gotPath, gotQuery, gotContentType, gotAcceptEncoding string
	var gotDeadline time.Time
	var gotDeadlineOK bool
	var gotBody string
	client := trackingClient(t, trackingRoundTripFunc(func(request *http.Request) (*http.Response, error) {
		gotMethod, gotPath, gotQuery, gotContentType = request.Method, request.URL.Path, request.URL.RawQuery, request.Header.Get("Content-Type")
		gotAcceptEncoding = request.Header.Get("Accept-Encoding")
		gotDeadline, gotDeadlineOK = request.Context().Deadline()
		data, _ := io.ReadAll(request.Body)
		gotBody = string(data)
		return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"id":1,"repo_path":"private"}`)), Request: request}, nil
	}))
	binding := RepositoryTrackingBinding{Version: "v0.64.0", Generation: 1, FileID: "fixture", DaemonID: repositoryTrackingDaemonID("http://127.0.0.1:7373")}
	result, err := client.RegisterRepository(context.Background(), root, binding, func(context.Context) error { return nil })
	if err != nil || !result.Attempted || result.StatusCode != http.StatusOK {
		t.Fatalf("result=%#v err=%v", result, err)
	}
	if gotMethod != http.MethodPost || gotPath != "/api/repos/register" || gotQuery != "" || gotContentType != "application/json" {
		t.Fatalf("request method=%q path=%q query=%q content-type=%q", gotMethod, gotPath, gotQuery, gotContentType)
	}
	if gotAcceptEncoding != "identity" || !gotDeadlineOK || time.Until(gotDeadline) <= 0 || time.Until(gotDeadline) > repositoryTrackingRegisterTimeout {
		t.Fatalf("request accept-encoding=%q deadline=%v present=%v", gotAcceptEncoding, gotDeadline, gotDeadlineOK)
	}
	var body map[string]string
	if err := json.Unmarshal([]byte(gotBody), &body); err != nil || len(body) != 1 || body["repo_path"] != root {
		t.Fatalf("body=%s parsed=%#v err=%v", gotBody, body, err)
	}
}

func TestRegisterRepositoryGuardIsBeforeAttemptAndRedirectIsNotFollowed(t *testing.T) {
	root := t.TempDir()
	var calls int
	client := trackingClient(t, trackingRoundTripFunc(func(request *http.Request) (*http.Response, error) {
		calls++
		return &http.Response{StatusCode: http.StatusTemporaryRedirect, Header: http.Header{"Location": []string{"http://private.invalid/register"}}, Body: io.NopCloser(strings.NewReader("redirect")), Request: request}, nil
	}))
	binding := RepositoryTrackingBinding{Version: "v0.64.0", Generation: 1, FileID: "fixture", DaemonID: repositoryTrackingDaemonID("http://127.0.0.1:7373")}
	guardErr := context.Canceled
	if result, err := client.RegisterRepository(context.Background(), root, binding, func(context.Context) error { return guardErr }); err != guardErr || result.Attempted || calls != 0 {
		t.Fatalf("guard result=%#v err=%v calls=%d", result, err, calls)
	}
	result, err := client.RegisterRepository(context.Background(), root, binding, func(context.Context) error { return nil })
	if err == nil || !result.Attempted || result.StatusCode != http.StatusTemporaryRedirect || calls != 1 {
		t.Fatalf("redirect result=%#v err=%v calls=%d", result, err, calls)
	}
}

func TestRegisterRepositoryRejectsDaemonBindingDriftBeforeHTTPDo(t *testing.T) {
	root := t.TempDir()
	var calls int
	client := trackingClient(t, trackingRoundTripFunc(func(request *http.Request) (*http.Response, error) {
		calls++
		return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{}`)), Request: request}, nil
	}))
	binding := RepositoryTrackingBinding{Version: "v0.64.0", Generation: 1, FileID: "fixture", DaemonID: "different-daemon"}
	result, err := client.RegisterRepository(context.Background(), root, binding, func(context.Context) error { return nil })
	if err == nil || result.Attempted || calls != 0 {
		t.Fatalf("drift result=%#v err=%v calls=%d", result, err, calls)
	}
}

func TestRegisterRepositoryGuardContextExpiryDoesNotAttempt(t *testing.T) {
	root := t.TempDir()
	var calls int
	client := trackingClient(t, trackingRoundTripFunc(func(request *http.Request) (*http.Response, error) {
		calls++
		return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{}`)), Request: request}, nil
	}))
	binding := RepositoryTrackingBinding{Version: "v0.64.0", Generation: 1, FileID: "fixture", DaemonID: repositoryTrackingDaemonID("http://127.0.0.1:7373")}
	ctx, cancel := context.WithCancel(context.Background())
	result, err := client.RegisterRepository(ctx, root, binding, func(context.Context) error {
		cancel()
		return nil
	})
	if err == nil || result.Attempted || calls != 0 {
		t.Fatalf("expired guard result=%#v err=%v calls=%d", result, err, calls)
	}
}

func TestRegisterRepositoryGuardBindingDriftDoesNotAttempt(t *testing.T) {
	root := t.TempDir()
	var calls int
	client := trackingClient(t, trackingRoundTripFunc(func(request *http.Request) (*http.Response, error) {
		calls++
		return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{}`)), Request: request}, nil
	}))
	binding := RepositoryTrackingBinding{Version: "v0.64.0", Generation: 1, FileID: "fixture", DaemonID: repositoryTrackingDaemonID("http://127.0.0.1:7373")}
	guard := func(context.Context) error {
		client.bindingMu.Lock()
		client.binding.generation++
		client.bindingMu.Unlock()
		return nil
	}
	result, err := client.RegisterRepository(context.Background(), root, binding, guard)
	if err == nil || result.Attempted || calls != 0 {
		t.Fatalf("binding drift result=%#v err=%v calls=%d", result, err, calls)
	}
}

func TestRegisterRepositoryRejectsNonLoopbackBoundURL(t *testing.T) {
	// NewClient itself is the authority boundary for daemon destination; this
	// keeps tracking transport from ever accepting a browser-provided endpoint.
	if _, err := NewClient(`C:\fake\roborev.exe`, rebindRunner{}, "https://private.invalid:7373"); err == nil {
		t.Fatal("accepted non-loopback daemon URL")
	}
}

func TestRepositoryTrackingPreflightUsesFreshVersionHealthAndCompleteInventory(t *testing.T) {
	binary := filepath.Join(t.TempDir(), "roborev.exe")
	if err := os.WriteFile(binary, []byte("fixture"), 0o700); err != nil {
		t.Fatal(err)
	}
	runner := &trackingVersionRunner{stdout: `{"name":"roborev","version":"v0.64.0"}`}
	client, err := NewClient(binary, runner, "127.0.0.1:7373")
	if err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(t.TempDir(), "tracked")
	inventoryBody, err := json.Marshal(repoinventory.Inventory{Repositories: []repoinventory.Repository{{Name: "repo", RootPath: root, Count: 0}}, TotalCount: 0})
	if err != nil {
		t.Fatal(err)
	}
	var paths []string
	client.HTTP = &http.Client{Transport: trackingRoundTripFunc(func(request *http.Request) (*http.Response, error) {
		paths = append(paths, request.URL.Path)
		var body string
		switch request.URL.Path {
		case "/api/health":
			body = `{"healthy":true,"version":"v0.64.0"}`
		case "/api/repos":
			body = string(inventoryBody)
		default:
			t.Fatalf("unexpected request %s", request.URL.String())
		}
		return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body)), Request: request}, nil
	})}
	preflight, err := client.RepositoryTrackingPreflight(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !preflight.Binding.Valid() || preflight.Binding.DaemonID != repositoryTrackingDaemonID("http://127.0.0.1:7373") {
		t.Fatalf("binding=%#v", preflight.Binding)
	}
	if count, matchErr := preflight.Inventory.MatchRoot(root); matchErr != nil || count != 1 {
		t.Fatalf("inventory=%#v count=%d err=%v", preflight.Inventory, count, matchErr)
	}
	if got := strings.Join(paths, ","); got != "/api/health,/api/repos" {
		t.Fatalf("HTTP order=%s", got)
	}
	if len(runner.commands) != 1 {
		t.Fatalf("version commands=%#v", runner.commands)
	}
	command := runner.commands[0]
	if got := strings.Join(command.Args, " "); got != "version --json" || command.Timeout != 5*time.Second || command.OutputLimit != 64<<10 {
		t.Fatalf("version command=%#v", command)
	}
}

func TestRepositoryTrackingPreflightFailsClosedWithoutFollowingRedirects(t *testing.T) {
	for _, test := range []struct {
		name      string
		cli       string
		health    string
		inventory string
		redirect  bool
		wantHTTP  int
	}{
		{name: "CLI mismatch", cli: `{"name":"roborev","version":"v0.65.0"}`, wantHTTP: 0},
		{name: "unhealthy daemon", cli: `{"name":"roborev","version":"v0.64.0"}`, health: `{"healthy":false,"version":"v0.64.0"}`, wantHTTP: 1},
		{name: "daemon version mismatch", cli: `{"name":"roborev","version":"v0.64.0"}`, health: `{"healthy":true,"version":"v0.63.0"}`, wantHTTP: 1},
		{name: "malformed inventory", cli: `{"name":"roborev","version":"v0.64.0"}`, health: `{"healthy":true,"version":"v0.64.0"}`, inventory: `{bad`, wantHTTP: 2},
		{name: "inventory redirect", cli: `{"name":"roborev","version":"v0.64.0"}`, health: `{"healthy":true,"version":"v0.64.0"}`, redirect: true, wantHTTP: 2},
	} {
		t.Run(test.name, func(t *testing.T) {
			binary := filepath.Join(t.TempDir(), "roborev.exe")
			if err := os.WriteFile(binary, []byte("fixture"), 0o700); err != nil {
				t.Fatal(err)
			}
			client, err := NewClient(binary, &trackingVersionRunner{stdout: test.cli}, "127.0.0.1:7373")
			if err != nil {
				t.Fatal(err)
			}
			calls := 0
			client.HTTP = &http.Client{Transport: trackingRoundTripFunc(func(request *http.Request) (*http.Response, error) {
				calls++
				if request.URL.Path == "/api/health" {
					return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(test.health)), Request: request}, nil
				}
				if request.URL.Path == "/api/repos" && test.redirect {
					return &http.Response{StatusCode: http.StatusTemporaryRedirect, Header: http.Header{"Location": []string{"http://127.0.0.2:7373/private"}}, Body: io.NopCloser(strings.NewReader("PRIVATE_DAEMON_ERROR")), Request: request}, nil
				}
				if request.URL.Host != "127.0.0.1:7373" || request.URL.Path != "/api/repos" {
					t.Fatalf("redirect followed to %s", request.URL.String())
				}
				return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(test.inventory)), Request: request}, nil
			})}
			if _, err := client.RepositoryTrackingPreflight(context.Background()); err == nil {
				t.Fatal("unsafe preflight unexpectedly succeeded")
			}
			if calls != test.wantHTTP {
				t.Fatalf("HTTP calls=%d want=%d", calls, test.wantHTTP)
			}
		})
	}
}

func TestRegisterRepositoryBoundsResponseAndNeverRetries(t *testing.T) {
	root := t.TempDir()
	calls := 0
	client := trackingClient(t, trackingRoundTripFunc(func(request *http.Request) (*http.Response, error) {
		calls++
		return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(strings.Repeat("x", repositoryTrackingResponseLimit+1))), Request: request}, nil
	}))
	binding := RepositoryTrackingBinding{Version: "v0.64.0", Generation: 1, FileID: "fixture", DaemonID: repositoryTrackingDaemonID("http://127.0.0.1:7373")}
	result, err := client.RegisterRepository(context.Background(), root, binding, func(context.Context) error { return nil })
	if err == nil || !result.Attempted || result.StatusCode != http.StatusOK || calls != 1 {
		t.Fatalf("result=%#v err=%v calls=%d", result, err, calls)
	}
}

func TestRegisterRepositoryNeverRetriesHTTPFailures(t *testing.T) {
	for _, test := range []struct {
		name       string
		statusCode int
		transport  error
	}{
		{name: "HTTP 500", statusCode: http.StatusInternalServerError},
		{name: "connection reset", transport: errors.New("connection reset by peer")},
	} {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			calls := 0
			client := trackingClient(t, trackingRoundTripFunc(func(request *http.Request) (*http.Response, error) {
				calls++
				if test.transport != nil {
					return nil, test.transport
				}
				return &http.Response{StatusCode: test.statusCode, Header: make(http.Header), Body: io.NopCloser(strings.NewReader("PRIVATE_DAEMON_ERROR")), Request: request}, nil
			}))
			binding := RepositoryTrackingBinding{Version: "v0.64.0", Generation: 1, FileID: "fixture", DaemonID: repositoryTrackingDaemonID("http://127.0.0.1:7373")}
			result, err := client.RegisterRepository(context.Background(), root, binding, func(context.Context) error { return nil })
			if err == nil || !result.Attempted || result.StatusCode != test.statusCode || calls != 1 || strings.Contains(err.Error(), "PRIVATE_DAEMON_ERROR") {
				t.Fatalf("result=%#v err=%v calls=%d", result, err, calls)
			}
		})
	}
}
