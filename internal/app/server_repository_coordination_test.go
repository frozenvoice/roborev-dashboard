package app

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/froze/roborev-dashboard/internal/gitx"
	"github.com/froze/roborev-dashboard/internal/insights"
	"github.com/froze/roborev-dashboard/internal/repoinventory"
	"github.com/froze/roborev-dashboard/internal/repomove"
	"github.com/froze/roborev-dashboard/internal/reporename"
	"github.com/froze/roborev-dashboard/internal/roborev"
)

// coordinationService implements the optional adapters needed by the
// cross-feature tests. Each blocking adapter signals after its process-boundary
// call has been admitted, which proves the shared repository lease is held by
// the handler rather than merely testing Gate in isolation.
type coordinationService struct {
	*moveHTTPService

	renameStarted chan struct{}
	renameBlock   <-chan struct{}
	renameCalls   int

	reviewStarted chan struct{}
	reviewBlock   <-chan struct{}

	insightsStarted chan struct{}
	insightsBlock   <-chan struct{}
	insightsCalls   int

	moveStarted chan struct{}
	moveBlock   <-chan struct{}
}

func (s *coordinationService) RepositoryRenamePreflight(_ context.Context, request reporename.Request) (reporename.Preflight, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.preflightCalls++
	return reporename.Preflight{Command: reporename.Command{Executable: "roborev", Args: []string{"repo", "rename", request.Path, request.NewName}}, Binding: "1:roborev", CLIVersion: "v0.64.0", DaemonVersion: "v0.64.0"}, nil
}

func (s *coordinationService) RepositoryRename(_ context.Context, _ reporename.Request, _ reporename.Preflight) (reporename.ExecutionResult, error) {
	if s.renameStarted != nil {
		select {
		case s.renameStarted <- struct{}{}:
		default:
		}
	}
	if s.renameBlock != nil {
		<-s.renameBlock
	}
	s.mu.Lock()
	s.renameCalls++
	s.mu.Unlock()
	return reporename.ExecutionResult{Started: true, Verified: true}, nil
}

func (s *coordinationService) RepositoryMove(_ context.Context, _ repomove.Request, _ repomove.Preflight) (repomove.ExecutionResult, error) {
	if s.moveStarted != nil {
		select {
		case s.moveStarted <- struct{}{}:
		default:
		}
	}
	if s.moveBlock != nil {
		<-s.moveBlock
	}
	s.mu.Lock()
	s.moveCalls++
	s.mu.Unlock()
	return repomove.ExecutionResult{Started: true, Verified: true}, nil
}

func (s *coordinationService) Review(ctx context.Context, request roborev.ReviewRequest) (json.RawMessage, error) {
	if s.reviewStarted != nil {
		select {
		case s.reviewStarted <- struct{}{}:
		default:
		}
	}
	if s.reviewBlock != nil {
		<-s.reviewBlock
	}
	return s.fakeService.Review(ctx, request)
}

func (s *coordinationService) InsightsPreflight(context.Context, insights.Request) (insights.Preflight, error) {
	return insights.Preflight{Command: insights.Command{Executable: "roborev", Args: []string{"insights", "--json"}, Dir: `E:\destination`}, Binding: "1:roborev", CLIVersion: "v0.64.0", DaemonVersion: "v0.64.0"}, nil
}

func (s *coordinationService) Insights(context.Context, insights.Request, insights.Preflight) (insights.LaunchResult, error) {
	if s.insightsStarted != nil {
		select {
		case s.insightsStarted <- struct{}{}:
		default:
		}
	}
	if s.insightsBlock != nil {
		<-s.insightsBlock
	}
	s.mu.Lock()
	s.insightsCalls++
	s.mu.Unlock()
	return insights.LaunchResult{Receipt: &insights.Receipt{JobID: 77, Agent: "codex", Since: time.Now()}}, nil
}

var _ roborev.RepositoryRenameService = (*coordinationService)(nil)
var _ roborev.RepositoryMoveService = (*coordinationService)(nil)
var _ roborev.InsightsService = (*coordinationService)(nil)

