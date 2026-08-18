// Package operation coordinates mutually exclusive Dashboard lifecycle work.
package operation

import (
	"errors"
	"sync"
)

var ErrBusy = errors.New("another Dashboard operation is active")

// Gate allows ordinary RoboRev mutations and Panel Refine leases to share a
// slot, while setup is exclusive. Repository-scoped conflicts remain owned by
// the Panel Refine manager. All acquisition is
// non-blocking: HTTP callers can report 409 immediately.
type Gate struct {
	mu        sync.Mutex
	setup     bool
	runtime   bool
	refine    int
	mutations int
}

func NewGate() *Gate { return &Gate{} }

func (g *Gate) AcquireSetup() (func(), error) {
	if g == nil {
		return nil, ErrBusy
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.setup || g.runtime || g.refine > 0 || g.mutations > 0 {
		return nil, ErrBusy
	}
	g.setup = true
	released := false
	return func() {
		g.mu.Lock()
		defer g.mu.Unlock()
		if !released {
			released = true
			g.setup = false
		}
	}, nil
}

func (g *Gate) AcquireMutation() (func(), error) {
	if g == nil {
		return nil, ErrBusy
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.setup || g.runtime {
		return nil, ErrBusy
	}
	g.mutations++
	released := false
	return func() {
		g.mu.Lock()
		defer g.mu.Unlock()
		if !released {
			released = true
			g.mutations--
		}
	}, nil
}

func (g *Gate) AcquireRefine() (func(), error) {
	if g == nil {
		return nil, ErrBusy
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.setup || g.runtime {
		return nil, ErrBusy
	}
	g.refine++
	released := false
	return func() {
		g.mu.Lock()
		defer g.mu.Unlock()
		if !released {
			released = true
			g.refine--
		}
	}, nil
}

// AcquireRuntimeControl reserves the single runtime-control slot. Lifecycle
// actions (daemon start/stop/restart) and queue pause/resume are mutually
// exclusive with setup, panel refine, ordinary mutations, and one another.
// Acquisition is deliberately non-blocking so HTTP callers can fail fast with
// a conflict response.
func (g *Gate) AcquireRuntimeControl() (func(), error) {
	if g == nil {
		return nil, ErrBusy
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.setup || g.runtime || g.refine > 0 || g.mutations > 0 {
		return nil, ErrBusy
	}
	g.runtime = true
	released := false
	return func() {
		g.mu.Lock()
		defer g.mu.Unlock()
		if !released {
			released = true
			g.runtime = false
		}
	}, nil
}

func (g *Gate) Snapshot() (setup bool, refine, mutations int) {
	if g == nil {
		return false, 0, 0
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.setup, g.refine, g.mutations
}

// DetailedSnapshot exposes the runtime lease without changing Snapshot's
// historical three-value return signature.
type DetailedSnapshot struct {
	Setup          bool
	RuntimeControl bool
	Refine         int
	Mutations      int
}

func (g *Gate) SnapshotDetailed() DetailedSnapshot {
	if g == nil {
		return DetailedSnapshot{}
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	return DetailedSnapshot{Setup: g.setup, RuntimeControl: g.runtime, Refine: g.refine, Mutations: g.mutations}
}
