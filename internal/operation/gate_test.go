package operation

import (
	"errors"
	"testing"
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

func TestTaskLeaseConflictsBidirectionallyWithEveryOperation(t *testing.T) {
	g := NewGate()
	release, err := g.AcquireTaskRepository("repo-a")
	if err != nil {
		t.Fatalf("AcquireTaskRepository: %v", err)
	}
	defer release()
	if got := g.SnapshotDetailed(); got.Tasks != 1 || got.RepositoryLeases != 1 {
		t.Fatalf("task snapshot = %#v", got)
	}
	if _, err := g.AcquireTask(); !errors.Is(err, ErrBusy) {
		t.Fatalf("second task = %v", err)
	}
	if _, err := g.AcquireRepository("repo-b"); !errors.Is(err, ErrBusy) {
		t.Fatalf("repository during task = %v", err)
	}
	if _, err := g.AcquireMutation(); !errors.Is(err, ErrBusy) {
		t.Fatalf("mutation during task = %v", err)
	}
	if _, err := g.AcquireRefine(); !errors.Is(err, ErrBusy) {
		t.Fatalf("refine during task = %v", err)
	}
	if _, err := g.AcquireFix(); !errors.Is(err, ErrBusy) {
		t.Fatalf("fix during task = %v", err)
	}
	if _, err := g.AcquireSetup(); !errors.Is(err, ErrBusy) {
		t.Fatalf("setup during task = %v", err)
	}
	if _, err := g.AcquireRuntimeControl(); !errors.Is(err, ErrBusy) {
		t.Fatalf("runtime during task = %v", err)
	}
	release()
	if got := g.SnapshotDetailed(); got.Tasks != 0 || got.RepositoryLeases != 0 {
		t.Fatalf("task release snapshot = %#v", got)
	}
}

func TestTaskGlobalLeaseRejectsRepositoryLease(t *testing.T) {
	g := NewGate()
	release, err := g.AcquireTask()
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	if _, err := g.AcquireTaskRepository("repo-a"); !errors.Is(err, ErrBusy) {
		t.Fatalf("task repository while task global = %v", err)
	}
}