func coordinationServer(t *testing.T, service *coordinationService, inventory repoinventory.Inventory, paths ...string) *Server {
	t.Helper()
	service.inventory = inventory
	registry := NewRegistry(fakeInspector{}, "")
	for _, path := range paths {
		if _, err := registry.Add(context.Background(), path); err != nil {
			t.Fatal(err)
		}
	}
	server, err := NewServer(service, registry, "")
	if err != nil {
		t.Fatal(err)
	}
	server.Panels = fakePanelResolver{catalog: testPanelCatalog()}
	server.repositoryMoveFS = moveAppAbsentFS{}
	return server
}

func coordinationServiceForTest() *coordinationService {
	return &coordinationService{moveHTTPService: &moveHTTPService{fakeService: &fakeService{}}}
}

func waitCoordination(t *testing.T, ch <-chan struct{}, name string) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(2 * time.Second):
		t.Fatalf("timed out waiting for %s", name)
	}
}

func waitCoordinationResponse(t *testing.T, ch <-chan *httptest.ResponseRecorder, name string) *httptest.ResponseRecorder {
	t.Helper()
	select {
	case response := <-ch:
		return response
	case <-time.After(2 * time.Second):
		t.Fatalf("timed out waiting for %s response", name)
		return nil
	}
}

func TestRepositoryMoveConfirmGateBusyOnSourceOrDestinationPreservesToken(t *testing.T) {
	const source = `E:\old\project`
	for _, key := range []string{source, `E:\new\project`} {
		t.Run(key, func(t *testing.T) {
			service := &moveHTTPService{fakeService: &fakeService{}}
			server := moveTestServer(t, service, repoinventory.Inventory{Repositories: []repoinventory.Repository{{Name: "old", RootPath: source}}})
			token := prepareMoveToken(t, server, source, `E:\new\project`)
			release, err := server.Gate.AcquireRepository(gitx.RepositoryKey(key))
			if err != nil {
				t.Fatal(err)
			}
			busy := movePost(t, server, "/api/repository-moves", `{"token":"`+token+`"}`)
			if busy.Code != http.StatusConflict || service.moveCalls != 0 {
				t.Fatalf("busy status=%d move=%d body=%s", busy.Code, service.moveCalls, busy.Body.String())
			}
			release()
			if response := movePost(t, server, "/api/repository-moves", `{"token":"`+token+`"}`); response.Code != http.StatusOK || service.moveCalls != 1 {
				t.Fatalf("retry status=%d move=%d body=%s", response.Code, service.moveCalls, response.Body.String())
			}
		})
	}
}

func TestRepositoryRenameConfirmGateBusyPreservesToken(t *testing.T) {
	service := &renameHTTPService{fakeService: &fakeService{}}
	server := renameTestServer(t, service, repoinventory.Inventory{Repositories: []repoinventory.Repository{{Name: "old", RootPath: `E:\tracked repo`}}})
	token := prepareRenameToken(t, server, `E:\tracked repo`, "new")
	release, err := server.Gate.AcquireRepository(gitx.RepositoryKey(`E:\tracked repo`))
	if err != nil {
		t.Fatal(err)
	}
	busy := confirmRename(t, server, token)
	if busy.Code != http.StatusConflict || service.renameCalls != 0 {
		t.Fatalf("busy status=%d rename=%d body=%s", busy.Code, service.renameCalls, busy.Body.String())
	}
	release()
	if response := confirmRename(t, server, token); response.Code != http.StatusOK || service.renameCalls != 1 {
		t.Fatalf("retry status=%d rename=%d body=%s", response.Code, service.renameCalls, response.Body.String())
	}
}

