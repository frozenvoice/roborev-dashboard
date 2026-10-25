package main

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
)

func main() {
	root, err := moduleRoot()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	artifacts, err := setupArtifacts()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	for _, item := range artifacts {
		path := filepath.Join(root, filepath.FromSlash(item.path))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		existing, readErr := os.ReadFile(path)
		if readErr == nil && bytes.Equal(existing, item.bytes) {
			fmt.Printf("unchanged %s\n", item.path)
			continue
		}
		if err := os.WriteFile(path, item.bytes, 0o644); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		fmt.Printf("wrote %s (%s)\n", item.path, item.why)
	}
}

func moduleRoot() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for {
		if _, statErr := os.Stat(filepath.Join(dir, "go.mod")); statErr == nil {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", fmt.Errorf("go.mod not found from %s", dir)
		}
		dir = parent
	}
}
