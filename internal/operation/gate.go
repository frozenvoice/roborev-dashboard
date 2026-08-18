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
	if g.setup || g.refine > 0 || g.mutations > 0 {
		return nil, ErrBusy
	}
	g.setup = true
	return g.releaseSetup, nil
}

func (g *Gate) releaseSetup() {
	g.mu.Lock()
	g.setup = false
	g.mu.Unlock()
}

func (g *Gate) AcquireMutation() (func(), error) {
	if g == nil {
		return nil, ErrBusy
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.setup {
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
	if g.setup {
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

func (g *Gate) Snapshot() (setup bool, refine, mutations int) {
	if g == nil {
		return false, 0, 0
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.setup, g.refine, g.mutations
}