func TestRepositoryRenameBlocksMoveAndMoveBlocksRename(t *testing.T) {
	const source = `E:\shared\source`
	const destination = `E:\shared\destination`
	inventory := repoinventory.Inventory{Repositories: []repoinventory.Repository{{Name: "source", RootPath: source}}}

	t.Run("rename then move", func(t *testing.T) {
		allowRename := make(chan struct{})
		renameStarted := make(chan struct{}, 1)
		service := coordinationServiceForTest()
		service.renameBlock = allowRename
		service.renameStarted = renameStarted
		server := coordinationServer(t, service, inventory, destination)
		renameToken := prepareRenameToken(t, server, source, "repaired")
		moveToken := prepareMoveToken(t, server, source, destination)
		renameResponses := make(chan *httptest.ResponseRecorder, 1)
		go func() { renameResponses <- confirmRename(t, server, renameToken) }()
		waitCoordination(t, renameStarted, "rename process")
		busy := movePost(t, server, "/api/repository-moves", `{"token":"`+moveToken+`"}`)
		if busy.Code != http.StatusConflict || service.moveCalls != 0 {
			t.Fatalf("move while rename status=%d move=%d body=%s", busy.Code, service.moveCalls, busy.Body.String())
		}
		close(allowRename)
		if response := waitCoordinationResponse(t, renameResponses, "rename"); response.Code != http.StatusOK || service.renameCalls != 1 {
			t.Fatalf("rename status=%d calls=%d body=%s", response.Code, service.renameCalls, response.Body.String())
		}
		if response := movePost(t, server, "/api/repository-moves", `{"token":"`+moveToken+`"}`); response.Code != http.StatusOK || service.moveCalls != 1 {
			t.Fatalf("move retry status=%d calls=%d body=%s", response.Code, service.moveCalls, response.Body.String())
		}
	})

	t.Run("move then rename", func(t *testing.T) {
		allowMove := make(chan struct{})
		moveStarted := make(chan struct{}, 1)
		service := coordinationServiceForTest()
		service.moveBlock = allowMove
		service.moveStarted = moveStarted
		server := coordinationServer(t, service, inventory, destination)
		moveToken := prepareMoveToken(t, server, source, destination)
		renameToken := prepareRenameToken(t, server, source, "repaired")
		moveResponses := make(chan *httptest.ResponseRecorder, 1)
		go func() { moveResponses <- movePost(t, server, "/api/repository-moves", `{"token":"`+moveToken+`"}`) }()
		waitCoordination(t, moveStarted, "move process")
		busy := confirmRename(t, server, renameToken)
		if busy.Code != http.StatusConflict || service.renameCalls != 0 {
			t.Fatalf("rename while move status=%d rename=%d body=%s", busy.Code, service.renameCalls, busy.Body.String())
		}
		close(allowMove)
		if response := waitCoordinationResponse(t, moveResponses, "move"); response.Code != http.StatusOK || service.moveCalls != 1 {
			t.Fatalf("move status=%d calls=%d body=%s", response.Code, service.moveCalls, response.Body.String())
		}
		if response := confirmRename(t, server, renameToken); response.Code != http.StatusOK || service.renameCalls != 1 {
			t.Fatalf("rename retry status=%d calls=%d body=%s", response.Code, service.renameCalls, response.Body.String())
		}
	})
}

func TestRepositoryReviewBlocksMoveAndMoveBlocksReview(t *testing.T) {
	const source = `E:\stale\source`
	const destination = `E:\registered\destination`
	inventory := repoinventory.Inventory{Repositories: []repoinventory.Repository{{Name: "source", RootPath: source}}}
	reviewBody := `{"repoPath":"E:\\registered\\destination","panel":"dashboard_cross"}`

	t.Run("review then move", func(t *testing.T) {
		allowReview := make(chan struct{})
		reviewStarted := make(chan struct{}, 1)
		service := coordinationServiceForTest()
		service.reviewBlock = allowReview
		service.reviewStarted = reviewStarted
		server := coordinationServer(t, service, inventory, destination)
		moveToken := prepareMoveToken(t, server, source, destination)
		reviewResponses := make(chan *httptest.ResponseRecorder, 1)
		go func() { reviewResponses <- movePost(t, server, "/api/reviews", reviewBody) }()
		waitCoordination(t, reviewStarted, "review enqueue")
		busy := movePost(t, server, "/api/repository-moves", `{"token":"`+moveToken+`"}`)
		if busy.Code != http.StatusConflict || service.moveCalls != 0 {
			t.Fatalf("move while review status=%d move=%d body=%s", busy.Code, service.moveCalls, busy.Body.String())
		}
		close(allowReview)
		if response := waitCoordinationResponse(t, reviewResponses, "review"); response.Code != http.StatusCreated || service.reviewCalls != 1 {
			t.Fatalf("review status=%d calls=%d body=%s", response.Code, service.reviewCalls, response.Body.String())
		}
		if response := movePost(t, server, "/api/repository-moves", `{"token":"`+moveToken+`"}`); response.Code != http.StatusOK || service.moveCalls != 1 {
			t.Fatalf("move retry status=%d calls=%d body=%s", response.Code, service.moveCalls, response.Body.String())
		}
	})

	t.Run("move then review", func(t *testing.T) {
		allowMove := make(chan struct{})
		moveStarted := make(chan struct{}, 1)
		service := coordinationServiceForTest()
		service.moveBlock = allowMove
		service.moveStarted = moveStarted
		server := coordinationServer(t, service, inventory, destination)
		moveToken := prepareMoveToken(t, server, source, destination)
		moveResponses := make(chan *httptest.ResponseRecorder, 1)
		go func() { moveResponses <- movePost(t, server, "/api/repository-moves", `{"token":"`+moveToken+`"}`) }()
		waitCoordination(t, moveStarted, "move process")
		busy := movePost(t, server, "/api/reviews", reviewBody)
		if busy.Code != http.StatusConflict || service.reviewCalls != 0 {
			t.Fatalf("review while move status=%d calls=%d body=%s", busy.Code, service.reviewCalls, busy.Body.String())
		}
		close(allowMove)
		if response := waitCoordinationResponse(t, moveResponses, "move"); response.Code != http.StatusOK || service.moveCalls != 1 {
			t.Fatalf("move status=%d calls=%d body=%s", response.Code, service.moveCalls, response.Body.String())
		}
		if response := movePost(t, server, "/api/reviews", reviewBody); response.Code != http.StatusCreated || service.reviewCalls != 1 {
			t.Fatalf("review retry status=%d calls=%d body=%s", response.Code, service.reviewCalls, response.Body.String())
		}
	})
}

