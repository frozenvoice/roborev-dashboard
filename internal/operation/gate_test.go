package operation

import (
	"errors"
	"testing"
	"time"
)

func TestSetupIsExclusiveAndMutationIsNonblocking(t *testing.T) {
	gate := NewGate()
	release, err := gate.AcquireSetup()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := gate.AcquireMutation(); !errors.Is(err, ErrBusy) {
		t.Fatalf("mutation during setup = %v", err)
	}
	if _, err := gate.AcquireRefine(); !errors.Is(err, ErrBusy) {
		t.Fatalf("refine during setup = %v", err)
	}
	release()
	mutation, err := gate.AcquireMutation()
	if err != nil {
		t.Fatal(err)
	}
	refine, err := gate.AcquireRefine()
	if err != nil {
		t.Fatal(err)
	}
	mutation()
	refine()
	if setup, refines, mutations := gate.Snapshot(); setup || refines != 0 || mutations != 0 {
		t.Fatalf("snapshot = %v,%d,%d", setup, refines, mutations)
	}
}

func TestReleaseIsIdempotent(t *testing.T) {
	gate := NewGate()
	release, err := gate.AcquireMutation()
	if err != nil {
		t.Fatal(err)
	}
	release()
	release()
	if _, _, mutations := gate.Snapshot(); mutations != 0 {
		t.Fatal(mutations)
	}
}

func TestRuntimeControlIsExclusiveWithEveryOperation(t *testing.T) {
	g := NewGate()
	release, err := g.AcquireRuntimeControl()
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	if _, err := g.AcquireSetup(); !errors.Is(err, ErrBusy) {
		t.Fatalf("setup during runtime = %v", err)
	}
	if _, err := g.AcquireRefine(); !errors.Is(err, ErrBusy) {
		t.Fatalf("refine during runtime = %v", err)
	}
	if _, err := g.AcquireMutation(); !errors.Is(err, ErrBusy) {
		t.Fatalf("mutation during runtime = %v", err)
	}
	if got := g.SnapshotDetailed(); !got.RuntimeControl || got.Setup || got.Refine != 0 || got.Mutations != 0 {
		t.Fatalf("detailed snapshot = %#v", got)
	}
	release()
	if got := g.SnapshotDetailed(); got.RuntimeControl {
		t.Fatal("runtime lease was not released")
	}
}

func TestSetupAndRuntimeRejectEachOther(t *testing.T) {
	g := NewGate()
	releaseSetup, err := g.AcquireSetup()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := g.AcquireRuntimeControl(); !errors.Is(err, ErrBusy) {
		t.Fatalf("runtime during setup = %v", err)
	}
	releaseSetup()
	releaseRuntime, err := g.AcquireRuntimeControl()
	if err != nil {
		t.Fatal(err)
	}
	releaseRuntime()
}

func TestFixLeaseConflictsWithOrdinaryAndRepositoryLeases(t *testing.T) {
	g := NewGate()
	releaseFix, err := g.AcquireFix()
	if err != nil {
		t.Fatalf("AcquireFix: %v", err)
	}
	defer releaseFix()
	if _, err := g.AcquireMutation(); !errors.Is(err, ErrBusy) {
		t.Fatalf("ordinary mutation during fix = %v", err)
	}
	if _, err := g.AcquireRefine(); !errors.Is(err, ErrBusy) {
		t.Fatalf("refine during fix = %v", err)
	}
	releaseRepo, err := g.AcquireRepository("repo-a")
	if err != nil {
		t.Fatalf("AcquireRepository: %v", err)
	}
	if _, err := g.AcquireRepository("repo-a"); !errors.Is(err, ErrBusy) {
		t.Fatalf("same repository lease = %v", err)
	}
	releaseRepo()
	releaseAgain, err := g.AcquireRepository("repo-a")
	if err != nil {
		t.Fatalf("repository lease after release = %v", err)
	}
	// A stale once-safe release must not drop a newer owner.
	releaseRepo()
	if _, err := g.AcquireRepository("repo-a"); !errors.Is(err, ErrBusy) {
		t.Fatalf("stale release removed newer owner: %v", err)
	}
	releaseAgain()
}

func TestFixLeaseIsGloballyBidirectionalWithSetupAndRuntime(t *testing.T) {
	g := NewGate()
	releaseFix, err := g.AcquireFix()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := g.AcquireSetup(); !errors.Is(err, ErrBusy) {
		t.Fatalf("setup during Fix = %v", err)
	}
	if _, err := g.AcquireRuntimeControl(); !errors.Is(err, ErrBusy) {
		t.Fatalf("runtime during Fix = %v", err)
	}
	releaseFix()

	releaseSetup, err := g.AcquireSetup()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := g.AcquireFix(); !errors.Is(err, ErrBusy) {
		t.Fatalf("Fix during setup = %v", err)
	}
	releaseSetup()

	releaseRuntime, err := g.AcquireRuntimeControl()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := g.AcquireFix(); !errors.Is(err, ErrBusy) {
		t.Fatalf("Fix during runtime = %v", err)
	}
	releaseRuntime()
}

