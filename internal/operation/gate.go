// Package operation coordinates mutually exclusive Dashboard lifecycle work.
package operation

import (
	"errors"
	"sync"
)

var ErrBusy = errors.New("another Dashboard operation is active")

// Gate coordinates ordinary RoboRev mutations, Panel Refine, and agentic Fix
// leases. Repository-scoped conflicts use the same nonblocking gate so every
// mutator observes the same checkout ownership. All acquisition is
// non-blocking: HTTP callers can report 409 immediately.
type Gate struct {
	mu                 sync.Mutex
	setup              bool
	runtime            bool
	refine             int
	mutations          int
	fixes              int
	repositories       map[string]uint64
	repositorySequence uint64
}

func NewGate() *Gate { return &Gate{repositories: make(map[string]uint64)} }

func (g *Gate) AcquireSetup() (func(), error) {
	if g == nil {
		return nil, ErrBusy
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.setup || g.runtime || g.refine > 0 || g.mutations > 0 || g.fixes > 0 {
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
	if g.setup || g.runtime || g.fixes > 0 {
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
	if g.setup || g.runtime || g.fixes > 0 {
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

// AcquireFix reserves the agentic-fix operation slot. Fixes are process-scoped
// mutations and therefore cannot overlap setup, runtime control, or ordinary
// HTTP mutations and Panel Refine. Repository leases (AcquireRepository)
// serialize short-lived review mutations with long-lived work on one checkout.
func (g *Gate) AcquireFix() (func(), error) {
	if g == nil {
		return nil, ErrBusy
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.setup || g.runtime || g.refine > 0 || g.mutations > 0 || g.fixes > 0 {
		return nil, ErrBusy
	}
	g.fixes++
	released := false
	return func() {
		g.mu.Lock()
		defer g.mu.Unlock()
		if !released {
			released = true
			g.fixes--
		}
	}, nil
}

// AcquireRepository reserves a canonical repository identity without waiting.
// Every long-lived repository mutator shares this map so stale UI requests
// fail with a conflict instead of racing a currently running operation.
func (g *Gate) AcquireRepository(identity string) (func(), error) {
	if g == nil || identity == "" {
		return nil, ErrBusy
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.repositories == nil {
		g.repositories = make(map[string]uint64)
	}
	if g.repositories[identity] != 0 {
		return nil, ErrBusy
	}
	g.repositorySequence++
	owner := g.repositorySequence
	g.repositories[identity] = owner
	released := false
	return func() {
		g.mu.Lock()
		defer g.mu.Unlock()
		if !released {
			released = true
			if g.repositories[identity] == owner {
				delete(g.repositories, identity)
			}
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
	if g.setup || g.runtime || g.refine > 0 || g.mutations > 0 || g.fixes > 0 {
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
	Setup            bool
	RuntimeControl   bool
	Refine           int
	Mutations        int
	Fixes            int
	RepositoryLeases int
}

func (g *Gate) SnapshotDetailed() DetailedSnapshot {
	if g == nil {
		return DetailedSnapshot{}
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	return DetailedSnapshot{Setup: g.setup, RuntimeControl: g.runtime, Refine: g.refine, Mutations: g.mutations, Fixes: g.fixes, RepositoryLeases: len(g.repositories)}
}
