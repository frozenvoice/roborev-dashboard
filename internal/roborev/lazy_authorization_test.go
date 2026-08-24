package roborev

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"testing"
)

type lazyRoundTripFunc func(*http.Request) (*http.Response, error)

func (f lazyRoundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return f(request)
}

type trackingReadCloser struct {
	data []byte
	read int
}

func (b *trackingReadCloser) Read(dst []byte) (int, error) {
	if b.read >= len(b.data) {
		return 0, io.EOF
	}
	n := copy(dst, b.data[b.read:])
	b.read += n
	return n, nil
}

func (b *trackingReadCloser) Close() error { return nil }

type lazyAuthorizationFixture struct {
	mu                    sync.Mutex
	targetCalls           int
	fallbackCalls         int
	reviewCalls           int
	reviewNotFoundCalls   int
	logCalls              int
	queries               []url.Values
	acceptEncoding        []string
	targetBody            *trackingReadCloser
	witnessBody           *trackingReadCloser
	errorBody             *trackingReadCloser
	foreignRepoID         int64
	targetEmpty           bool
	targetHTTPStatus      int
	targetStatusNameValue string
	targetJobType         string
	targetPanelRole       string
	fallback              bool
	logStatus             string
	logBody               string
	ownerEncoding         string
	driftAfterWitness     bool
	mutateAfterContent    bool
	witnessStatuses       []int
	witnessBodies         []string
	witnessTransportErrs  []error
	witnessIndex          int
	witnessErrorBody      *trackingReadCloser
	client                *Client
}

func (f *lazyAuthorizationFixture) response(status int, contentType string, body io.ReadCloser) *http.Response {
	headers := http.Header{"Content-Type": []string{contentType}}
	if f.ownerEncoding != "" {
		headers.Set("Content-Encoding", f.ownerEncoding)
	}
	return &http.Response{StatusCode: status, Header: headers, Body: body}
}

