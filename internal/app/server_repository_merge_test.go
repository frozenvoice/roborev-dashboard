package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/froze/roborev-dashboard/internal/gitx"
	"github.com/froze/roborev-dashboard/internal/repoinventory"
	"github.com/froze/roborev-dashboard/internal/repomerge"
	"github.com/froze/roborev-dashboard/internal/repomove"
	"github.com/froze/roborev-dashboard/internal/roborev"
)

type mergeHTTPService struct {
	*fakeService
	mu               sync.Mutex
	eligibility      repomerge.Eligibility
	eligibilityErr   error
	preflight        repomerge.Preflight
	preflightErr     error
	mergeResult      repomerge.ExecutionResult
	mergeErr         error
	eligibilityCalls int
	preflightCalls   int
	mergeCalls       int
	mergeStarted     chan struct{}
	mergeBlock       <-chan struct{}
}

func (s *mergeHTTPService) RepositoryMergeEligibility(context.Context, string) (repomerge.Eligibility, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.eligibilityCalls++
	return s.eligibility, s.eligibilityErr
}

func (s *mergeHTTPService) RepositoryMergePreflight(context.Context, repomerge.Request) (repomerge.Preflight, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.preflightCalls++
	return s.preflight, s.preflightErr
}

func (s *mergeHTTPService) RepositoryMerge(context.Context, repomerge.Request, repomerge.Preflight) (repomerge.ExecutionResult, error) {
	if s.mergeStarted != nil {
		select {
		case s.mergeStarted <- struct{}{}:
		default:
		}
	}
	if s.mergeBlock != nil {
		<-s.mergeBlock
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.mergeCalls++
	if s.mergeResult == (repomerge.ExecutionResult{}) && s.mergeErr == nil {
		return repomerge.ExecutionResult{Started: true, Verified: true}, nil
	}
	return s.mergeResult, s.mergeErr
}

var _ roborev.RepositoryMergeService = (*mergeHTTPService)(nil)

func mergeTestServer(t *testing.T, service *mergeHTTPService, inventory repoinventory.Inventory) *Server {
	t.Helper()
	const target = `E:\target\repo`
	service.eligibility = repomerge.Eligibility{Source: repomerge.RepositorySnapshot{Path: `E:\stale\repo`, Name: "old", JobCount: 7}, Inventory: inventory}
	service.preflight = repomerge.Preflight{Source: service.eligibility.Source, Target: repomerge.RepositorySnapshot{Path: target, Name: "target", JobCount: 3}, Command: repomerge.Command{Executable: "roborev", Args: []string{"repo", "merge", "--yes", `E:\stale\repo`, target}, WorkingDirectory: ""}, Binding: "1:roborev", CLIVersion: "v0.64.0", DaemonVersion: "v0.64.0"}
	registry := NewRegistry(fakeInspector{}, "")
	if _, err := registry.Add(context.Background(), target); err != nil {
		t.Fatal(err)
	}
	server, err := NewServer(service, registry, "")
	if err != nil {
		t.Fatal(err)
	}
	return server
}

func mergePost(t *testing.T, server *Server, path string, body string) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	request.Host = "example.com"
	request.Header.Set("Origin", "http://example.com")
	request.Header.Set("X-CSRF-Token", server.csrfToken)
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	return response
}

func mergeEligibilityBody(source string) string {
	value, _ := json.Marshal(map[string]string{"sourcePath": source})
	return string(value)
}

func mergePreviewBody(source, identity string) string {
	value, _ := json.Marshal(map[string]string{"sourcePath": source, "targetRepositoryIdentity": identity})
	return string(value)
}

func mergeTargetInventory(source string) repoinventory.Inventory {
	return repoinventory.Inventory{Repositories: []repoinventory.Repository{{Name: "old", RootPath: source, Count: 7}, {Name: "target", RootPath: `E:\target\repo`, Count: 3}}, TotalCount: 10}
}

func mergeToken(t *testing.T, server *Server) string {
	t.Helper()
	response := mergePost(t, server, "/api/repository-merges/preview", mergePreviewBody(`E:\stale\repo`, gitx.RepositoryKey(`E:\target\repo`)))
	if response.Code != http.StatusOK {
		t.Fatalf("preview status=%d body=%s", response.Code, response.Body.String())
	}
	var dto repositoryMergePreviewDTO
	if err := json.Unmarshal(response.Body.Bytes(), &dto); err != nil || dto.Token == "" {
		t.Fatalf("preview dto=%#v err=%v", dto, err)
	}
	return dto.Token
}