func prepareCoordinationInsightsToken(t *testing.T, server *Server, path string) string {
	t.Helper()
	identity, err := server.Registry.Identity(path)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := json.Marshal(map[string]any{"repositoryIdentity": identity, "window": "30d", "branchScope": "all"})
	request := httptest.NewRequest(http.MethodPost, "/api/insights/preview", strings.NewReader(string(body)))
	request.Host = "example.com"
	request.Header.Set("Origin", "http://example.com")
	request.Header.Set("X-CSRF-Token", server.csrfToken)
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("Insights preview status=%d body=%s", response.Code, response.Body.String())
	}
	var preview insights.Preview
	if err := json.Unmarshal(response.Body.Bytes(), &preview); err != nil || preview.Token == "" {
		t.Fatalf("Insights preview=%#v err=%v", preview, err)
	}
	return preview.Token
}

func confirmCoordinationInsights(t *testing.T, server *Server, token string) *httptest.ResponseRecorder {
	t.Helper()
	return movePost(t, server, "/api/insights", `{"token":"`+token+`"}`)
}

func TestRepositoryLeaseCoordinatesFixRefineAndUnrelatedRepository(t *testing.T) {
	const source = `E:\stale\source`
	const destination = `E:\registered\destination`
	service := coordinationServiceForTest()
	server := coordinationServer(t, service, repoinventory.Inventory{Repositories: []repoinventory.Repository{{Name: "source", RootPath: source}}}, destination)
	token := prepareMoveToken(t, server, source, destination)
	// The repository lease held by a long-lived Fix blocks Move at the same
	// shared resource boundary, while an unrelated checkout remains usable.
	fixRepositoryLease, err := server.Gate.AcquireRepository(gitx.RepositoryKey(destination))
	if err != nil {
		t.Fatal(err)
	}
	busy := movePost(t, server, "/api/repository-moves", `{"token":"`+token+`"}`)
	if busy.Code != http.StatusConflict || service.moveCalls != 0 {
		t.Fatalf("move with review lease status=%d move=%d body=%s", busy.Code, service.moveCalls, busy.Body.String())
	}
	if unrelated, err := server.Gate.AcquireRepository(gitx.RepositoryKey(`E:\unrelated`)); err != nil {
		t.Fatalf("unrelated repository was blocked: %v", err)
	} else {
		unrelated()
	}
	fixRepositoryLease()
	if response := movePost(t, server, "/api/repository-moves", `{"token":"`+token+`"}`); response.Code != http.StatusOK || service.moveCalls != 1 {
		t.Fatalf("move after review lease status=%d move=%d body=%s", response.Code, service.moveCalls, response.Body.String())
	}

	// A move's atomic pair blocks both the repository acquisition used by Fix
	// and the manager's Review/Refine lease path on its destination.
	releaseMove, err := server.Gate.AcquireRepositories(gitx.RepositoryKey(source), gitx.RepositoryKey(destination))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := server.Gate.AcquireRepository(gitx.RepositoryKey(destination)); err == nil {
		t.Fatal("Fix repository lease unexpectedly ignored active move repository lease")
	}
	if _, err := server.Refines.AcquireReviewLease(destination); err == nil {
		t.Fatal("review lease unexpectedly ignored active move repository lease")
	}
	if unrelated, err := server.Gate.AcquireRepository(gitx.RepositoryKey(`E:\another-unrelated`)); err != nil {
		t.Fatalf("unrelated repository was blocked by move pair: %v", err)
	} else {
		unrelated()
	}
	releaseMove()
}

