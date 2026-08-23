package app

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/froze/roborev-dashboard/internal/gitx"
	"github.com/froze/roborev-dashboard/internal/repoinventory"
)

func prepareCoordinationMergeToken(t *testing.T, server *Server, source, target string) string {
	t.Helper()
	identity, err := server.Registry.Identity(target)
	if err != nil {
		t.Fatal(err)
	}
	response := mergePost(t, server, "/api/repository-merges/preview", mergePreviewBody(source, identity))
	if response.Code != http.StatusOK {
		t.Fatalf("merge preview status=%d body=%s", response.Code, response.Body.String())
	}
	var dto repositoryMergePreviewDTO
	if err := json.Unmarshal(response.Body.Bytes(), &dto); err != nil || dto.Token == "" {
		t.Fatalf("merge preview dto=%#v err=%v", dto, err)
	}
	return dto.Token
}

func coordinationMergeInventory(paths ...string) repoinventory.Inventory {
	rows := make([]repoinventory.Repository, 0, len(paths))
	for index, path := range paths {
		rows = append(rows, repoinventory.Repository{Name: "repo-" + string(rune('A'+index)), RootPath: path, Count: 7})
	}
	return repoinventory.Inventory{Repositories: rows, TotalCount: int64(len(rows) * 7)}
}

func confirmCoordinationMerge(t *testing.T, server *Server, token string) *httptest.ResponseRecorder {
	t.Helper()
	return mergePost(t, server, "/api/repository-merges", `{"token":"`+token+`"}`)
}

func coordinationReviewBody(t *testing.T, path string) string {
	t.Helper()
	body, err := json.Marshal(map[string]string{"repoPath": path, "panel": "dashboard_cross"})
	if err != nil {
		t.Fatal(err)
	}
	return string(body)
}