func TestRepositoryMergeEligibilityBuildsOnlyRegisteredOpaqueTargets(t *testing.T) {
	service := &mergeHTTPService{fakeService: &fakeService{}}
	inventory := mergeTargetInventory(`E:\stale\repo`)
	inventory.Repositories[1].Identity = "upstream-secret-identity"
	server := mergeTestServer(t, service, inventory)
	response := mergePost(t, server, "/api/repository-merges/eligibility", mergeEligibilityBody(`E:\stale\repo`))
	if response.Code != http.StatusOK || service.eligibilityCalls != 1 {
		t.Fatalf("status=%d eligibility=%d body=%s", response.Code, service.eligibilityCalls, response.Body.String())
	}
	var dto repositoryMergeEligibilityDTO
	if err := json.Unmarshal(response.Body.Bytes(), &dto); err != nil || !dto.Eligible || len(dto.Targets) != 1 {
		t.Fatalf("dto=%#v err=%v body=%s", dto, err, response.Body.String())
	}
	if dto.Targets[0].RepositoryIdentity != gitx.RepositoryKey(`E:\target\repo`) || strings.Contains(response.Body.String(), "upstream-secret-identity") {
		t.Fatalf("identity contract leaked or mismatched: %s", response.Body.String())
	}
}

func TestRepositoryMergeEligibilityRejectsAmbiguousRegisteredTarget(t *testing.T) {
	service := &mergeHTTPService{fakeService: &fakeService{}}
	inventory := mergeTargetInventory(`E:\stale\repo`)
	inventory.Repositories = append(inventory.Repositories, repoinventory.Repository{Name: "duplicate-target", RootPath: `E:\target\repo`, Count: 3})
	inventory.TotalCount += 3
	server := mergeTestServer(t, service, inventory)
	response := mergePost(t, server, "/api/repository-merges/eligibility", mergeEligibilityBody(`E:\stale\repo`))
	if response.Code != http.StatusConflict || service.eligibilityCalls != 1 || !strings.Contains(response.Body.String(), "ambiguous") {
		t.Fatalf("status=%d calls=%d body=%s", response.Code, service.eligibilityCalls, response.Body.String())
	}
}

func TestRepositoryMergeEligibilityReasonsAndNoServiceForRegisteredSource(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want string
	}{
		{name: "source exists", err: repomove.ErrSourceExists, want: "source-exists"},
		{name: "empty", err: repomerge.ErrSourceEmpty, want: "source-empty"},
		{name: "active", err: roborev.ErrRepositoryMergeActiveJobs, want: "active-source-jobs"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			service := &mergeHTTPService{fakeService: &fakeService{}, eligibilityErr: tc.err}
			server := mergeTestServer(t, service, mergeTargetInventory(`E:\stale\repo`))
			response := mergePost(t, server, "/api/repository-merges/eligibility", mergeEligibilityBody(`E:\stale\repo`))
			if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"reason":"`+tc.want+`"`) || service.eligibilityCalls != 1 {
				t.Fatalf("status=%d calls=%d body=%s", response.Code, service.eligibilityCalls, response.Body.String())
			}
		})
	}
	noTargetsService := &mergeHTTPService{fakeService: &fakeService{}}
	noTargetsServer := mergeTestServer(t, noTargetsService, repoinventory.Inventory{Repositories: []repoinventory.Repository{{Name: "old", RootPath: `E:\stale\repo`, Count: 7}}, TotalCount: 7})
	noTargets := mergePost(t, noTargetsServer, "/api/repository-merges/eligibility", mergeEligibilityBody(`E:\stale\repo`))
	if noTargets.Code != http.StatusOK || !strings.Contains(noTargets.Body.String(), `"reason":"no-targets"`) || noTargetsService.eligibilityCalls != 1 {
		t.Fatalf("no targets status=%d calls=%d body=%s", noTargets.Code, noTargetsService.eligibilityCalls, noTargets.Body.String())
	}
	service := &mergeHTTPService{fakeService: &fakeService{}}
	server := mergeTestServer(t, service, mergeTargetInventory(`E:\stale\repo`))
	if _, err := server.Registry.Add(context.Background(), `E:\stale\repo`); err != nil {
		t.Fatal(err)
	}
	response := mergePost(t, server, "/api/repository-merges/eligibility", mergeEligibilityBody(`E:\stale\repo`))
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"reason":"dashboard-registered"`) || service.eligibilityCalls != 0 {
		t.Fatalf("registered source status=%d calls=%d body=%s", response.Code, service.eligibilityCalls, response.Body.String())
	}
}

