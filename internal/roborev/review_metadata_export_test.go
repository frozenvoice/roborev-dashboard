package roborev

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	proc "github.com/froze/roborev-dashboard/internal/process"
)

type reviewExportRunner struct {
	mu       sync.Mutex
	commands []proc.Command
	version  string
}

func (r *reviewExportRunner) Run(_ context.Context, command proc.Command) (proc.Result, error) {
	r.mu.Lock()
	r.commands = append(r.commands, command)
	r.mu.Unlock()
	if len(command.Args) == 2 && command.Args[0] == "version" && command.Args[1] == "--json" {
		return proc.Result{Stdout: `{"name":"roborev","version":"` + r.version + `"}`}, nil
	}
	return proc.Result{}, errors.New("unexpected subprocess")
}

type bodyTrackingReader struct {
	reader io.Reader
	closed bool
	reads  int
}

func (r *bodyTrackingReader) Read(p []byte) (int, error) {
	r.reads++
	return r.reader.Read(p)
}

func (r *bodyTrackingReader) Close() error {
	r.closed = true
	return nil
}

func reviewExportClient(t *testing.T, handler http.Handler) (*Client, *reviewExportRunner, string) {
	t.Helper()
	root := filepath.Join(t.TempDir(), "selected repository")
	runner := &reviewExportRunner{version: "v0.64.0"}
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	client, err := NewClient("roborev", runner, server.URL)
	if err != nil {
		t.Fatal(err)
	}
	return client, runner, root
}

func validReviewExportDocument(repo string, truncated bool, count int) []byte {
	reviews := make([]map[string]any, 0, count)
	for index := 0; index < count; index++ {
		completedAt := time.Date(2026, time.August, 25, 0, 0, index, 0, time.UTC).Format(time.RFC3339)
		reviews = append(reviews, map[string]any{
			"review_id":    fmt.Sprintf("review-%04d", index),
			"status":       "done",
			"verdict":      "pass",
			"created_at":   completedAt,
			"completed_at": completedAt,
			"duration_ms":  nil,
			"project":      "selected repository",
			"repo":         repo,
			"branch":       nil,
			"commit_sha":   nil,
			"pr_number":    nil,
			"pr_url":       nil,
			"agent":        "codex",
			"model":        nil,
			"cost":         map[string]any{"tokens_in": nil, "tokens_out": nil, "usd": nil},
			"content":      nil,
			"subagents":    []any{},
		})
	}
	next := any(nil)
	if count > 0 {
		next = "cursor"
	}
	document := map[string]any{
		"schema_version": 1,
		"tool":           "roborev",
		"tool_version":   "v0.64.0",
		"generated_at":   "2026-08-25T00:00:00Z",
		"database_id":    "database",
		"profile":        "metadata",
		"window":         map[string]any{"field": "completed_at", "since": nil, "until": nil},
		"truncated":      truncated,
		"next_cursor":    next,
		"reviews":        reviews,
	}
	data, _ := json.Marshal(document)
	return data
}

func reviewExportInventoryDocument(root string, rows ...map[string]any) []byte {
	document := map[string]any{"repos": rows, "total_count": 0}
	var total int64
	for _, row := range rows {
		if count, ok := row["count"].(int); ok {
			total += int64(count)
		}
	}
	document["total_count"] = total
	data, _ := json.Marshal(document)
	return data
}

func TestReviewMetadataExportUsesExactIdentityAndSemanticQuery(t *testing.T) {
	root := filepath.Join(t.TempDir(), "selected repository")
	var gotQuery url.Values
	var exportCalls int
	client, runner, _ := reviewExportClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		switch r.URL.Path {
		case "/api/health":
			_, _ = io.WriteString(w, `{"healthy":true,"version":"v0.64.0"}`)
		case "/api/repos":
			_, _ = w.Write(reviewExportInventoryDocument(root, map[string]any{"name": "selected repository", "root_path": root, "identity": " exact-identity ", "count": 0}))
		case "/api/export/reviews":
			exportCalls++
			gotQuery = r.URL.Query()
			_, _ = w.Write(validReviewExportDocument(" exact-identity ", false, 1))
		default:
			http.NotFound(w, r)
		}
	}))
	result, err := client.ReviewMetadataExport(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	if result.Truncated || !bytes.Equal(result.Raw, validReviewExportDocument(" exact-identity ", false, 1)) {
		t.Fatalf("unexpected result: truncated=%v raw=%s", result.Truncated, result.Raw)
	}
	if exportCalls != 1 || gotQuery.Get("repo") != " exact-identity " || gotQuery.Get("format") != "json" || gotQuery.Get("profile") != "metadata" || gotQuery.Get("limit") != "500" || len(gotQuery) != 4 {
		t.Fatalf("query=%v calls=%d", gotQuery, exportCalls)
	}
	for _, command := range runner.commands {
		if len(command.Args) > 0 && command.Args[0] == "export" {
			t.Fatalf("export CLI was invoked: %#v", command)
		}
	}
}