func (f *lazyAuthorizationFixture) RoundTrip(request *http.Request) (*http.Response, error) {
	query := request.URL.Query()
	f.mu.Lock()
	f.queries = append(f.queries, query)
	f.acceptEncoding = append(f.acceptEncoding, request.Header.Get("Accept-Encoding"))
	f.mu.Unlock()
	switch request.URL.Path {
	case "/api/health":
		return f.response(http.StatusOK, "application/json", io.NopCloser(strings.NewReader(`{"healthy":true,"version":"v0.64.0"}`))), nil
	case "/api/jobs":
		if query.Get("id") != "" {
			f.mu.Lock()
			f.targetCalls++
			call := f.targetCalls
			if call > 1 {
				f.fallbackCalls++
			}
			f.mu.Unlock()
			if f.targetEmpty {
				return f.response(http.StatusOK, "application/json", io.NopCloser(strings.NewReader(`{"jobs":[]}`))), nil
			}
			if f.targetHTTPStatus != 0 && f.targetHTTPStatus != http.StatusOK {
				tracker := &trackingReadCloser{data: []byte("VERY_SENSITIVE_NON_2XX_BODY")}
				f.mu.Lock()
				f.errorBody = tracker
				f.mu.Unlock()
				return f.response(f.targetHTTPStatus, "application/json", tracker), nil
			}
			if f.fallback && call > 1 {
				status := f.targetStatusName()
				prompt := fmt.Sprintf("authorized %s prompt", status)
				if f.mutateAfterContent {
					f.foreignRepoID = 99
				}
				repoID := int64(7)
				if f.foreignRepoID != 0 {
					repoID = f.foreignRepoID
				}
				body := fmt.Sprintf(`{"jobs":[%s]}`, f.reviewJobJSON(42, repoID, status, "review", "", prompt))
				return f.response(http.StatusOK, "application/json", io.NopCloser(strings.NewReader(body))), nil
			}
			repoID := int64(7)
			if f.foreignRepoID != 0 {
				repoID = f.foreignRepoID
			}
			status := f.targetStatusName()
			jobType := f.targetJobType
			if jobType == "" {
				jobType = "review"
			}
			body := fmt.Sprintf(`{"jobs":[%s]}`, f.reviewJobJSON(42, repoID, status, jobType, f.targetPanelRole, "VERY_SENSITIVE_FOREIGN_PROMPT"))
			tracker := &trackingReadCloser{data: []byte(body)}
			f.mu.Lock()
			f.targetBody = tracker
			f.mu.Unlock()
			return f.response(http.StatusOK, "application/json", tracker), nil
		}
		f.mu.Lock()
		witnessIndex := f.witnessIndex
		f.witnessIndex++
		var witnessTransportErr error
		if witnessIndex < len(f.witnessTransportErrs) {
			witnessTransportErr = f.witnessTransportErrs[witnessIndex]
		}
		f.mu.Unlock()
		if witnessTransportErr != nil {
			return nil, witnessTransportErr
		}
		status := http.StatusOK
		if witnessIndex < len(f.witnessStatuses) && f.witnessStatuses[witnessIndex] != 0 {
			status = f.witnessStatuses[witnessIndex]
		}
		body := fmt.Sprintf(`{"jobs":[%s]}`, f.reviewJobJSON(99, 7, "done", "synthesis", "synthesis", "witness prompt"))
		if witnessIndex < len(f.witnessBodies) {
			body = f.witnessBodies[witnessIndex]
		}
		tracker := &trackingReadCloser{data: []byte(body)}
		f.mu.Lock()
		f.witnessBody = tracker
		if status != http.StatusOK {
			f.witnessErrorBody = tracker
		}
		f.mu.Unlock()
		if f.driftAfterWitness && f.client != nil {
			f.client.bindingMu.Lock()
			f.client.binding.generation++
			f.client.bindingMu.Unlock()
		}
		return f.response(status, "application/json", tracker), nil
	case "/api/review":
		f.mu.Lock()
		f.reviewCalls++
		f.mu.Unlock()
		if f.fallback {
			f.mu.Lock()
			f.reviewNotFoundCalls++
			f.mu.Unlock()
			return f.response(http.StatusNotFound, "application/json", io.NopCloser(strings.NewReader("foreign review body"))), nil
		}
		if f.mutateAfterContent {
			f.foreignRepoID = 99
		}
		return f.response(http.StatusOK, "application/json", io.NopCloser(strings.NewReader(`{"job_id":42,"prompt":"authorized prompt"}`))), nil
	case "/api/job/log":
		f.mu.Lock()
		f.logCalls++
		f.mu.Unlock()
		body := f.logBody
		status := f.logStatus
		if status == "" {
			status = "running"
		}
		if f.mutateAfterContent {
			f.foreignRepoID = 99
		}
		headers := http.Header{"Content-Type": []string{"application/x-ndjson"}, "X-Job-Status": []string{status}, "X-Log-Offset": []string{fmt.Sprint(len(body))}}
		return &http.Response{StatusCode: http.StatusOK, Header: headers, Body: io.NopCloser(strings.NewReader(body))}, nil
	default:
		return f.response(http.StatusNotFound, "application/json", io.NopCloser(strings.NewReader("not found"))), nil
	}
}

func (f *lazyAuthorizationFixture) targetStatusName() string {
	if f.targetStatusNameValue != "" {
		return f.targetStatusNameValue
	}
	return "running"
}

func (f *lazyAuthorizationFixture) reviewJobJSON(id, repoID int64, status, jobType, panelRole, prompt string) string {
	body := fmt.Sprintf(`{"id":%d,"repo_id":%d,"git_ref":"abcdef1234567890","agent":"codex","job_type":%q,"status":%q,"enqueued_at":"2026-08-24T00:00:00Z","prompt":%q,"retry_count":0,"agentic":false,"prompt_prebuilt":false`, id, repoID, jobType, status, prompt)
	if panelRole != "" {
		body += fmt.Sprintf(`,"panel_run_uuid":"00000000-0000-0000-0000-000000000042","panel_role":%q,"panel_member_name":"reviewer","panel_member_index":0`, panelRole)
	}
	return body + `}`
}

func newLazyAuthorizationClient(t *testing.T, fixture *lazyAuthorizationFixture) *Client {
	t.Helper()
	runner := &lazyContentRunner{}
	client, err := NewClient("roborev", runner, "127.0.0.1:7373")
	if err != nil {
		t.Fatal(err)
	}
	client.HTTP = &http.Client{Transport: lazyRoundTripFunc(fixture.RoundTrip)}
	fixture.client = client
	return client
}

