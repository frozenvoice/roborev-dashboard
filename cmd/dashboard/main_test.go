package main

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"

	"github.com/froze/roborev-dashboard/internal/gitx"
)

func TestWriteVersionDoesNotRequireRuntimeServices(t *testing.T) {
	var output bytes.Buffer
	if err := writeVersion(&output, false); err != nil {
		t.Fatalf("writeVersion() error = %v", err)
	}
	if got := output.String(); !strings.HasPrefix(got, "roborev-dashboard dev\n") || !strings.Contains(got, "RoboRev compatibility v0.64.0\n") {
		t.Fatalf("version output = %q", got)
	}

	output.Reset()
	if err := writeVersion(&output, true); err != nil {
		t.Fatalf("writeVersion(json) error = %v", err)
	}
	if got := output.String(); !strings.HasPrefix(got, `{"name":"roborev-dashboard","version":"dev"`) || !strings.HasSuffix(got, "\n") {
		t.Fatalf("version JSON output = %q", got)
	}
}

type blockingStartupRegistry struct {
	loadContext context.Context
	addContext  context.Context
	addPath     string
	addExpired  bool
}

func (r *blockingStartupRegistry) Load(ctx context.Context) {
	r.loadContext = ctx
	<-ctx.Done()
}

func (r *blockingStartupRegistry) Add(ctx context.Context, path string) (gitx.Repository, error) {
	r.addContext = ctx
	r.addPath = path
	r.addExpired = ctx.Err() != nil
	return gitx.Repository{}, nil
}

func TestInitializeRegistryUsesFreshContextForAdd(t *testing.T) {
	registry := &blockingStartupRegistry{}
	const path = `C:\workspaces\slow saved repository`

	if err := initializeRegistry(registry, path, 50*time.Millisecond); err != nil {
		t.Fatalf("initializeRegistry() error = %v", err)
	}
	if registry.addExpired {
		t.Fatal("Add received the expired Load context")
	}
	if registry.addPath != path {
		t.Fatalf("Add path = %q, want %q", registry.addPath, path)
	}
	if registry.loadContext == registry.addContext {
		t.Fatal("Load and Add received the same context")
	}
	select {
	case <-registry.addContext.Done():
	default:
		t.Fatal("Add context was not canceled after initialization")
	}
}
