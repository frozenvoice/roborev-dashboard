package app

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/froze/roborev-dashboard/internal/gitx"
	"github.com/froze/roborev-dashboard/internal/hookstatus"
	"github.com/froze/roborev-dashboard/internal/roborev"
)

type hookStatusAppService struct {
	*fakeService
	version string
	calls   int
}

func (s *hookStatusAppService) ProbeVersion(context.Context) (string, error) {
	s.calls++
	return s.version, nil
}

type hookStatusInspector struct {
	*reviewExportInspector
	hooksPath  string
	hooksCalls int
}

func (i *hookStatusInspector) HooksPath(context.Context, string) (string, error) {
	i.hooksCalls++
	return i.hooksPath, nil
}

type hookStatusMemoryInfo struct {
	name string
	size int64
	mode os.FileMode
	when time.Time
}

func (i hookStatusMemoryInfo) Name() string       { return i.name }
func (i hookStatusMemoryInfo) Size() int64        { return i.size }
func (i hookStatusMemoryInfo) Mode() os.FileMode  { return i.mode }
func (i hookStatusMemoryInfo) ModTime() time.Time { return i.when }
func (i hookStatusMemoryInfo) IsDir() bool        { return i.mode.IsDir() }
func (i hookStatusMemoryInfo) Sys() any           { return nil }

type hookStatusMemoryFile struct {
	*bytes.Reader
	info hookStatusMemoryInfo
}

func (f *hookStatusMemoryFile) Close() error               { return nil }
func (f *hookStatusMemoryFile) Stat() (os.FileInfo, error) { return f.info, nil }

type hookStatusMemoryEntry struct {
	content []byte
	info    hookStatusMemoryInfo
}

type hookStatusMemoryFS struct {
	entries    map[string]hookStatusMemoryEntry
	lstatCalls int
	openCalls  int
}

func (f *hookStatusMemoryFS) Lstat(path string) (os.FileInfo, error) {
	f.lstatCalls++
	entry, ok := f.entries[path]
	if !ok {
		return nil, os.ErrNotExist
	}
	return entry.info, nil
}

func (f *hookStatusMemoryFS) Open(path string) (hookstatus.File, error) {
	f.openCalls++
	entry, ok := f.entries[path]
	if !ok {
		return nil, os.ErrNotExist
	}
	return &hookStatusMemoryFile{Reader: bytes.NewReader(entry.content), info: entry.info}, nil
}

func newHookStatusMemoryFS(hooksPath string, postCommit, postRewrite string) *hookStatusMemoryFS {
	when := time.Unix(123, 0)
	entries := make(map[string]hookStatusMemoryEntry)
	for name, content := range map[string]string{"post-commit": postCommit, "post-rewrite": postRewrite} {
		entries[filepath.Join(hooksPath, name)] = hookStatusMemoryEntry{
			content: []byte(content),
			info:    hookStatusMemoryInfo{name: name, size: int64(len(content)), mode: 0o700, when: when},
		}
	}
	return &hookStatusMemoryFS{entries: entries}
}

func hookStatusRequest(path string) *http.Request {
	request := httptest.NewRequest(http.MethodGet, path, nil)
	request.Header.Set("Sec-Fetch-Site", "same-origin")
	request.Header.Set("Sec-Fetch-Mode", "cors")
	request.Header.Set("Sec-Fetch-Dest", "empty")
	return request
}

func TestRepositoryHooksStrictAuthorizationAndQueryOrdering(t *testing.T) {
	root := `E:\hook-repo`
	inspector := &hookStatusInspector{reviewExportInspector: &reviewExportInspector{repository: gitx.Repository{Path: root}}}
	registry := NewRegistry(inspector, "")
	if _, err := registry.Add(context.Background(), root); err != nil {
		t.Fatal(err)
	}
	identity, err := registry.Identity(root)
	if err != nil {
		t.Fatal(err)
	}
	service := &hookStatusAppService{fakeService: &fakeService{}, version: "v0.64.0"}
	server, err := NewServer(service, registry, "")
	if err != nil {
		t.Fatal(err)
	}
	memoryFS := newHookStatusMemoryFS(`E:\hooks`, "", "")
	server.HookStatus = hookstatus.New(service, inspector, memoryFS)
	inspector.calls = 0

	for _, test := range []struct {
		name string
		path string
		auth bool
		want int
	}{
		{"missing browser auth", "/api/repositories/hooks?repositoryIdentity=" + url.QueryEscape(identity), false, http.StatusForbidden},
		{"missing identity", "/api/repositories/hooks", true, http.StatusBadRequest},
		{"extra query", "/api/repositories/hooks?repositoryIdentity=" + url.QueryEscape(identity) + "&extra=1", true, http.StatusBadRequest},
		{"duplicate identity", "/api/repositories/hooks?repositoryIdentity=" + url.QueryEscape(identity) + "&repositoryIdentity=other", true, http.StatusBadRequest},
		{"blank identity", "/api/repositories/hooks?repositoryIdentity=+", true, http.StatusBadRequest},
		{"foreign identity", "/api/repositories/hooks?repositoryIdentity=foreign", true, http.StatusForbidden},
	} {
		t.Run(test.name, func(t *testing.T) {
			before := inspector.calls
			beforeVersion := service.calls
			beforeHooks := inspector.hooksCalls
			beforeLstat := memoryFS.lstatCalls
			beforeOpen := memoryFS.openCalls
			request := hookStatusRequest(test.path)
			if !test.auth {
				request.Header = nil
			}
			recorder := httptest.NewRecorder()
			server.Handler().ServeHTTP(recorder, request)
			if recorder.Code != test.want {
				t.Fatalf("status=%d want=%d body=%s", recorder.Code, test.want, recorder.Body.String())
			}
			if inspector.calls != before || service.calls != beforeVersion || inspector.hooksCalls != beforeHooks || memoryFS.lstatCalls != beforeLstat || memoryFS.openCalls != beforeOpen {
				t.Fatalf("side effects inspect=%d->%d version=%d->%d hooks=%d->%d lstat=%d->%d open=%d->%d", before, inspector.calls, beforeVersion, service.calls, beforeHooks, inspector.hooksCalls, beforeLstat, memoryFS.lstatCalls, beforeOpen, memoryFS.openCalls)
			}
		})
	}
}

