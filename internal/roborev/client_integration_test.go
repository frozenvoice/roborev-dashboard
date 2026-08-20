package roborev

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	proc "github.com/froze/roborev-dashboard/internal/process"
)

func buildFakeRoboRev(t *testing.T) string {
	t.Helper()
	name := "fake-roborev"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	output := filepath.Join(t.TempDir(), name)
	goBinary := filepath.Join(runtime.GOROOT(), "bin", "go")
	if runtime.GOOS == "windows" {
		goBinary += ".exe"
	}
	command := exec.Command(goBinary, "build", "-o", output, "../../testdata/fakeroborev")
	if data, err := command.CombinedOutput(); err != nil {
		t.Fatalf("build fake roborev: %v\n%s", err, data)
	}
	return output
}

func TestClientUsesFakeExecutableMachineContracts(t *testing.T) {
	binary := buildFakeRoboRev(t)
	health := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch request.URL.Path {
		case "/api/health":
			_, _ = w.Write([]byte(`{"$schema":"http://127.0.0.1:7373/HealthStatus.json","healthy":true,"version":"v0.64.0"}`))
		case "/api/jobs":
			if request.URL.Query().Get("omit_prompt") != "true" {
				t.Error("job list did not omit prompts")
			}
			query := request.URL.Query()
			job := map[string]any{"id": 42, "repo_path": `E:\한글 프로젝트\repo`, "status": "running", "verdict": "F", "commit_subject": "Observed metadata", "enqueued_at": "2026-08-17T00:00:00Z", "started_at": "2026-08-17T00:00:05Z", "prompt": strings.Repeat("large-active-prompt", 140_000), "diff_content": "secret diff"}
			switch {
			case query.Get("id") == "42":
				if query.Get("limit") != "1" {
					t.Error("single job lookup did not request limit=1")
				}
			case query.Get("panel_run") == "986d3bfa-05da-4621-aeca-66f493a19ea3":
				if query.Get("limit") != "0" || query.Get("omit_prompt") != "true" {
					t.Errorf("panel member lookup query = %s, want limit=0&omit_prompt=true", query.Encode())
				}
				job["id"] = 43
				job["panel_role"] = "member"
				job["panel_member_index"] = 0
			case query.Get("repo") == `E:\한글 프로젝트\repo`:
				job["finished_at"] = "2026-08-17T00:00:30Z"
			default:
				http.Error(w, "unexpected jobs query", http.StatusBadRequest)
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"jobs": []any{job}, "has_more": false})
		default:
			http.NotFound(w, request)
		}
	}))
	defer health.Close()
	client, err := NewClient(binary, proc.ExecRunner{}, health.URL)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	installation := client.Installation(ctx)
	if !installation.Installed || installation.Version != "v0.64.0" {
		t.Fatalf("unexpected installation: %#v", installation)
	}
	var status struct {
		Running bool `json:"running"`
		Known   bool `json:"known"`
	}
	if json.Unmarshal(installation.Status, &status) != nil || !status.Running || !status.Known {
		t.Fatalf("unexpected daemon health status: %s", installation.Status)
	}
	client.versionMu.Lock()
	client.version = "v0.63.0"
	client.versionAt = time.Now()
	_, _, generation := client.snapshot()
	client.versionGeneration = generation
	client.versionMu.Unlock()
	if version, _ := client.detectVersion(ctx); version != "v0.63.0" {
		t.Fatalf("fresh cached version was not reused: %q", version)
	}
	client.versionMu.Lock()
	client.versionAt = time.Now().Add(-time.Minute)
	client.versionMu.Unlock()
	if version, _ := client.detectVersion(ctx); version != "v0.64.0" {
		t.Fatalf("expired version cache was not refreshed: %q", version)
	}
	jobs, err := client.List(ctx, `E:\한글 프로젝트\repo`, 100)
	if err != nil || len(jobs) == 0 {
		t.Fatalf("list failed: %v %s", err, jobs)
	}
	if bytes.Contains(jobs, []byte(`"prompt"`)) || bytes.Contains(jobs, []byte(`"diff_content"`)) {
		t.Fatalf("metadata list leaked heavy fields: %s", jobs)
	}
	metadata, err := client.JobMetadata(ctx, 42)
	if err != nil || metadata.ID != 42 {
		t.Fatalf("metadata failed: %#v %v", metadata, err)
	}
	show, err := client.Show(ctx, 42)
	if err != nil || !bytes.Contains(show, []byte(`"review"`)) {
		t.Fatalf("show failed: %v %s", err, show)
	}
	members, err := client.PanelMembers(ctx, "986d3bfa-05da-4621-aeca-66f493a19ea3")
	if err != nil || !bytes.Contains(members, []byte(`"panel_role":"member"`)) {
		t.Fatalf("panel member lookup failed: %v %s", err, members)
	}
	prompt, err := client.Prompt(ctx, 42)
	if err != nil || prompt == "" {
		t.Fatalf("prompt failed: %v", err)
	}
	if err := client.Comment(ctx, 42, "pasted; $(text) & 한글\nline 2"); err != nil {
		t.Fatalf("comment failed: %v", err)
	}
}