func TestRepositoryMergePreviewConfirmUsesOpaqueTokenOnce(t *testing.T) {
	service := &mergeHTTPService{fakeService: &fakeService{}}
	server := mergeTestServer(t, service, mergeTargetInventory(`E:\stale\repo`))
	selectedBefore := server.Registry.Selected()
	token := mergeToken(t, server)
	response := mergePost(t, server, "/api/repository-merges", `{"token":"`+token+`"}`)
	second := mergePost(t, server, "/api/repository-merges", `{"token":"`+token+`"}`)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"status":"merged"`) || second.Code != http.StatusConflict || service.preflightCalls != 1 || service.mergeCalls != 1 {
		t.Fatalf("first=%d second=%d calls=%d firstBody=%s secondBody=%s", response.Code, second.Code, service.mergeCalls, response.Body.String(), second.Body.String())
	}
	if server.Registry.Selected() != selectedBefore || server.Registry.Contains(`E:\stale\repo`) || !server.Registry.Contains(`E:\target\repo`) {
		t.Fatalf("merge changed Dashboard Registry selected=%q before=%q source=%v target=%v", server.Registry.Selected(), selectedBefore, server.Registry.Contains(`E:\stale\repo`), server.Registry.Contains(`E:\target\repo`))
	}
}

func TestRepositoryMergePreviewBindsAuthoritativeFactsAndCommand(t *testing.T) {
	service := &mergeHTTPService{fakeService: &fakeService{}}
	server := mergeTestServer(t, service, mergeTargetInventory(`E:\stale\repo`))
	service.preflight.Source = repomerge.RepositorySnapshot{Path: `E:\stale\repo`, Name: "fresh source", JobCount: 9}
	service.preflight.Target = repomerge.RepositorySnapshot{Path: `E:\target\repo`, Name: "fresh target", JobCount: 4}
	service.preflight.Command = repomerge.Command{Executable: `C:\tools\roborev.exe`, Args: []string{"repo", "merge", "--yes", `E:\stale\repo`, `E:\target\repo`}, WorkingDirectory: `C:\private\cwd`}
	response := mergePost(t, server, "/api/repository-merges/preview", mergePreviewBody(`E:\stale\repo`, gitx.RepositoryKey(`E:\target\repo`)))
	if response.Code != http.StatusOK || service.preflightCalls != 1 {
		t.Fatalf("status=%d preflight=%d body=%s", response.Code, service.preflightCalls, response.Body.String())
	}
	var dto repositoryMergePreviewDTO
	if err := json.Unmarshal(response.Body.Bytes(), &dto); err != nil {
		t.Fatal(err)
	}
	if dto.Source.Name != "fresh source" || dto.Source.JobCount != 9 || dto.Target.Name != "fresh target" || dto.Target.JobCount != 4 || dto.Command.Executable != "roborev" || service.preflight.Command.Executable != `C:\tools\roborev.exe` || service.preflight.Command.WorkingDirectory != `C:\private\cwd` || len(dto.Command.Args) != 5 || dto.Command.Args[2] != "--yes" || dto.Command.WorkingDirectory != "" || dto.Token == "" {
		t.Fatalf("authoritative preview dto=%#v body=%s", dto, response.Body.String())
	}
}

func TestRepositoryMergeMutationAuthRejectsBeforeService(t *testing.T) {
	service := &mergeHTTPService{fakeService: &fakeService{}}
	server := mergeTestServer(t, service, mergeTargetInventory(`E:\stale\repo`))
	for _, tc := range []struct {
		name   string
		origin string
		csrf   string
	}{
		{name: "foreign origin", origin: "http://evil.example", csrf: server.csrfToken},
		{name: "missing csrf", origin: "http://example.com"},
		{name: "wrong csrf", origin: "http://example.com", csrf: "wrong"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodPost, "/api/repository-merges/eligibility", strings.NewReader(mergeEligibilityBody(`E:\stale\repo`)))
			request.Host = "example.com"
			request.Header.Set("Origin", tc.origin)
			if tc.csrf != "" {
				request.Header.Set("X-CSRF-Token", tc.csrf)
			}
			response := httptest.NewRecorder()
			server.Handler().ServeHTTP(response, request)
			if response.Code != http.StatusForbidden || service.eligibilityCalls != 0 {
				t.Fatalf("status=%d calls=%d body=%s", response.Code, service.eligibilityCalls, response.Body.String())
			}
		})
	}
}

func TestRepositoryMergeConfirmBusyPreservesToken(t *testing.T) {
	service := &mergeHTTPService{fakeService: &fakeService{}}
	server := mergeTestServer(t, service, mergeTargetInventory(`E:\stale\repo`))
	token := mergeToken(t, server)
	release, err := server.Gate.AcquireRepository(gitx.RepositoryKey(`E:\target\repo`))
	if err != nil {
		t.Fatal(err)
	}
	busy := mergePost(t, server, "/api/repository-merges", `{"token":"`+token+`"}`)
	release()
	retry := mergePost(t, server, "/api/repository-merges", `{"token":"`+token+`"}`)
	if busy.Code != http.StatusConflict || retry.Code != http.StatusOK || service.mergeCalls != 1 {
		t.Fatalf("busy=%d retry=%d calls=%d busyBody=%s retryBody=%s", busy.Code, retry.Code, service.mergeCalls, busy.Body.String(), retry.Body.String())
	}
}

func TestRepositoryMergeConfirmTargetDriftConsumesTokenWithoutProcess(t *testing.T) {
	service := &mergeHTTPService{fakeService: &fakeService{}}
	inspector := &mutableInspector{repository: gitx.Repository{Name: "target", Path: `E:\target\repo`}}
	registry := NewRegistry(inspector, "")
	if _, err := registry.Add(context.Background(), `E:\target\repo`); err != nil {
		t.Fatal(err)
	}
	server, err := NewServer(service, registry, "")
	if err != nil {
		t.Fatal(err)
	}
	service.eligibility = repomerge.Eligibility{Source: repomerge.RepositorySnapshot{Path: `E:\stale\repo`, Name: "old", JobCount: 7}, Inventory: mergeTargetInventory(`E:\stale\repo`)}
	service.preflight = repomerge.Preflight{Source: service.eligibility.Source, Target: repomerge.RepositorySnapshot{Path: `E:\target\repo`, Name: "target", JobCount: 3}, Command: repomerge.Command{Executable: "roborev", Args: []string{"repo", "merge", "--yes", `E:\stale\repo`, `E:\target\repo`}}}
	token := mergeToken(t, server)
	inspector.repository.Path = `E:\other\repo`
	first := mergePost(t, server, "/api/repository-merges", `{"token":"`+token+`"}`)
	second := mergePost(t, server, "/api/repository-merges", `{"token":"`+token+`"}`)
	if first.Code != http.StatusConflict || second.Code != http.StatusConflict || service.mergeCalls != 0 {
		t.Fatalf("first=%d second=%d calls=%d firstBody=%s secondBody=%s", first.Code, second.Code, service.mergeCalls, first.Body.String(), second.Body.String())
	}
}

func TestRepositoryMergeConfirmSourceRegistrationDriftConsumesToken(t *testing.T) {
	service := &mergeHTTPService{fakeService: &fakeService{}}
	server := mergeTestServer(t, service, mergeTargetInventory(`E:\stale\repo`))
	token := mergeToken(t, server)
	if _, err := server.Registry.Add(context.Background(), `E:\stale\repo`); err != nil {
		t.Fatal(err)
	}
	first := mergePost(t, server, "/api/repository-merges", `{"token":"`+token+`"}`)
	second := mergePost(t, server, "/api/repository-merges", `{"token":"`+token+`"}`)
	if first.Code != http.StatusConflict || second.Code != http.StatusConflict || service.mergeCalls != 0 {
		t.Fatalf("first=%d second=%d calls=%d firstBody=%s secondBody=%s", first.Code, second.Code, service.mergeCalls, first.Body.String(), second.Body.String())
	}
}

func TestRepositoryMergeConcurrentClaimRunsExactlyOnce(t *testing.T) {
	service := &mergeHTTPService{fakeService: &fakeService{}, mergeStarted: make(chan struct{}, 1)}
	allow := make(chan struct{})
	service.mergeBlock = allow
	server := mergeTestServer(t, service, mergeTargetInventory(`E:\stale\repo`))
	token := mergeToken(t, server)
	responses := make(chan *httptest.ResponseRecorder, 2)
	go func() { responses <- mergePost(t, server, "/api/repository-merges", `{"token":"`+token+`"}`) }()
	select {
	case <-service.mergeStarted:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for merge process")
	}
	go func() { responses <- mergePost(t, server, "/api/repository-merges", `{"token":"`+token+`"}`) }()
	var busy *httptest.ResponseRecorder
	select {
	case busy = <-responses:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for concurrent confirm")
	}
	close(allow)
	var finished *httptest.ResponseRecorder
	select {
	case finished = <-responses:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for merge completion")
	}
	if busy.Code != http.StatusConflict || finished.Code != http.StatusOK || service.mergeCalls != 1 {
		t.Fatalf("busy=%d finished=%d calls=%d busyBody=%s finishedBody=%s", busy.Code, finished.Code, service.mergeCalls, busy.Body.String(), finished.Body.String())
	}
}

func TestRepositoryMergeRejectsQueryAndUnknownBodyBeforeService(t *testing.T) {
	service := &mergeHTTPService{fakeService: &fakeService{}}
	server := mergeTestServer(t, service, mergeTargetInventory(`E:\stale\repo`))
	for _, path := range []string{"/api/repository-merges/eligibility?x=1", "/api/repository-merges/preview?x=1", "/api/repository-merges?x=1"} {
		response := mergePost(t, server, path, `{"sourcePath":"E:\\stale\\repo","targetRepositoryIdentity":"`+gitx.RepositoryKey(`E:\target\repo`)+`","token":"x","extra":true}`)
		if response.Code != http.StatusBadRequest {
			t.Errorf("path=%s status=%d body=%s", path, response.Code, response.Body.String())
		}
	}
	if service.eligibilityCalls != 0 || service.preflightCalls != 0 || service.mergeCalls != 0 {
		t.Fatalf("service called for rejected request: eligibility=%d preflight=%d merge=%d", service.eligibilityCalls, service.preflightCalls, service.mergeCalls)
	}
}

func TestRepositoryMergeErrorTaxonomy(t *testing.T) {
	for _, tc := range []struct {
		name string
		err  error
		want int
	}{
		{name: "offline", err: roborev.ErrRepositoryMergeOffline, want: http.StatusServiceUnavailable},
		{name: "unhealthy", err: roborev.ErrRepositoryMergeUnhealthy, want: http.StatusServiceUnavailable},
		{name: "deadline", err: context.DeadlineExceeded, want: http.StatusGatewayTimeout},
		{name: "unknown", err: errors.New("unknown"), want: http.StatusBadGateway},
	} {
		t.Run(tc.name, func(t *testing.T) {
			service := &mergeHTTPService{fakeService: &fakeService{}, preflightErr: tc.err}
			server := mergeTestServer(t, service, mergeTargetInventory(`E:\stale\repo`))
			response := mergePost(t, server, "/api/repository-merges/preview", mergePreviewBody(`E:\stale\repo`, gitx.RepositoryKey(`E:\target\repo`)))
			if response.Code != tc.want {
				t.Fatalf("status=%d want=%d body=%s", response.Code, tc.want, response.Body.String())
			}
		})
	}
}

func TestRepositoryMergeConfirmStartedOutcomeTaxonomy(t *testing.T) {
	for _, tc := range []struct {
		name       string
		result     repomerge.ExecutionResult
		err        error
		wantStatus int
		wantBody   string
	}{
		{name: "start failure", err: errors.New("private stderr marker C:\\private\\roborev.exe"), wantStatus: http.StatusBadGateway},
		{name: "started unverified", result: repomerge.ExecutionResult{Started: true}, wantStatus: http.StatusOK, wantBody: `"status":"unverified"`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			service := &mergeHTTPService{fakeService: &fakeService{}, mergeResult: tc.result, mergeErr: tc.err}
			server := mergeTestServer(t, service, mergeTargetInventory(`E:\stale\repo`))
			token := mergeToken(t, server)
			response := mergePost(t, server, "/api/repository-merges", `{"token":"`+token+`"}`)
			if response.Code != tc.wantStatus || service.mergeCalls != 1 || (tc.wantBody != "" && !strings.Contains(response.Body.String(), tc.wantBody)) {
				t.Fatalf("status=%d want=%d calls=%d body=%s", response.Code, tc.wantStatus, service.mergeCalls, response.Body.String())
			}
			if strings.Contains(response.Body.String(), "private stderr marker") || strings.Contains(response.Body.String(), `C:\private\roborev.exe`) {
				t.Fatalf("private process detail leaked in response: %s", response.Body.String())
			}
		})
	}
}

func TestRepositoryMergePreviewExpiresTokens(t *testing.T) {
	service := &mergeHTTPService{fakeService: &fakeService{}}
	server := mergeTestServer(t, service, mergeTargetInventory(`E:\stale\repo`))
	token := mergeToken(t, server)
	server.mergePreviews.mu.Lock()
	record := server.mergePreviews.pending[token]
	record.ExpiresAt = time.Now().Add(-time.Second)
	server.mergePreviews.pending[token] = record
	server.mergePreviews.mu.Unlock()
	response := mergePost(t, server, "/api/repository-merges", `{"token":"`+token+`"}`)
	if response.Code != http.StatusConflict || service.mergeCalls != 0 {
		t.Fatalf("status=%d calls=%d body=%s", response.Code, service.mergeCalls, response.Body.String())
	}
}

func TestRepositoryMergePreviewStoreExpiresBeforeCapacityAndEvictsDeterministically(t *testing.T) {
	store := newRepositoryMergePreviewStore()
	now := time.Now()
	store.pending["expired"] = repositoryMergePreviewRecord{ExpiresAt: now.Add(-time.Second)}
	for index := 0; index < repositoryMergePreviewMax-1; index++ {
		store.pending[fmt.Sprintf("live-%02d", index)] = repositoryMergePreviewRecord{ExpiresAt: now.Add(time.Minute)}
	}
	if _, err := store.put(repositoryMergePreviewRecord{ExpiresAt: now.Add(time.Minute)}); err != nil {
		t.Fatal(err)
	}
	if len(store.pending) != repositoryMergePreviewMax {
		t.Fatalf("expiry was not processed first: pending=%d", len(store.pending))
	}
	if _, ok := store.pending["expired"]; ok {
		t.Fatal("expired preview was not removed before capacity eviction")
	}
	store = newRepositoryMergePreviewStore()
	for index := 0; index < repositoryMergePreviewMax; index++ {
		store.pending[fmt.Sprintf("token-%02d", index)] = repositoryMergePreviewRecord{ExpiresAt: now.Add(time.Minute)}
	}
	if _, err := store.put(repositoryMergePreviewRecord{ExpiresAt: now.Add(time.Minute)}); err != nil {
		t.Fatal(err)
	}
	if _, ok := store.pending["token-00"]; ok {
		t.Fatal("lexicographically oldest token was not evicted on expiry tie")
	}
	if len(store.pending) != repositoryMergePreviewMax {
		t.Fatalf("pending=%d want=%d", len(store.pending), repositoryMergePreviewMax)
	}
}

func TestRepositoryMergeConfirmRejectsMalformedAndUnknownTokens(t *testing.T) {
	service := &mergeHTTPService{fakeService: &fakeService{}}
	server := mergeTestServer(t, service, mergeTargetInventory(`E:\stale\repo`))
	if response := mergePost(t, server, "/api/repository-merges", `{"token":""}`); response.Code != http.StatusBadRequest {
		t.Fatalf("empty token status=%d body=%s", response.Code, response.Body.String())
	}
	if response := mergePost(t, server, "/api/repository-merges", `{"token":"not-a-preview-token"}`); response.Code != http.StatusConflict {
		t.Fatalf("unknown token status=%d body=%s", response.Code, response.Body.String())
	}
	if service.mergeCalls != 0 {
		t.Fatalf("malformed token launched merge: %d", service.mergeCalls)
	}
}