func TestAcquireRepositoriesReservesAllKeysAtomically(t *testing.T) {
	g := NewGate()
	release, err := g.AcquireRepositories("repo-a", "repo-b")
	if err != nil {
		t.Fatalf("AcquireRepositories: %v", err)
	}
	if got := g.SnapshotDetailed(); got.RepositoryLeases != 2 {
		t.Fatalf("repository leases = %d, want 2", got.RepositoryLeases)
	}
	if _, err := g.AcquireRepository("repo-a"); !errors.Is(err, ErrBusy) {
		t.Fatalf("single lease for A while multi lease is active = %v", err)
	}
	if _, err := g.AcquireRepository("repo-b"); !errors.Is(err, ErrBusy) {
		t.Fatalf("single lease for B while multi lease is active = %v", err)
	}
	unrelated, err := g.AcquireRepository("repo-c")
	if err != nil {
		t.Fatalf("unrelated repository lease = %v", err)
	}
	unrelated()
	release()
	release()
	if got := g.SnapshotDetailed(); got.RepositoryLeases != 0 {
		t.Fatalf("repository leases after release = %d, want 0", got.RepositoryLeases)
	}
	for _, key := range []string{"repo-a", "repo-b", "repo-c"} {
		lease, err := g.AcquireRepository(key)
		if err != nil {
			t.Fatalf("%s after release: %v", key, err)
		}
		lease()
	}
}

func TestAcquireRepositoriesBusyKeyDoesNotLeakOtherKeys(t *testing.T) {
	testCases := []struct {
		name string
		busy string
		free string
	}{
		{name: "A busy", busy: "repo-a", free: "repo-b"},
		{name: "B busy", busy: "repo-b", free: "repo-a"},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			g := NewGate()
			busyLease, err := g.AcquireRepository(tc.busy)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := g.AcquireRepositories("repo-a", "repo-b"); !errors.Is(err, ErrBusy) {
				t.Fatalf("multi lease with %s busy = %v", tc.busy, err)
			}
			if got := g.SnapshotDetailed(); got.RepositoryLeases != 1 {
				t.Fatalf("repository leases after failed multi lease = %d, want 1", got.RepositoryLeases)
			}
			freeLease, err := g.AcquireRepository(tc.free)
			if err != nil {
				t.Fatalf("free key was leaked by failed multi lease: %v", err)
			}
			freeLease()
			busyLease()
		})
	}
}

func TestAcquireRepositoriesDeduplicatesKeys(t *testing.T) {
	g := NewGate()
	release, err := g.AcquireRepositories("repo-a", "repo-a", "repo-b", "repo-a")
	if err != nil {
		t.Fatalf("AcquireRepositories with duplicates: %v", err)
	}
	if got := g.SnapshotDetailed(); got.RepositoryLeases != 2 {
		t.Fatalf("repository leases = %d, want 2 after deduplication", got.RepositoryLeases)
	}
	release()
	if got := g.SnapshotDetailed(); got.RepositoryLeases != 0 {
		t.Fatalf("repository leases after release = %d, want 0", got.RepositoryLeases)
	}
}

func TestAcquireRepositoriesRejectsEmptyAndBlankKeysWithoutLeasing(t *testing.T) {
	for _, keys := range [][]string{nil, {}, {""}, {"\t"}, {"repo-a", "  "}} {
		g := NewGate()
		if _, err := g.AcquireRepositories(keys...); !errors.Is(err, ErrBusy) {
			t.Fatalf("keys %#v error = %v, want ErrBusy", keys, err)
		}
		if got := g.SnapshotDetailed(); got.RepositoryLeases != 0 {
			t.Fatalf("keys %#v left %d repository leases", keys, got.RepositoryLeases)
		}
	}
	var nilGate *Gate
	if _, err := nilGate.AcquireRepositories("repo-a"); !errors.Is(err, ErrBusy) {
		t.Fatalf("nil gate error = %v, want ErrBusy", err)
	}
}

func TestAcquireRepositoriesOppositeOrdersAreNonblocking(t *testing.T) {
	g := NewGate()
	results := make(chan struct {
		release func()
		err     error
	}, 2)
	go func() {
		release, err := g.AcquireRepositories("repo-a", "repo-b")
		results <- struct {
			release func()
			err     error
		}{release: release, err: err}
	}()
	go func() {
		release, err := g.AcquireRepositories("repo-b", "repo-a")
		results <- struct {
			release func()
			err     error
		}{release: release, err: err}
	}()

	var successes int
	var leases []func()
	for range 2 {
		select {
		case result := <-results:
			if result.err == nil {
				successes++
				leases = append(leases, result.release)
			} else if !errors.Is(result.err, ErrBusy) {
				t.Fatalf("opposite-order acquisition error = %v", result.err)
			}
		case <-time.After(time.Second):
			t.Fatal("opposite-order acquisition did not return")
		}
	}
	for _, release := range leases {
		release()
	}
	if successes != 1 {
		t.Fatalf("opposite-order successes = %d, want 1", successes)
	}
	if got := g.SnapshotDetailed(); got.RepositoryLeases != 0 {
		t.Fatalf("repository leases after opposite-order test = %d, want 0", got.RepositoryLeases)
	}
}