func TestAuthorizedCompletedPromptStopsAtRepoPrefixBeforeStoredPrompt(t *testing.T) {
	fixture := &lazyAuthorizationFixture{targetStatusNameValue: "done"}
	client := newLazyAuthorizationClient(t, fixture)
	value, err := client.AuthorizedPrompt(context.Background(), 42, []string{`E:\repo`})
	if err != nil || value != "authorized prompt" {
		t.Fatalf("prompt=%q err=%v", value, err)
	}
	fixture.mu.Lock()
	defer fixture.mu.Unlock()
	if fixture.targetCalls != 2 || fixture.fallbackCalls != 1 || fixture.reviewCalls != 1 || fixture.reviewNotFoundCalls != 0 || fixture.logCalls != 0 || fixture.targetBody == nil || fixture.witnessBody == nil {
		t.Fatalf("calls target=%d fallback=%d review=%d review404=%d log=%d bodies=%v/%v", fixture.targetCalls, fixture.fallbackCalls, fixture.reviewCalls, fixture.reviewNotFoundCalls, fixture.logCalls, fixture.targetBody != nil, fixture.witnessBody != nil)
	}
	for _, encoding := range fixture.acceptEncoding {
		if encoding != "identity" {
			t.Fatalf("Accept-Encoding=%q, want exact identity", encoding)
		}
	}
	for name, body := range map[string]*trackingReadCloser{"target": fixture.targetBody, "witness": fixture.witnessBody} {
		promptAt := bytesIndex(body.data, []byte(`"prompt"`))
		if body.read >= promptAt {
			t.Fatalf("%s prefix read=%d, prompt starts=%d", name, body.read, promptAt)
		}
	}
	var targetQuery, witnessQuery url.Values
	for _, query := range fixture.queries {
		if query.Get("id") == "42" {
			targetQuery = query
		}
		if query.Get("repo") == `E:\repo` {
			witnessQuery = query
		}
	}
	if targetQuery == nil || targetQuery.Get("repo") != "" || targetQuery.Get("limit") != "1" || targetQuery.Get("omit_prompt") != "true" {
		t.Fatalf("target query=%v", fixture.queries)
	}
	if witnessQuery == nil || witnessQuery.Get("id") != "" || witnessQuery.Get("limit") != "1" || witnessQuery.Get("omit_prompt") != "true" {
		t.Fatalf("witness query=%v", fixture.queries)
	}
}

func TestAuthorizedLazyForeignJobsNeverReachContentEndpoints(t *testing.T) {
	for _, test := range []struct {
		name   string
		status string
		log    bool
	}{
		{name: "completed prompt", status: "done"},
		{name: "queued prompt", status: "queued"},
		{name: "running prompt", status: "running"},
		{name: "running log", status: "running", log: true},
		{name: "terminal log", status: "done", log: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			fixture := &lazyAuthorizationFixture{foreignRepoID: 99, targetStatusNameValue: test.status, logStatus: test.status}
			client := newLazyAuthorizationClient(t, fixture)
			var err error
			if test.log {
				_, err = client.AuthorizedLog(context.Background(), 42, []string{`E:\repo`})
			} else {
				_, err = client.AuthorizedPrompt(context.Background(), 42, []string{`E:\repo`})
			}
			if !errors.Is(err, ErrLazyContentForbidden) {
				t.Fatalf("err=%v, want forbidden", err)
			}
			fixture.mu.Lock()
			defer fixture.mu.Unlock()
			if fixture.reviewCalls != 0 || fixture.logCalls != 0 {
				t.Fatalf("foreign content calls review=%d log=%d", fixture.reviewCalls, fixture.logCalls)
			}
			if fixture.targetBody == nil || fixture.targetBody.read >= bytesIndex(fixture.targetBody.data, []byte("VERY_SENSITIVE_FOREIGN_PROMPT")) {
				t.Fatalf("foreign target body was read beyond identity prefix: %#v", fixture.targetBody)
			}
		})
	}
}

func TestAuthorizedLazyMissingTargetReturnsNotFoundBeforeContent(t *testing.T) {
	fixture := &lazyAuthorizationFixture{targetEmpty: true}
	client := newLazyAuthorizationClient(t, fixture)
	_, err := client.AuthorizedLog(context.Background(), 42, []string{`E:\repo`})
	if !errors.Is(err, ErrLazyContentNotFound) {
		t.Fatalf("err=%v, want not found", err)
	}
	fixture.mu.Lock()
	defer fixture.mu.Unlock()
	if fixture.logCalls != 0 || fixture.reviewCalls != 0 {
		t.Fatalf("missing target content calls review=%d log=%d", fixture.reviewCalls, fixture.logCalls)
	}
}