func TestRepositoryMovePairLeasesSerializeSharedSourceAndDestination(t *testing.T) {
	const source = `E:\shared\source`
	const destinationA = `E:\destination\a`
	const destinationB = `E:\destination\b`
	service := &moveHTTPService{fakeService: &fakeService{}}
	registry := NewRegistry(fakeInspector{}, "")
	for _, path := range []string{destinationA, destinationB} {
		if _, err := registry.Add(context.Background(), path); err != nil {
			t.Fatal(err)
		}
	}
	server, err := NewServer(service, registry, "")
	if err != nil {
		t.Fatal(err)
	}
	server.repositoryMoveFS = moveAppAbsentFS{}
	service.inventory = repoinventory.Inventory{Repositories: []repoinventory.Repository{{Name: "source", RootPath: source}}}
	tokenA := prepareMoveToken(t, server, source, destinationA)
	tokenB := prepareMoveToken(t, server, source, destinationB)
	release, err := server.Gate.AcquireRepositories(gitx.RepositoryKey(source), gitx.RepositoryKey(destinationA))
	if err != nil {
		t.Fatal(err)
	}
	busy := movePost(t, server, "/api/repository-moves", `{"token":"`+tokenB+`"}`)
	if busy.Code != http.StatusConflict || service.moveCalls != 0 {
		t.Fatalf("shared source status=%d move=%d body=%s", busy.Code, service.moveCalls, busy.Body.String())
	}
	release()
	if response := movePost(t, server, "/api/repository-moves", `{"token":"`+tokenB+`"}`); response.Code != http.StatusOK || service.moveCalls != 1 {
		t.Fatalf("shared source retry status=%d move=%d body=%s", response.Code, service.moveCalls, response.Body.String())
	}
	if response := movePost(t, server, "/api/repository-moves", `{"token":"`+tokenA+`"}`); response.Code != http.StatusOK || service.moveCalls != 2 {
		t.Fatalf("shared source first token status=%d move=%d body=%s", response.Code, service.moveCalls, response.Body.String())
	}

	const sourceB = `E:\shared\source-b`
	service.inventory = repoinventory.Inventory{Repositories: []repoinventory.Repository{{Name: "source-a", RootPath: source}, {Name: "source-b", RootPath: sourceB}}}
	const sharedDestination = `E:\destination\shared`
	if _, err := registry.Add(context.Background(), sharedDestination); err != nil {
		t.Fatal(err)
	}
	tokenA = prepareMoveToken(t, server, source, sharedDestination)
	tokenB = prepareMoveToken(t, server, sourceB, sharedDestination)
	release, err = server.Gate.AcquireRepositories(gitx.RepositoryKey(source), gitx.RepositoryKey(sharedDestination))
	if err != nil {
		t.Fatal(err)
	}
	busy = movePost(t, server, "/api/repository-moves", `{"token":"`+tokenB+`"}`)
	if busy.Code != http.StatusConflict || service.moveCalls != 2 {
		t.Fatalf("shared destination status=%d move=%d body=%s", busy.Code, service.moveCalls, busy.Body.String())
	}
	release()
	if response := movePost(t, server, "/api/repository-moves", `{"token":"`+tokenB+`"}`); response.Code != http.StatusOK || service.moveCalls != 3 {
		t.Fatalf("shared destination retry status=%d move=%d body=%s", response.Code, service.moveCalls, response.Body.String())
	}
	if response := movePost(t, server, "/api/repository-moves", `{"token":"`+tokenA+`"}`); response.Code != http.StatusOK || service.moveCalls != 4 {
		t.Fatalf("shared destination first token status=%d move=%d body=%s", response.Code, service.moveCalls, response.Body.String())
	}
}

