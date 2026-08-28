package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

type versionResponse struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

func main() {
	if logPath := os.Getenv("RELEASE_E2E_FAKE_ROBOREV_LOG"); logPath != "" {
		_ = os.MkdirAll(filepath.Dir(logPath), 0o700)
		file, err := os.OpenFile(logPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
		if err == nil {
			defer file.Close()
			_, _ = fmt.Fprintln(file, strings.Join(os.Args[1:], " "))
		}
	}
	if len(os.Args) == 3 && os.Args[1] == "version" && os.Args[2] == "--json" {
		version := os.Getenv("RELEASE_E2E_FAKE_ROBOREV_VERSION")
		if version == "" {
			version = "v0.64.0"
		}
		_ = json.NewEncoder(os.Stdout).Encode(versionResponse{Name: "roborev", Version: version})
		return
	}
	fmt.Fprintln(os.Stderr, "unsupported fake RoboRev command")
	os.Exit(2)
}