func TestRepositoryMergeBlocksRenameMoveReviewAndUnrelatedRepository(t *testing.T) {
	const source = `E:\merge\source`
	const target = `E:\merge\target`
	inventory := coordinationMergeInventory(source)

	t.Run("rename on source", func(t *testing.T) {
		service := coordinationServiceForTest()
		allowRename := make(chan struct{})
		renameStarted := make(chan struct{}, 1)
		service.renameBlock = allowRename
		service.renameStarted = renameStarted
		server := coordinationServer(t, service, inventory, target)
		mergeToken := prepareCoordinationMergeToken(t, server, source, target)
		renameToken := prepareRenameToken(t, server, source, "source-renamed")
		renameResponses := make(chan *httptest.ResponseRecorder, 1)
		go func() { renameResponses <- confirmRename(t, server, renameToken) }()
		waitCoordination(t, renameStarted, "source rename")
		busy := confirmCoordinationMerge(t, server, mergeToken)
		if busy.Code != http.StatusConflict || service.mergeCalls != 0 {
			t.Fatalf("merge while source rename lease status=%d calls=%d body=%s", busy.Code, service.mergeCalls, busy.Body.String())
		}
		close(allowRename)
		if response := waitCoordinationResponse(t, renameResponses, "source rename"); response.Code != http.StatusOK {
			t.Fatalf("rename status=%d body=%s", response.Code, response.Body.String())
		}
		if response := confirmCoordinationMerge(t, server, mergeToken); response.Code != http.StatusOK || service.mergeCalls != 1 {
			t.Fatalf("merge retry status=%d calls=%d body=%s", response.Code, service.mergeCalls, response.Body.String())
		}
	})

	t.Run("move on source", func(t *testing.T) {
		service := coordinationServiceForTest()
		allowMove := make(chan struct{})
		moveStarted := make(chan struct{}, 1)
		service.moveBlock = allowMove
		service.moveStarted = moveStarted
		server := coordinationServer(t, service, inventory, target)
		mergeToken := prepareCoordinationMergeToken(t, server, source, target)
		moveToken := prepareMoveToken(t, server, source, target)
		moveResponses := make(chan *httptest.ResponseRecorder, 1)
		go func() { moveResponses <- movePost(t, server, "/api/repository-moves", `{"token":"`+moveToken+`"}`) }()
		waitCoordination(t, moveStarted, "source move")
		busy := confirmCoordinationMerge(t, server, mergeToken)
		if busy.Code != http.StatusConflict || service.mergeCalls != 0 {
			t.Fatalf("merge while source move lease status=%d calls=%d body=%s", busy.Code, service.mergeCalls, busy.Body.String())
		}
		close(allowMove)
		if response := waitCoordinationResponse(t, moveResponses, "source move"); response.Code != http.StatusOK {
			t.Fatalf("move status=%d body=%s", response.Code, response.Body.String())
		}
		if response := confirmCoordinationMerge(t, server, mergeToken); response.Code != http.StatusOK || service.mergeCalls != 1 {
			t.Fatalf("merge retry status=%d calls=%d body=%s", response.Code, service.mergeCalls, response.Body.String())
		}
	})

	t.Run("review on target", func(t *testing.T) {
		service := coordinationServiceForTest()
		allowReview := make(chan struct{})
		reviewStarted := make(chan struct{}, 1)
		service.reviewBlock = allowReview
		service.reviewStarted = reviewStarted
		server := coordinationServer(t, service, inventory, target)
		mergeToken := prepareCoordinationMergeToken(t, server, source, target)
		reviewResponses := make(chan *httptest.ResponseRecorder, 1)
		go func() {
			reviewResponses <- movePost(t, server, "/api/reviews", coordinationReviewBody(t, target))
		}()
		waitCoordination(t, reviewStarted, "target review")
		busy := confirmCoordinationMerge(t, server, mergeToken)
		close(allowReview)
		if busy.Code != http.StatusConflict || service.mergeCalls != 0 {
			t.Fatalf("merge while target review status=%d calls=%d body=%s", busy.Code, service.mergeCalls, busy.Body.String())
		}
		if response := waitCoordinationResponse(t, reviewResponses, "target review"); response.Code != http.StatusCreated {
			t.Fatalf("review status=%d body=%s", response.Code, response.Body.String())
		}
		if response := confirmCoordinationMerge(t, server, mergeToken); response.Code != http.StatusOK || service.mergeCalls != 1 {
			t.Fatalf("merge retry status=%d calls=%d body=%s", response.Code, service.mergeCalls, response.Body.String())
		}
	})

	t.Run("move on target", func(t *testing.T) {
		const otherSource = `E:\merge\other-source`
		service := coordinationServiceForTest()
		allowMove := make(chan struct{})
		moveStarted := make(chan struct{}, 1)
		service.moveBlock = allowMove
		service.moveStarted = moveStarted
		server := coordinationServer(t, service, coordinationMergeInventory(source, otherSource), target)
		mergeToken := prepareCoordinationMergeToken(t, server, otherSource, target)
		moveToken := prepareMoveToken(t, server, source, target)
		moveResponses := make(chan *httptest.ResponseRecorder, 1)
		go func() { moveResponses <- movePost(t, server, "/api/repository-moves", `{"token":"`+moveToken+`"}`) }()
		waitCoordination(t, moveStarted, "target move")
		busy := confirmCoordinationMerge(t, server, mergeToken)
		if busy.Code != http.StatusConflict || service.mergeCalls != 0 {
			t.Fatalf("merge while target move lease status=%d calls=%d body=%s", busy.Code, service.mergeCalls, busy.Body.String())
		}
		close(allowMove)
		if response := waitCoordinationResponse(t, moveResponses, "target move"); response.Code != http.StatusOK {
			t.Fatalf("move status=%d body=%s", response.Code, response.Body.String())
		}
		if response := confirmCoordinationMerge(t, server, mergeToken); response.Code != http.StatusOK || service.mergeCalls != 1 {
			t.Fatalf("merge retry status=%d calls=%d body=%s", response.Code, service.mergeCalls, response.Body.String())
		}
	})

	// Repository keys outside the merge pair remain independently usable.
	service := coordinationServiceForTest()
	server := coordinationServer(t, service, inventory, target)
	mergeToken := prepareCoordinationMergeToken(t, server, source, target)
	release, err := server.Gate.AcquireRepositories(gitx.RepositoryKey(source), gitx.RepositoryKey(target))
	if err != nil {
		t.Fatal(err)
	}
	if unrelated, err := server.Gate.AcquireRepository(gitx.RepositoryKey(`E:\merge\unrelated`)); err != nil {
		t.Fatalf("unrelated repository blocked: %v", err)
	} else {
		unrelated()
	}
	busy := confirmCoordinationMerge(t, server, mergeToken)
	release()
	if busy.Code != http.StatusConflict || service.mergeCalls != 0 {
		t.Fatalf("pair lease did not block merge: %d %s", busy.Code, busy.Body.String())
	}
}

func TestRepositoryMergeBlocksReviewWhileMergeTargetLeaseIsActive(t *testing.T) {
	const source = `E:\merge\source-review`
	const target = `E:\merge\target-review`
	service := coordinationServiceForTest()
	allowMerge := make(chan struct{})
	mergeStarted := make(chan struct{}, 1)
	service.mergeBlock = allowMerge
	service.mergeStarted = mergeStarted
	server := coordinationServer(t, service, coordinationMergeInventory(source), target)
	mergeToken := prepareCoordinationMergeToken(t, server, source, target)
	mergeResponses := make(chan *httptest.ResponseRecorder, 1)
	go func() { mergeResponses <- confirmCoordinationMerge(t, server, mergeToken) }()
	waitCoordination(t, mergeStarted, "merge process")
	review := movePost(t, server, "/api/reviews", coordinationReviewBody(t, target))
	if review.Code != http.StatusConflict || service.reviewCalls != 0 {
		t.Fatalf("review while merge status=%d calls=%d body=%s", review.Code, service.reviewCalls, review.Body.String())
	}
	close(allowMerge)
	if response := waitCoordinationResponse(t, mergeResponses, "merge"); response.Code != http.StatusOK || service.mergeCalls != 1 {
		t.Fatalf("merge status=%d calls=%d body=%s", response.Code, service.mergeCalls, response.Body.String())
	}
	if response := movePost(t, server, "/api/reviews", coordinationReviewBody(t, target)); response.Code != http.StatusCreated {
		t.Fatalf("review retry status=%d body=%s", response.Code, response.Body.String())
	}
}

