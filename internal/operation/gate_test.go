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