func TestAuthorizedLazyQueuedAndRunningPromptUseOneContentFallback(t *testing.T) {
	for _, test := range []struct {
		name   string
		status string
	}{
		{name: "queued", status: "queued"},
		{name: "running", status: "running"},
	} {
		t.Run(test.name, func(t *testing.T) {
			fixture := &lazyAuthorizationFixture{fallback: true, targetStatusNameValue: test.status}
			client := newLazyAuthorizationClient(t, fixture)
			value, err := client.AuthorizedPrompt(context.Background(), 42, []string{`E:\repo`})
			want := fmt.Sprintf("authorized %s prompt", test.status)
			if err != nil || value != want {
				t.Fatalf("prompt=%q err=%v, want %q", value, err, want)
			}
			fixture.mu.Lock()
			defer fixture.mu.Unlock()
			if fixture.reviewCalls != 1 || fixture.reviewNotFoundCalls != 1 || fixture.targetCalls != 3 || fixture.fallbackCalls != 2 {
				t.Fatalf("review calls=%d review404=%d jobs calls=%d fallback=%d, want 1/1/3/2", fixture.reviewCalls, fixture.reviewNotFoundCalls, fixture.targetCalls, fixture.fallbackCalls)
			}
		})
	}
}

func TestAuthorizedLazyRawLogUsesOneCallAndAllowsEmptyRunningLog(t *testing.T) {
	fixture := &lazyAuthorizationFixture{logStatus: "running", logBody: ""}
	client := newLazyAuthorizationClient(t, fixture)
	value, err := client.AuthorizedLog(context.Background(), 42, []string{`E:\repo`})
	if err != nil || value != "" {
		t.Fatalf("log=%q err=%v, want empty success", value, err)
	}
	fixture.mu.Lock()
	defer fixture.mu.Unlock()
	if fixture.logCalls != 1 || fixture.reviewCalls != 0 {
		t.Fatalf("content calls log=%d review=%d, want 1/0", fixture.logCalls, fixture.reviewCalls)
	}
}

func TestAuthorizedLazyBindingDriftAfterWitnessPreventsContent(t *testing.T) {
	for _, log := range []bool{false, true} {
		name := "prompt"
		if log {
			name = "log"
		}
		t.Run(name, func(t *testing.T) {
			fixture := &lazyAuthorizationFixture{driftAfterWitness: true}
			client := newLazyAuthorizationClient(t, fixture)
			var err error
			if log {
				_, err = client.AuthorizedLog(context.Background(), 42, []string{`E:\repo`})
			} else {
				_, err = client.AuthorizedPrompt(context.Background(), 42, []string{`E:\repo`})
			}
			if !errors.Is(err, ErrBindingChanged) {
				t.Fatalf("err=%v, want binding changed", err)
			}
			fixture.mu.Lock()
			defer fixture.mu.Unlock()
			if fixture.reviewCalls != 0 || fixture.logCalls != 0 {
				t.Fatalf("content calls review=%d log=%d", fixture.reviewCalls, fixture.logCalls)
			}
		})
	}
}

func TestAuthorizedLazyFinalOwnershipProofRejectsMutationAfterContent(t *testing.T) {
	for _, test := range []struct {
		name     string
		status   string
		log      bool
		fallback bool
	}{
		{name: "completed prompt", status: "done"},
		{name: "queued fallback prompt", status: "queued", fallback: true},
		{name: "raw log", status: "running", log: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			fixture := &lazyAuthorizationFixture{targetStatusNameValue: test.status, fallback: test.fallback, mutateAfterContent: true, logStatus: test.status}
			client := newLazyAuthorizationClient(t, fixture)
			var err error
			if test.log {
				_, err = client.AuthorizedLog(context.Background(), 42, []string{`E:\repo`})
			} else {
				_, err = client.AuthorizedPrompt(context.Background(), 42, []string{`E:\repo`})
			}
			if !errors.Is(err, ErrLazyContentForbidden) {
				t.Fatalf("err=%v, want final ownership rejection", err)
			}
			fixture.mu.Lock()
			defer fixture.mu.Unlock()
			if fixture.reviewCalls == 0 && fixture.logCalls == 0 {
				t.Fatal("content was not fetched before final ownership proof")
			}
		})
	}
}