func TestRepositoryMergeRespectsFixAndRefineTargetLeases(t *testing.T) {
	const source = `E:\merge\source-leases`
	const target = `E:\merge\target-leases`
	for _, tc := range []struct {
		name string
		lock func(*Server) (func(), error)
	}{
		{name: "fix repository lease", lock: func(server *Server) (func(), error) {
			return server.Gate.AcquireRepository(gitx.RepositoryKey(target))
		}},
		{name: "panel refine lease", lock: func(server *Server) (func(), error) {
			return server.Refines.AcquireReviewLease(target)
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			service := coordinationServiceForTest()
			server := coordinationServer(t, service, coordinationMergeInventory(source), target)
			token := prepareCoordinationMergeToken(t, server, source, target)
			release, err := tc.lock(server)
			if err != nil {
				t.Fatal(err)
			}
			busy := confirmCoordinationMerge(t, server, token)
			release()
			if busy.Code != http.StatusConflict || service.mergeCalls != 0 {
				t.Fatalf("busy status=%d calls=%d body=%s", busy.Code, service.mergeCalls, busy.Body.String())
			}
			if response := confirmCoordinationMerge(t, server, token); response.Code != http.StatusOK || service.mergeCalls != 1 {
				t.Fatalf("retry status=%d calls=%d body=%s", response.Code, service.mergeCalls, response.Body.String())
			}
		})
	}
}

func TestRepositoryMergePairLeasesSerializeSourceAndTargetAlternatives(t *testing.T) {
	const sourceA = `E:\merge\source-a`
	const sourceC = `E:\merge\source-c`
	const targetB = `E:\merge\target-b`
	const targetD = `E:\merge\target-d`

	t.Run("shared source", func(t *testing.T) {
		service := coordinationServiceForTest()
		server := coordinationServer(t, service, coordinationMergeInventory(sourceA, sourceC), targetB, targetD)
		tokenAB := prepareCoordinationMergeToken(t, server, sourceA, targetB)
		tokenAD := prepareCoordinationMergeToken(t, server, sourceA, targetD)
		release, err := server.Gate.AcquireRepositories(gitx.RepositoryKey(sourceA), gitx.RepositoryKey(targetB))
		if err != nil {
			t.Fatal(err)
		}
		if response := confirmCoordinationMerge(t, server, tokenAD); response.Code != http.StatusConflict || service.mergeCalls != 0 {
			t.Fatalf("shared source status=%d calls=%d body=%s", response.Code, service.mergeCalls, response.Body.String())
		}
		release()
		if response := confirmCoordinationMerge(t, server, tokenAD); response.Code != http.StatusOK || service.mergeCalls != 1 {
			t.Fatalf("shared source retry status=%d calls=%d body=%s", response.Code, service.mergeCalls, response.Body.String())
		}
		if response := confirmCoordinationMerge(t, server, tokenAB); response.Code != http.StatusOK || service.mergeCalls != 2 {
			t.Fatalf("A->B status=%d calls=%d body=%s", response.Code, service.mergeCalls, response.Body.String())
		}
	})

	t.Run("shared target", func(t *testing.T) {
		service := coordinationServiceForTest()
		server := coordinationServer(t, service, coordinationMergeInventory(sourceA, sourceC), targetB)
		tokenAB := prepareCoordinationMergeToken(t, server, sourceA, targetB)
		tokenCB := prepareCoordinationMergeToken(t, server, sourceC, targetB)
		release, err := server.Gate.AcquireRepositories(gitx.RepositoryKey(sourceA), gitx.RepositoryKey(targetB))
		if err != nil {
			t.Fatal(err)
		}
		if response := confirmCoordinationMerge(t, server, tokenCB); response.Code != http.StatusConflict || service.mergeCalls != 0 {
			t.Fatalf("shared target status=%d calls=%d body=%s", response.Code, service.mergeCalls, response.Body.String())
		}
		release()
		if response := confirmCoordinationMerge(t, server, tokenCB); response.Code != http.StatusOK || service.mergeCalls != 1 {
			t.Fatalf("C->B status=%d calls=%d body=%s", response.Code, service.mergeCalls, response.Body.String())
		}
		if response := confirmCoordinationMerge(t, server, tokenAB); response.Code != http.StatusOK || service.mergeCalls != 2 {
			t.Fatalf("A->B status=%d calls=%d body=%s", response.Code, service.mergeCalls, response.Body.String())
		}
	})
}