func TestReviewMetadataExportUsesUniqueNameFallbackAndRejectsDuplicate(t *testing.T) {
	root := filepath.Join(t.TempDir(), "selected")
	for _, test := range []struct {
		name       string
		inventory  []byte
		wantStatus bool
		wantRepo   string
	}{
		{name: "unique", inventory: reviewExportInventoryDocument(root, map[string]any{"name": "selected", "root_path": root, "identity": "", "count": 0}), wantStatus: true, wantRepo: "selected"},
		{name: "duplicate", inventory: reviewExportInventoryDocument(root, map[string]any{"name": "selected", "root_path": root, "identity": "", "count": 0}, map[string]any{"name": "selected", "root_path": "other", "identity": "", "count": 0}), wantStatus: false},
	} {
		t.Run(test.name, func(t *testing.T) {
			var exportCalls int
			client, _, _ := reviewExportClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				switch r.URL.Path {
				case "/api/health":
					_, _ = io.WriteString(w, `{"healthy":true,"version":"v0.64.0"}`)
				case "/api/repos":
					_, _ = w.Write(test.inventory)
				case "/api/export/reviews":
					exportCalls++
					_, _ = w.Write(validReviewExportDocument(test.wantRepo, false, 0))
				}
			}))
			result, err := client.ReviewMetadataExport(context.Background(), root)
			if test.wantStatus && err != nil {
				t.Fatal(err)
			}
			if !test.wantStatus && (!errors.Is(err, ErrReviewMetadataExportConflict) || exportCalls != 0) {
				t.Fatalf("err=%v exportCalls=%d", err, exportCalls)
			}
			if test.wantStatus && result.Raw == nil {
				t.Fatal("missing export")
			}
		})
	}
}

func TestReviewMetadataExportDoesNotFollowRedirectOrReadNon200Body(t *testing.T) {
	root := filepath.Join(t.TempDir(), "selected")
	redirectBody := &bodyTrackingReader{reader: strings.NewReader(`{"secret":"redirect"}`)}
	non200Body := &bodyTrackingReader{reader: strings.NewReader(`{"secret":"daemon"}`)}
	var destinationCalls int
	client, _, _ := reviewExportClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/health":
			_, _ = io.WriteString(w, `{"healthy":true,"version":"v0.64.0"}`)
		case "/api/repos":
			_, _ = w.Write(reviewExportInventoryDocument(root, map[string]any{"name": "selected", "root_path": root, "identity": "id", "count": 0}))
		case "/api/export/reviews":
			http.Redirect(w, r, "/redirect-target", http.StatusFound)
		case "/redirect-target":
			destinationCalls++
		}
	}))
	client.HTTP.Transport = exportRoundTripFunc(func(request *http.Request) (*http.Response, error) {
		if request.URL.Path == "/api/export/reviews" {
			return &http.Response{StatusCode: http.StatusFound, Header: http.Header{"Location": []string{"/redirect-target"}}, Body: redirectBody, Request: request}, nil
		}
		return http.DefaultTransport.RoundTrip(request)
	})
	_, err := client.ReviewMetadataExport(context.Background(), root)
	if !errors.Is(err, ErrMalformedReviewMetadataExport) || destinationCalls != 0 || redirectBody.reads != 0 || !redirectBody.closed {
		t.Fatalf("redirect err=%v destination=%d reads=%d closed=%v", err, destinationCalls, redirectBody.reads, redirectBody.closed)
	}
	client.HTTP.Transport = exportRoundTripFunc(func(request *http.Request) (*http.Response, error) {
		if request.URL.Path == "/api/export/reviews" {
			return &http.Response{StatusCode: http.StatusBadGateway, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: non200Body, Request: request}, nil
		}
		return http.DefaultTransport.RoundTrip(request)
	})
	_, err = client.ReviewMetadataExport(context.Background(), root)
	if !errors.Is(err, ErrMalformedReviewMetadataExport) || non200Body.reads != 0 || !non200Body.closed {
		t.Fatalf("non200 err=%v reads=%d closed=%v", err, non200Body.reads, non200Body.closed)
	}
}