func TestRepositoryHooksV064ReturnsExactReadyDTOWithInjectedFilesystem(t *testing.T) {
	root := `E:\hook-repo`
	hooksPath := `E:\hook-files`
	inspector := &hookStatusInspector{reviewExportInspector: &reviewExportInspector{repository: gitx.Repository{Path: root}}, hooksPath: hooksPath}
	registry := NewRegistry(inspector, "")
	if _, err := registry.Add(context.Background(), root); err != nil {
		t.Fatal(err)
	}
	identity, _ := registry.Identity(root)
	service := &hookStatusAppService{fakeService: &fakeService{}, version: "v0.64.0"}
	server, err := NewServer(service, registry, "")
	if err != nil {
		t.Fatal(err)
	}
	memoryFS := newHookStatusMemoryFS(hooksPath, "# roborev post-commit hook v4", "# roborev post-rewrite hook v2")
	server.HookStatus = hookstatus.New(service, inspector, memoryFS)
	inspector.calls = 0
	inspector.hooksCalls = 0
	recorder := httptest.NewRecorder()
	server.Handler().ServeHTTP(recorder, hookStatusRequest("/api/repositories/hooks?repositoryIdentity="+url.QueryEscape(identity)))
	if recorder.Code != http.StatusOK || inspector.calls != 1 || service.calls != 1 || inspector.hooksCalls != 1 || memoryFS.lstatCalls != 2 || memoryFS.openCalls != 2 {
		t.Fatalf("status=%d inspect=%d version=%d hooks=%d lstat=%d open=%d body=%s", recorder.Code, inspector.calls, service.calls, inspector.hooksCalls, memoryFS.lstatCalls, memoryFS.openCalls, recorder.Body.String())
	}
	var response hookstatus.Response
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response.RepositoryIdentity != identity || response.Status != hookstatus.Ready || response.PostCommit.Status != hookstatus.Current || response.PostRewrite.Status != hookstatus.Current {
		t.Fatalf("response=%+v", response)
	}
	if strings.Contains(recorder.Body.String(), "hook-files") || strings.Contains(recorder.Body.String(), "roborev post") {
		t.Fatalf("path/content leaked: %s", recorder.Body.String())
	}
}

func TestRepositoryHooksFreshInspectionThenTypedStatusDTO(t *testing.T) {
	root := `E:\hook-repo`
	inspector := &hookStatusInspector{reviewExportInspector: &reviewExportInspector{repository: gitx.Repository{Path: root}}}
	registry := NewRegistry(inspector, "")
	if _, err := registry.Add(context.Background(), root); err != nil {
		t.Fatal(err)
	}
	identity, _ := registry.Identity(root)
	service := &hookStatusAppService{fakeService: &fakeService{}, version: "v0.63.0"}
	server, err := NewServer(service, registry, "")
	if err != nil {
		t.Fatal(err)
	}
	server.HookStatus = hookstatus.New(service, inspector, nil)
	inspector.calls = 0
	recorder := httptest.NewRecorder()
	server.Handler().ServeHTTP(recorder, hookStatusRequest("/api/repositories/hooks?repositoryIdentity="+url.QueryEscape(identity)))
	if recorder.Code != http.StatusOK || inspector.calls != 1 || service.calls != 1 {
		t.Fatalf("status=%d inspect=%d version=%d body=%s", recorder.Code, inspector.calls, service.calls, recorder.Body.String())
	}
	var response hookstatus.Response
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response.RepositoryIdentity != identity || response.Status != hookstatus.Unavailable || response.PostCommit.Status != hookstatus.Unknown || response.PostRewrite.Status != hookstatus.Unknown {
		t.Fatalf("response=%+v", response)
	}
}

// Keep the compile-time assertion close to this endpoint's test doubles: the
// endpoint remains compatible with the existing base RoboRev service.
var _ roborev.Service = (*hookStatusAppService)(nil)