func TestAuthorizedLazyOwnerRejectsEmptyPathsAndNonIdentityEncoding(t *testing.T) {
	t.Run("empty paths", func(t *testing.T) {
		fixture := &lazyAuthorizationFixture{}
		client := newLazyAuthorizationClient(t, fixture)
		_, err := client.AuthorizedPrompt(context.Background(), 42, nil)
		if !errors.Is(err, ErrLazyContentForbidden) {
			t.Fatalf("err=%v, want forbidden", err)
		}
		fixture.mu.Lock()
		defer fixture.mu.Unlock()
		if fixture.reviewCalls != 0 || fixture.logCalls != 0 {
			t.Fatalf("content calls review=%d log=%d", fixture.reviewCalls, fixture.logCalls)
		}
	})
	t.Run("empty path is malformed", func(t *testing.T) {
		fixture := &lazyAuthorizationFixture{}
		client := newLazyAuthorizationClient(t, fixture)
		_, err := client.AuthorizedPrompt(context.Background(), 42, []string{""})
		if !errors.Is(err, ErrLazyContentMalformed) {
			t.Fatalf("err=%v, want malformed", err)
		}
		fixture.mu.Lock()
		defer fixture.mu.Unlock()
		if fixture.reviewCalls != 0 || fixture.logCalls != 0 {
			t.Fatalf("content calls review=%d log=%d", fixture.reviewCalls, fixture.logCalls)
		}
	})
	t.Run("non identity encoding", func(t *testing.T) {
		fixture := &lazyAuthorizationFixture{ownerEncoding: "gzip"}
		client := newLazyAuthorizationClient(t, fixture)
		_, err := client.AuthorizedPrompt(context.Background(), 42, []string{`E:\repo`})
		if !errors.Is(err, ErrLazyContentMalformed) {
			t.Fatalf("err=%v, want malformed", err)
		}
		fixture.mu.Lock()
		defer fixture.mu.Unlock()
		if fixture.reviewCalls != 0 || fixture.logCalls != 0 {
			t.Fatalf("content calls review=%d log=%d", fixture.reviewCalls, fixture.logCalls)
		}
	})
}

func TestAuthorizedLazyNon2xxOwnerBodyIsNotRead(t *testing.T) {
	fixture := &lazyAuthorizationFixture{targetHTTPStatus: http.StatusNotFound}
	client := newLazyAuthorizationClient(t, fixture)
	_, err := client.AuthorizedPrompt(context.Background(), 42, []string{`E:\repo`})
	if !errors.Is(err, ErrLazyContentNotFound) {
		t.Fatalf("err=%v, want not found", err)
	}
	fixture.mu.Lock()
	defer fixture.mu.Unlock()
	if fixture.errorBody == nil || fixture.errorBody.read != 0 {
		t.Fatalf("non-2xx body reads=%d, want 0", fixture.errorBody.read)
	}
	if fixture.reviewCalls != 0 || fixture.logCalls != 0 {
		t.Fatalf("content calls review=%d log=%d", fixture.reviewCalls, fixture.logCalls)
	}
}

func TestAuthorizedLazyWitnessErrorsContinueToValidMatch(t *testing.T) {
	validWitness := func(fixture *lazyAuthorizationFixture) string {
		return fmt.Sprintf(`{"jobs":[%s]}`, fixture.reviewJobJSON(99, 7, "done", "synthesis", "synthesis", "witness prompt"))
	}
	for _, test := range []struct {
		name        string
		makeFixture func(*lazyAuthorizationFixture, string)
	}{
		{name: "non-2xx", makeFixture: func(f *lazyAuthorizationFixture, valid string) {
			f.witnessStatuses = []int{http.StatusInternalServerError, http.StatusOK}
			f.witnessBodies = []string{"SENSITIVE_WITNESS_ERROR", valid}
		}},
		{name: "malformed", makeFixture: func(f *lazyAuthorizationFixture, valid string) {
			f.witnessBodies = []string{"{", valid}
		}},
		{name: "transport", makeFixture: func(f *lazyAuthorizationFixture, valid string) {
			f.witnessTransportErrs = []error{errors.New("witness transport failure")}
			f.witnessBodies = []string{"", valid}
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			fixture := &lazyAuthorizationFixture{}
			test.makeFixture(fixture, validWitness(fixture))
			client := newLazyAuthorizationClient(t, fixture)
			value, err := client.AuthorizedPrompt(context.Background(), 42, []string{`E:\repo`, `E:\repo-two`})
			if err != nil || value != "authorized prompt" {
				t.Fatalf("prompt=%q err=%v, want valid witness success", value, err)
			}
			fixture.mu.Lock()
			defer fixture.mu.Unlock()
			if fixture.reviewCalls != 1 || fixture.witnessIndex != 3 {
				t.Fatalf("review calls=%d witness calls=%d, want 1/3", fixture.reviewCalls, fixture.witnessIndex)
			}
			if test.name == "non-2xx" && (fixture.witnessErrorBody == nil || fixture.witnessErrorBody.read != 0) {
				reads := -1
				if fixture.witnessErrorBody != nil {
					reads = fixture.witnessErrorBody.read
				}
				t.Fatalf("non-2xx witness body reads=%d, want 0", reads)
			}
		})
	}
}

