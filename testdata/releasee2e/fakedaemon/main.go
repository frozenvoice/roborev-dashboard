package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

type daemon struct {
	version  string
	logPath  string
	repoPath string
}

func (d *daemon) record(r *http.Request) {
	if d.logPath == "" {
		return
	}
	if err := os.MkdirAll(filepath.Dir(d.logPath), 0o700); err != nil {
		return
	}
	file, err := os.OpenFile(d.logPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return
	}
	defer file.Close()
	_, _ = fmt.Fprintf(file, "%s %s\n", r.Method, r.URL.RequestURI())
}

func (d *daemon) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	d.record(r)
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	switch r.URL.Path {
	case "/api/health":
		_ = json.NewEncoder(w).Encode(map[string]any{"healthy": true, "version": d.version})
	case "/api/jobs":
		id := int64(42)
		if queryID, err := strconv.ParseInt(r.URL.Query().Get("id"), 10, 64); err == nil && queryID > 0 {
			id = queryID
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"jobs": []map[string]any{{
			"id": id, "repo_path": d.repoPath, "git_ref": "0123456789abcdef0123456789abcdef01234567",
			"branch": "main", "job_type": "review", "status": "done", "verdict": "P",
			"closed": false,
		}}})
	case "/api/review":
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id": 42, "job_id": 42, "status": "done", "verdict": "P",
			"review":    map[string]any{"output": "safe review fixture"},
			"repo_path": d.repoPath, "prompt": "PRIVATE_PROMPT", "binary": "PRIVATE_BINARY",
		})
	default:
		// A GET to an unsupported endpoint is still recorded, but stays a safe
		// JSON 404 so the dashboard cannot accidentally consume an HTML page.
		w.WriteHeader(http.StatusNotFound)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "not found"})
	}
}

func main() {
	port := flag.Int("port", 0, "loopback port")
	version := flag.String("version", "v0.64.0", "health version")
	logPath := flag.String("log", "", "request log")
	repoPath := flag.String("repo", "", "fixture repository root")
	flag.Parse()
	listener, err := net.Listen("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(*port)))
	if err != nil {
		log.Fatal(err)
	}
	host := listener.Addr().String()
	// Keep stdout a deliberately tiny machine-readable startup contract.
	fmt.Printf("FAKE_DAEMON: http://%s\n", host)
	if err := (&http.Server{Handler: &daemon{version: *version, logPath: *logPath, repoPath: *repoPath}}).Serve(listener); err != nil && !strings.Contains(err.Error(), "closed network connection") {
		log.Print(err)
	}
}
