package app

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"

	"github.com/froze/roborev-dashboard/internal/gitx"
)

type RepositoryInspector interface {
	Inspect(context.Context, string) (gitx.Repository, error)
}

type registryConfig struct {
	Recent       []string `json:"recentRepositories"`
	LastSelected string   `json:"lastSelectedRepository"`
}

type Registry struct {
	mu         sync.RWMutex
	paths      map[string]string
	selected   string
	configFile string
	inspector  RepositoryInspector
}

func registryKey(path string) string {
	path = filepath.Clean(path)
	if runtime.GOOS == "windows" {
		return strings.ToLower(path)
	}
	return path
}

func NewRegistry(inspector RepositoryInspector, configFile string) *Registry {
	return &Registry{paths: make(map[string]string), inspector: inspector, configFile: configFile}
}

func (r *Registry) Load(ctx context.Context) {
	if r.configFile == "" {
		return
	}
	data, err := os.ReadFile(r.configFile)
	if err != nil {
		return
	}
	var cfg registryConfig
	if json.Unmarshal(data, &cfg) != nil {
		return
	}
	for _, path := range cfg.Recent {
		_, _ = r.Add(ctx, path)
	}
	if r.Contains(cfg.LastSelected) {
		r.selected = r.paths[registryKey(cfg.LastSelected)]
	}
}

func (r *Registry) saveLocked() error {
	if r.configFile == "" {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(r.configFile), 0o700); err != nil {
		return err
	}
	paths := make([]string, 0, len(r.paths))
	for _, path := range r.paths {
		paths = append(paths, path)
	}
	data, err := json.MarshalIndent(registryConfig{Recent: paths, LastSelected: r.selected}, "", "  ")
	if err != nil {
		return err
	}
	temp := r.configFile + ".tmp"
	if err := os.WriteFile(temp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(temp, r.configFile)
}

func (r *Registry) Add(ctx context.Context, path string) (gitx.Repository, error) {
	if r.inspector == nil {
		return gitx.Repository{}, errors.New("repository inspector is unavailable")
	}
	repo, err := r.inspector.Inspect(ctx, path)
	if err != nil {
		return gitx.Repository{}, err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.paths[registryKey(repo.Path)] = repo.Path
	if r.selected == "" {
		r.selected = repo.Path
	}
	if err := r.saveLocked(); err != nil {
		return gitx.Repository{}, err
	}
	return repo, nil
}

func (r *Registry) Contains(path string) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	_, ok := r.paths[registryKey(path)]
	return ok
}

func (r *Registry) Resolve(path string) (string, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if path == "" {
		path = r.selected
	}
	resolved, ok := r.paths[registryKey(path)]
	if !ok {
		return "", errors.New("repository is not registered")
	}
	return resolved, nil
}

func (r *Registry) Select(path string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	resolved, ok := r.paths[registryKey(path)]
	if !ok {
		return errors.New("repository is not registered")
	}
	r.selected = resolved
	return r.saveLocked()
}

func (r *Registry) InspectAll(ctx context.Context) []gitx.Repository {
	r.mu.RLock()
	paths := make([]string, 0, len(r.paths))
	for _, path := range r.paths {
		paths = append(paths, path)
	}
	r.mu.RUnlock()
	repos := make([]gitx.Repository, 0, len(paths))
	for _, path := range paths {
		if repo, err := r.inspector.Inspect(ctx, path); err == nil {
			repos = append(repos, repo)
		}
	}
	return repos
}

func (r *Registry) Selected() string { r.mu.RLock(); defer r.mu.RUnlock(); return r.selected }