func TestReviewMetadataExportDoesNotFollowRedirectOnHealthInventoryOrExport(t *testing.T) {
	for _, stage := range []string{"health", "inventory", "export"} {
		t.Run(stage, func(t *testing.T) {
			root := filepath.Join(t.TempDir(), "selected")
			body := &bodyTrackingReader{reader: strings.NewReader(`{"secret":"redirect"}`)}
			destinationCalls := 0
			client, _, _ := reviewExportClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				switch r.URL.Path {
				case "/api/health":
					_, _ = io.WriteString(w, `{"healthy":true,"version":"v0.64.0"}`)
				case "/api/repos":
					_, _ = w.Write(reviewExportInventoryDocument(root, map[string]any{"name": "selected", "root_path": root, "identity": "id", "count": 0}))
				case "/api/export/reviews":
					_, _ = w.Write(validReviewExportDocument("id", false, 0))
				}
			}))
			client.HTTP.Transport = exportRoundTripFunc(func(request *http.Request) (*http.Response, error) {
				redirectPath := map[string]string{"health": "/api/health", "inventory": "/api/repos", "export": "/api/export/reviews"}[stage]
				if request.URL.Path == redirectPath {
					return &http.Response{StatusCode: http.StatusFound, Header: http.Header{"Content-Type": []string{"application/json"}, "Location": []string{"/redirect-target"}}, Body: body, Request: request}, nil
				}
				if request.URL.Path == "/redirect-target" {
					destinationCalls++
				}
				return http.DefaultTransport.RoundTrip(request)
			})
			_, err := client.ReviewMetadataExport(context.Background(), root)
			if !errors.Is(err, ErrMalformedReviewMetadataExport) || destinationCalls != 0 || body.reads != 0 || !body.closed {
				t.Fatalf("stage=%s err=%v destination=%d reads=%d closed=%v", stage, err, destinationCalls, body.reads, body.closed)
			}
		})
	}
}

func TestReviewMetadataExportRejectsNonUTF8Charset(t *testing.T) {
	for _, value := range []string{"application/json; charset=latin1", "application/json; charset=iso-8859-1", "text/json; charset=utf-8"} {
		if reviewMetadataJSONContentType(value) {
			t.Fatalf("accepted invalid export media type %q", value)
		}
	}
	for _, value := range []string{"application/json", "application/json; charset=utf-8", "Application/JSON; Charset=UTF-8"} {
		if !reviewMetadataJSONContentType(value) {
			t.Fatalf("rejected valid export media type %q", value)
		}
	}
}