func TestRepositoryMoveConcurrentConfirmsSerializeSharedSourceAndDestination(t *testing.T) {
	const source = `E:\concurrent\source`
	const destinationA = `E:\concurrent\destination-a`
	const destinationB = `E:\concurrent\destination-b`

	t.Run("shared source", func(t *testing.T) {
		allowFirst := make(chan struct{})
		firstStarted := make(chan struct{}, 1)
		service := coordinationServiceForTest()
		service.moveBlock = allowFirst
		service.moveStarted = firstStarted
		server := coordinationServer(t, service, repoinventory.Inventory{Repositories: []repoinventory.Repository{{Name: "source", RootPath: source}}}, destinationA, destinationB)
		tokenA := prepareMoveToken(t, server, source, destinationA)
		tokenB := prepareMoveToken(t, server, source, destinationB)
		firstResponses := make(chan *httptest.ResponseRecorder, 1)
		go func() { firstResponses <- movePost(t, server, "/api/repository-moves", `{"token":"`+tokenA+`"}`) }()
		waitCoordination(t, firstStarted, "first shared-source move")
		busy := movePost(t, server, "/api/repository-moves", `{"token":"`+tokenB+`"}`)
		if busy.Code != http.StatusConflict || service.moveCalls != 0 {
			t.Fatalf("second shared-source status=%d move=%d body=%s", busy.Code, service.moveCalls, busy.Body.String())
		}
		close(allowFirst)
		if response := waitCoordinationResponse(t, firstResponses, "first shared-source move"); response.Code != http.StatusOK || service.moveCalls != 1 {
			t.Fatalf("first shared-source status=%d move=%d body=%s", response.Code, service.moveCalls, response.Body.String())
		}
		if response := movePost(t, server, "/api/repository-moves", `{"token":"`+tokenB+`"}`); response.Code != http.StatusOK || service.moveCalls != 2 {
			t.Fatalf("second shared-source retry status=%d move=%d body=%s", response.Code, service.moveCalls, response.Body.String())
		}
	})

	t.Run("shared destination", func(t *testing.T) {
		const sourceA = `E:\concurrent\source-a`
		const sourceB = `E:\concurrent\source-b`
		const destination = `E:\concurrent\destination`
		allowFirst := make(chan struct{})
		firstStarted := make(chan struct{}, 1)
		service := coordinationServiceForTest()
		service.moveBlock = allowFirst
		service.moveStarted = firstStarted
		server := coordinationServer(t, service, repoinventory.Inventory{Repositories: []repoinventory.Repository{{Name: "source-a", RootPath: sourceA}, {Name: "source-b", RootPath: sourceB}}}, destination)
		tokenA := prepareMoveToken(t, server, sourceA, destination)
		tokenB := prepareMoveToken(t, server, sourceB, destination)
		firstResponses := make(chan *httptest.ResponseRecorder, 1)
		go func() { firstResponses <- movePost(t, server, "/api/repository-moves", `{"token":"`+tokenA+`"}`) }()
		waitCoordination(t, firstStarted, "first shared-destination move")
		busy := movePost(t, server, "/api/repository-moves", `{"token":"`+tokenB+`"}`)
		if busy.Code != http.StatusConflict || service.moveCalls != 0 {
			t.Fatalf("second shared-destination status=%d move=%d body=%s", busy.Code, service.moveCalls, busy.Body.String())
		}
		close(allowFirst)
		if response := waitCoordinationResponse(t, firstResponses, "first shared-destination move"); response.Code != http.StatusOK || service.moveCalls != 1 {
			t.Fatalf("first shared-destination status=%d move=%d body=%s", response.Code, service.moveCalls, response.Body.String())
		}
		if response := movePost(t, server, "/api/repository-moves", `{"token":"`+tokenB+`"}`); response.Code != http.StatusOK || service.moveCalls != 2 {
			t.Fatalf("second shared-destination retry status=%d move=%d body=%s", response.Code, service.moveCalls, response.Body.String())
		}
	})
}

