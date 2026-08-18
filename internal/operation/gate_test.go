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