func TestReviewMetadataExportRejectsInvalidResponseMediaTypeBeforeBodyRead(t *testing.T) {
	root := filepath.Join(t.TempDir(), "selected")
	body := &bodyTrackingReader{reader: strings.NewReader(`{"secret":"wrong charset"}`)}
	client, _, _ := reviewExportClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/health":
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"healthy":true,"version":"v0.64.0"}`)
		case "/api/repos":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write(reviewExportInventoryDocument(root, map[string]any{"name": "selected", "root_path": root, "identity": "id", "count": 0}))
		case "/api/export/reviews":
			_, _ = w.Write(validReviewExportDocument("id", false, 0))
		}
	}))
	client.HTTP.Transport = exportRoundTripFunc(func(request *http.Request) (*http.Response, error) {
		if request.URL.Path == "/api/export/reviews" {
			return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{"application/json; charset=latin1"}}, Body: body, Request: request}, nil
		}
		return http.DefaultTransport.RoundTrip(request)
	})
	_, err := client.ReviewMetadataExport(context.Background(), root)
	if !errors.Is(err, ErrMalformedReviewMetadataExport) || body.reads != 0 {
		t.Fatalf("err=%v body reads=%d", err, body.reads)
	}
}

func TestReviewMetadataExportHealthRejectsInvalidCharsetBeforeInventory(t *testing.T) {
	root := filepath.Join(t.TempDir(), "selected")
	var repos int
	client, _, _ := reviewExportClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/health":
			w.Header().Set("Content-Type", "application/json; charset=latin1")
			_, _ = io.WriteString(w, `{"healthy":true,"version":"v0.64.0"}`)
		case "/api/repos":
			repos++
		}
	}))
	_, err := client.ReviewMetadataExport(context.Background(), root)
	if !errors.Is(err, ErrMalformedReviewMetadataExport) || repos != 0 {
		t.Fatalf("err=%v repos=%d", err, repos)
	}
}

func TestReviewMetadataExportHealthRejectsDuplicateOrMissingVersionBeforeInventory(t *testing.T) {
	for _, healthBody := range []string{
		`{"healthy":true,"healthy":false,"version":"v0.64.0"}`,
		`{"healthy":true,"version":"v0.64.0","version":"v0.64.0"}`,
		`{"healthy":true}`,
		`{"healthy":true,"version":""}`,
	} {
		t.Run(healthBody, func(t *testing.T) {
			root := filepath.Join(t.TempDir(), "selected")
			var repos, exports int
			client, _, _ := reviewExportClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				switch r.URL.Path {
				case "/api/health":
					_, _ = io.WriteString(w, healthBody)
				case "/api/repos":
					repos++
					_, _ = w.Write(reviewExportInventoryDocument(root, map[string]any{"name": "selected", "root_path": root, "identity": "id", "count": 0}))
				case "/api/export/reviews":
					exports++
				}
			}))
			_, err := client.ReviewMetadataExport(context.Background(), root)
			if !errors.Is(err, ErrMalformedReviewMetadataExport) || repos != 0 || exports != 0 {
				t.Fatalf("health=%s err=%v repos=%d exports=%d", healthBody, err, repos, exports)
			}
		})
	}
}

func TestReviewMetadataExportTransportRejectsOversizedBody(t *testing.T) {
	root := filepath.Join(t.TempDir(), "selected")
	oversized := bytes.Repeat([]byte("x"), int(reviewMetadataExportResponseLimit)+1)
	client, _, _ := reviewExportClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/health":
			_, _ = io.WriteString(w, `{"healthy":true,"version":"v0.64.0"}`)
		case "/api/repos":
			_, _ = w.Write(reviewExportInventoryDocument(root, map[string]any{"name": "selected", "root_path": root, "identity": "id", "count": 0}))
		}
	}))
	client.HTTP.Transport = exportRoundTripFunc(func(request *http.Request) (*http.Response, error) {
		if request.URL.Path == "/api/export/reviews" {
			return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(bytes.NewReader(oversized)), Request: request}, nil
		}
		return http.DefaultTransport.RoundTrip(request)
	})
	_, err := client.ReviewMetadataExport(context.Background(), root)
	if !errors.Is(err, ErrMalformedReviewMetadataExport) {
		t.Fatalf("oversized err=%v", err)
	}
}

type exportRoundTripFunc func(*http.Request) (*http.Response, error)

func (f exportRoundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return f(request)
}

func TestReviewMetadataExportValidatorRejectsMalformedDocuments(t *testing.T) {
	valid := validReviewExportDocument("id", false, 1)
	tests := []struct {
		name string
		body string
	}{
		{name: "duplicate", body: strings.Replace(string(valid), `"tool":"roborev"`, `"tool":"roborev","tool":"roborev"`, 1)},
		{name: "trailing", body: string(valid) + `{}`},
		{name: "content", body: strings.Replace(string(valid), `"content":null`, `"content":"secret"`, 1)},
		{name: "wrong repo", body: strings.Replace(string(valid), `"repo":"id"`, `"repo":"other"`, 1)},
		{name: "unknown field", body: strings.Replace(string(valid), `"tool":"roborev"`, `"tool":"roborev","unknown":true`, 1)},
		{name: "invalid utf8", body: string(append([]byte(`{"schema_version":1,"tool":"roborev","tool_version":"v0.64.0","generated_at":"2026-08-25T00:00:00Z","database_id":"database","profile":"metadata","window":{"field":"completed_at","since":null,"until":null},"truncated":false,"next_cursor":null,"reviews":[]}`), 0xff))},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, _, err := validateReviewMetadataExport([]byte(test.body), "id"); !errors.Is(err, ErrMalformedReviewMetadataExport) {
				t.Fatalf("err=%v", err)
			}
		})
	}
}

func TestReviewMetadataExportRejectsOversizedBody(t *testing.T) {
	oversized := bytes.Repeat([]byte("x"), int(reviewMetadataExportResponseLimit)+1)
	if _, _, err := validateReviewMetadataExport(oversized, "id"); !errors.Is(err, ErrMalformedReviewMetadataExport) {
		t.Fatalf("err=%v", err)
	}
}

func TestReviewMetadataExportValidatorEnforcesRowAndTruncationBounds(t *testing.T) {
	if _, _, err := validateReviewMetadataExport(validReviewExportDocument("id", false, 501), "id"); !errors.Is(err, ErrMalformedReviewMetadataExport) {
		t.Fatalf("accepted >500 rows: %v", err)
	}
	if _, _, err := validateReviewMetadataExport(validReviewExportDocument("id", true, 499), "id"); !errors.Is(err, ErrMalformedReviewMetadataExport) {
		t.Fatalf("accepted truncated underfilled page: %v", err)
	}
	if _, truncated, err := validateReviewMetadataExport(validReviewExportDocument("id", true, 500), "id"); err != nil || !truncated {
		t.Fatalf("truncated page err=%v truncated=%v", err, truncated)
	}
}

func TestReviewMetadataExportValidatorAcceptsSubagentMetadataAndRejectsContent(t *testing.T) {
	valid := validReviewExportDocument("id", false, 1)
	subagent := `{"review_id":"subagent-1","name":"security","agent":"codex","model":null,"review_type":null,"verdict":"pass","completed_at":"2026-08-25T00:00:02Z","duration_ms":null,"cost":{"tokens_in":null,"tokens_out":null,"usd":null},"content":null}`
	withSubagent := []byte(strings.Replace(string(valid), `"subagents":[]`, `"subagents":[`+subagent+`]`, 1))
	if _, _, err := validateReviewMetadataExport(withSubagent, "id"); err != nil {
		t.Fatalf("valid subagent metadata rejected: %v", err)
	}
	contentSubagent := strings.Replace(subagent, `"content":null`, `"content":"secret"`, 1)
	withContent := []byte(strings.Replace(string(withSubagent), subagent, contentSubagent, 1))
	if _, _, err := validateReviewMetadataExport(withContent, "id"); !errors.Is(err, ErrMalformedReviewMetadataExport) {
		t.Fatalf("non-null subagent content accepted: %v", err)
	}
}

func TestReviewMetadataExportRejectsRootMismatchAndDuplicateRootBeforeExport(t *testing.T) {
	for _, rows := range [][]map[string]any{
		{{"name": "other", "root_path": "other", "identity": "id", "count": 0}},
		{{"name": "selected", "root_path": "same", "identity": "id-a", "count": 0}, {"name": "selected", "root_path": "same", "identity": "id-b", "count": 0}},
	} {
		root := filepath.Join(t.TempDir(), "selected")
		var exports int
		client, _, _ := reviewExportClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			switch r.URL.Path {
			case "/api/health":
				_, _ = io.WriteString(w, `{"healthy":true,"version":"v0.64.0"}`)
			case "/api/repos":
				for _, row := range rows {
					if row["root_path"] == "same" {
						row["root_path"] = root
					}
				}
				_, _ = w.Write(reviewExportInventoryDocument(root, rows...))
			case "/api/export/reviews":
				exports++
			}
		}))
		_, err := client.ReviewMetadataExport(context.Background(), root)
		if !errors.Is(err, ErrReviewMetadataExportConflict) || exports != 0 {
			t.Fatalf("err=%v exports=%d", err, exports)
		}
	}
}

func TestReviewMetadataExportRejectsGloballyCollidingEffectiveIdentifiers(t *testing.T) {
	for _, test := range []struct {
		name string
		rows func(string) []map[string]any
	}{
		{
			name: "duplicate nonblank identity",
			rows: func(root string) []map[string]any {
				return []map[string]any{
					{"name": "selected", "root_path": root, "identity": "shared-id", "count": 0},
					{"name": "other", "root_path": filepath.Join(filepath.Dir(root), "other"), "identity": "shared-id", "count": 0},
				}
			},
		},
		{
			name: "empty identity collides with nonblank identity",
			rows: func(root string) []map[string]any {
				return []map[string]any{
					{"name": "shared-name", "root_path": root, "identity": "", "count": 0},
					{"name": "other-name", "root_path": filepath.Join(filepath.Dir(root), "other"), "identity": "shared-name", "count": 0},
				}
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			root := filepath.Join(t.TempDir(), "selected")
			rows := test.rows(root)
			exports := 0
			client, _, _ := reviewExportClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				switch r.URL.Path {
				case "/api/health":
					_, _ = io.WriteString(w, `{"healthy":true,"version":"v0.64.0"}`)
				case "/api/repos":
					_, _ = w.Write(reviewExportInventoryDocument(root, rows...))
				case "/api/export/reviews":
					exports++
				}
			}))
			_, err := client.ReviewMetadataExport(context.Background(), root)
			if !errors.Is(err, ErrReviewMetadataExportConflict) || exports != 0 {
				t.Fatalf("err=%v exports=%d", err, exports)
			}
		})
	}
}

func TestReviewMetadataExportPreflightFailuresDoNotReadInventoryOrExport(t *testing.T) {
	for _, test := range []struct {
		name          string
		version       string
		healthVersion string
		healthy       bool
		want          error
	}{
		{name: "unsupported cli", version: "v0.63.0", healthVersion: "v0.64.0", healthy: true, want: ErrUnsupportedReviewMetadataExport},
		{name: "daemon mismatch", version: "v0.64.0", healthVersion: "v0.63.0", healthy: true, want: ErrUnsupportedReviewMetadataExport},
		{name: "unhealthy", version: "v0.64.0", healthVersion: "v0.64.0", healthy: false, want: ErrReviewMetadataExportUnhealthy},
	} {
		t.Run(test.name, func(t *testing.T) {
			root := filepath.Join(t.TempDir(), "selected")
			var repos, exports int
			client, runner, _ := reviewExportClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				switch r.URL.Path {
				case "/api/health":
					_, _ = io.WriteString(w, fmt.Sprintf(`{"healthy":%t,"version":%q}`, test.healthy, test.healthVersion))
				case "/api/repos":
					repos++
					_, _ = w.Write(reviewExportInventoryDocument(root, map[string]any{"name": "selected", "root_path": root, "identity": "id", "count": 0}))
				case "/api/export/reviews":
					exports++
				}
			}))
			runner.version = test.version
			_, err := client.ReviewMetadataExport(context.Background(), root)
			if !errors.Is(err, test.want) || repos != 0 || exports != 0 {
				t.Fatalf("err=%v repos=%d exports=%d", err, repos, exports)
			}
		})
	}
}

func TestReviewMetadataExportOfflineDoesNotStartLifecycleOrExport(t *testing.T) {
	root := filepath.Join(t.TempDir(), "selected")
	runner := &reviewExportRunner{version: "v0.64.0"}
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	client, err := NewClient("roborev", runner, server.URL)
	if err != nil {
		t.Fatal(err)
	}
	server.Close()
	_, err = client.ReviewMetadataExport(context.Background(), root)
	if !errors.Is(err, ErrReviewMetadataExportOffline) {
		t.Fatalf("offline err=%v", err)
	}
	for _, command := range runner.commands {
		if len(command.Args) == 2 && command.Args[0] == "daemon" {
			t.Fatalf("lifecycle command invoked: %#v", command)
		}
	}
}

func TestReviewMetadataExportDetectsBindingGenerationDrift(t *testing.T) {
	root := filepath.Join(t.TempDir(), "selected")
	var client *Client
	client, _, _ = reviewExportClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/health":
			_, _ = io.WriteString(w, `{"healthy":true,"version":"v0.64.0"}`)
		case "/api/repos":
			_, _ = w.Write(reviewExportInventoryDocument(root, map[string]any{"name": "selected", "root_path": root, "identity": "id", "count": 0}))
		case "/api/export/reviews":
			client.bindingMu.Lock()
			client.binding.generation++
			client.bindingMu.Unlock()
			_, _ = w.Write(validReviewExportDocument("id", false, 0))
		}
	}))
	_, err := client.ReviewMetadataExport(context.Background(), root)
	if !errors.Is(err, ErrBindingChanged) {
		t.Fatalf("generation drift err=%v", err)
	}
}