func TestAuthorizedLazyAllWitnessErrorsReturnFirstWithoutContent(t *testing.T) {
	fixture := &lazyAuthorizationFixture{
		witnessStatuses: []int{http.StatusInternalServerError, http.StatusBadGateway},
		witnessBodies:   []string{"first secret", "second secret"},
	}
	client := newLazyAuthorizationClient(t, fixture)
	_, err := client.AuthorizedPrompt(context.Background(), 42, []string{`E:\repo`, `E:\repo-two`})
	if !errors.Is(err, ErrLazyContentMalformed) {
		t.Fatalf("err=%v, want first witness malformed error", err)
	}
	fixture.mu.Lock()
	defer fixture.mu.Unlock()
	if fixture.witnessIndex != 2 || fixture.reviewCalls != 0 || fixture.logCalls != 0 {
		t.Fatalf("witness calls=%d review=%d log=%d, want 2/0/0", fixture.witnessIndex, fixture.reviewCalls, fixture.logCalls)
	}
}

func TestAuthorizedLazyWitnessNotFoundRemainsNegative(t *testing.T) {
	fixture := &lazyAuthorizationFixture{
		foreignRepoID:   99,
		witnessStatuses: []int{http.StatusNotFound, http.StatusOK},
		witnessBodies:   []string{"not found witness", `{"jobs":[]}`},
	}
	client := newLazyAuthorizationClient(t, fixture)
	_, err := client.AuthorizedPrompt(context.Background(), 42, []string{`E:\repo`, `E:\repo-two`})
	if !errors.Is(err, ErrLazyContentForbidden) {
		t.Fatalf("err=%v, want forbidden for mismatch-only witnesses", err)
	}
	fixture.mu.Lock()
	defer fixture.mu.Unlock()
	if fixture.reviewCalls != 0 || fixture.logCalls != 0 {
		t.Fatalf("content calls review=%d log=%d, want zero", fixture.reviewCalls, fixture.logCalls)
	}
}

type lazyOwnerRedirectTransport struct {
	redirectBody    *trackingReadCloser
	destinationCall int
}

func (t *lazyOwnerRedirectTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	health := func() *http.Response {
		return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(`{"healthy":true,"version":"v0.64.0"}`))}
	}
	jsonResponse := func(body string) *http.Response {
		return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(body))}
	}
	switch request.URL.Path {
	case "/api/health":
		return health(), nil
	case "/api/jobs":
		if request.URL.Query().Get("id") != "" {
			return jsonResponse(`{"jobs":[{"id":42,"repo_id":7,"prompt":"SENSITIVE_TARGET_PROMPT"}]}`), nil
		}
		t.redirectBody = &trackingReadCloser{data: []byte("SENSITIVE_REDIRECT_BODY")}
		return &http.Response{
			StatusCode: http.StatusFound,
			Header:     http.Header{"Content-Type": []string{"application/json"}, "Location": []string{"http://127.0.0.1:7373/destination"}},
			Body:       t.redirectBody,
		}, nil
	case "/destination":
		t.destinationCall++
		return jsonResponse(`{"jobs":[{"id":99,"repo_id":7}]}`), nil
	default:
		return &http.Response{StatusCode: http.StatusNotFound, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader("not found"))}, nil
	}
}