func TestInsightsConfirmGateBusyPreservesToken(t *testing.T) {
	const destination = `E:\registered\destination`
	service := coordinationServiceForTest()
	server := coordinationServer(t, service, repoinventory.Inventory{}, destination)
	token := prepareCoordinationInsightsToken(t, server, destination)
	identity, err := server.Registry.Identity(destination)
	if err != nil {
		t.Fatal(err)
	}
	release, err := server.Gate.AcquireRepository(identity)
	if err != nil {
		t.Fatal(err)
	}
	busy := confirmCoordinationInsights(t, server, token)
	if busy.Code != http.StatusConflict || service.insightsCalls != 0 {
		t.Fatalf("Insights busy status=%d calls=%d body=%s", busy.Code, service.insightsCalls, busy.Body.String())
	}
	release()
	if response := confirmCoordinationInsights(t, server, token); response.Code != http.StatusAccepted || service.insightsCalls != 1 {
		t.Fatalf("Insights retry status=%d calls=%d body=%s", response.Code, service.insightsCalls, response.Body.String())
	}
}

func TestInsightsBlocksMoveAndMoveBlocksInsights(t *testing.T) {
	const source = `E:\stale\source`
	const destination = `E:\registered\destination`
	inventory := repoinventory.Inventory{Repositories: []repoinventory.Repository{{Name: "source", RootPath: source}}}

	t.Run("Insights then move", func(t *testing.T) {
		allowInsights := make(chan struct{})
		insightsStarted := make(chan struct{}, 1)
		service := coordinationServiceForTest()
		service.insightsBlock = allowInsights
		service.insightsStarted = insightsStarted
		server := coordinationServer(t, service, inventory, destination)
		insightsToken := prepareCoordinationInsightsToken(t, server, destination)
		moveToken := prepareMoveToken(t, server, source, destination)
		insightsResponses := make(chan *httptest.ResponseRecorder, 1)
		go func() { insightsResponses <- confirmCoordinationInsights(t, server, insightsToken) }()
		waitCoordination(t, insightsStarted, "Insights launch")
		busy := movePost(t, server, "/api/repository-moves", `{"token":"`+moveToken+`"}`)
		if busy.Code != http.StatusConflict || service.moveCalls != 0 {
			t.Fatalf("move while Insights status=%d move=%d body=%s", busy.Code, service.moveCalls, busy.Body.String())
		}
		close(allowInsights)
		if response := waitCoordinationResponse(t, insightsResponses, "Insights"); response.Code != http.StatusAccepted || service.insightsCalls != 1 {
			t.Fatalf("Insights status=%d calls=%d body=%s", response.Code, service.insightsCalls, response.Body.String())
		}
		if response := movePost(t, server, "/api/repository-moves", `{"token":"`+moveToken+`"}`); response.Code != http.StatusOK || service.moveCalls != 1 {
			t.Fatalf("move retry status=%d calls=%d body=%s", response.Code, service.moveCalls, response.Body.String())
		}
	})

	t.Run("move then Insights", func(t *testing.T) {
		allowMove := make(chan struct{})
		moveStarted := make(chan struct{}, 1)
		service := coordinationServiceForTest()
		service.moveBlock = allowMove
		service.moveStarted = moveStarted
		server := coordinationServer(t, service, inventory, destination)
		moveToken := prepareMoveToken(t, server, source, destination)
		insightsToken := prepareCoordinationInsightsToken(t, server, destination)
		moveResponses := make(chan *httptest.ResponseRecorder, 1)
		go func() { moveResponses <- movePost(t, server, "/api/repository-moves", `{"token":"`+moveToken+`"}`) }()
		waitCoordination(t, moveStarted, "move process")
		busy := confirmCoordinationInsights(t, server, insightsToken)
		if busy.Code != http.StatusConflict || service.insightsCalls != 0 {
			t.Fatalf("Insights while move status=%d calls=%d body=%s", busy.Code, service.insightsCalls, busy.Body.String())
		}
		close(allowMove)
		if response := waitCoordinationResponse(t, moveResponses, "move"); response.Code != http.StatusOK || service.moveCalls != 1 {
			t.Fatalf("move status=%d calls=%d body=%s", response.Code, service.moveCalls, response.Body.String())
		}
		if response := confirmCoordinationInsights(t, server, insightsToken); response.Code != http.StatusAccepted || service.insightsCalls != 1 {
			t.Fatalf("Insights retry status=%d calls=%d body=%s", response.Code, service.insightsCalls, response.Body.String())
		}
	})
}