func TestLazyOwnerRedirectDoesNotFollowOrReadBody(t *testing.T) {
	transport := &lazyOwnerRedirectTransport{}
	runner := &lazyContentRunner{}
	client, err := NewClient("roborev", runner, "127.0.0.1:7373")
	if err != nil {
		t.Fatal(err)
	}
	client.HTTP = &http.Client{Transport: transport}
	_, err = client.AuthorizedPrompt(context.Background(), 42, []string{`E:\repo`})
	if !errors.Is(err, ErrLazyContentMalformed) {
		t.Fatalf("err=%v, want malformed redirect status", err)
	}
	if transport.destinationCall != 0 {
		t.Fatalf("redirect destination calls=%d, want 0", transport.destinationCall)
	}
	if transport.redirectBody == nil || transport.redirectBody.read != 0 {
		reads := -1
		if transport.redirectBody != nil {
			reads = transport.redirectBody.read
		}
		t.Fatalf("redirect body reads=%d, want 0", reads)
	}
}

func TestAuthorizedLazyJobTypesUseFullRowsAndSynthesisWitnessForMembers(t *testing.T) {
	tests := []struct {
		name      string
		jobType   string
		panelRole string
	}{
		{name: "review", jobType: "review"},
		{name: "range", jobType: "range"},
		{name: "dirty", jobType: "dirty"},
		{name: "worktree", jobType: "worktree"},
		{name: "staged", jobType: "staged"},
		{name: "task", jobType: "task"},
		{name: "insights", jobType: "insights"},
		{name: "compact", jobType: "compact"},
		{name: "fix", jobType: "fix"},
		{name: "classify", jobType: "classify"},
		{name: "synthesis", jobType: "synthesis", panelRole: "synthesis"},
		{name: "panel member", jobType: "review", panelRole: "member"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := &lazyAuthorizationFixture{targetStatusNameValue: "done", targetJobType: test.jobType, targetPanelRole: test.panelRole}
			client := newLazyAuthorizationClient(t, fixture)
			value, err := client.AuthorizedPrompt(context.Background(), 42, []string{`E:\repo`})
			if err != nil || value != "authorized prompt" {
				t.Fatalf("job type=%s prompt=%q err=%v", test.jobType, value, err)
			}
			fixture.mu.Lock()
			defer fixture.mu.Unlock()
			if fixture.targetCalls != 2 || fixture.fallbackCalls != 1 || fixture.reviewCalls != 1 || fixture.targetBody == nil || fixture.witnessBody == nil {
				t.Fatalf("job type=%s calls target=%d fallback=%d witness=%v review=%d", test.jobType, fixture.targetCalls, fixture.fallbackCalls, fixture.witnessBody != nil, fixture.reviewCalls)
			}
			if test.name == "panel member" && (!strings.Contains(string(fixture.targetBody.data), `"job_type":"review"`) || !strings.Contains(string(fixture.targetBody.data), `"panel_role":"member"`) || !strings.Contains(string(fixture.witnessBody.data), `"job_type":"synthesis"`) || !strings.Contains(string(fixture.witnessBody.data), `"panel_role":"synthesis"`)) {
				t.Fatalf("member witness is not synthesis row: %s", fixture.witnessBody.data)
			}
		})
	}
}

func TestLazyOwnerPrefixFailsClosedOnReorderedAndOversizedRows(t *testing.T) {
	tests := []struct {
		name string
		body string
		want error
	}{
		{name: "reordered", body: `{"jobs":[{"repo_id":7,"id":42}]}`, want: ErrLazyContentMalformed},
		{name: "wrong row", body: `{"jobs":[42]}`, want: ErrLazyContentMalformed},
		{name: "repo id closes row", body: `{"jobs":[{"id":42,"repo_id":7}]}`, want: ErrLazyContentMalformed},
		{name: "truncated repo id", body: `{"jobs":[{"id":42,"repo_id":7`, want: ErrLazyContentMalformed},
		{name: "oversized prefix", body: strings.Repeat(" ", lazyOwnerPrefixLimit+1), want: ErrLazyContentOversized},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, _, err := parseLazyOwnerPrefix(strings.NewReader(test.body), 42)
			if !errors.Is(err, test.want) {
				t.Fatalf("err=%v, want %v", err, test.want)
			}
		})
	}
}

func bytesIndex(value, needle []byte) int {
	for index := 0; index+len(needle) <= len(value); index++ {
		if string(value[index:index+len(needle)]) == string(needle) {
			return index
		}
	}
	return len(value) + 1
}
