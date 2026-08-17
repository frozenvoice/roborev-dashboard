package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
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
	return gitx.RepositoryKey(path)
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
	loaded := make(map[string]string)
	firstLoaded := ""
	for _, path := range cfg.Recent {
		repo, inspectErr := r.inspectExpected(ctx, path)
		if inspectErr == nil {
			loaded[registryKey(repo.Path)] = repo.Path
			if firstLoaded == "" {
				firstLoaded = repo.Path
			}
		}
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.paths = loaded
	selectedPath := ""
	if selected, ok := loaded[registryKey(cfg.LastSelected)]; ok {
		selectedPath = selected
	}
	if selectedPath == "" {
		selectedPath = firstLoaded
	}
	r.selected = selectedPath
}

func (r *Registry) saveState(pathsByKey map[string]string, selected string) error {
	if r.configFile == "" {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(r.configFile), 0o700); err != nil {
		return err
	}
	paths := make([]string, 0, len(pathsByKey))
	for _, path := range pathsByKey {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	data, err := json.MarshalIndent(registryConfig{Recent: paths, LastSelected: selected}, "", "  ")
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
	prospectivePaths := make(map[string]string, len(r.paths)+1)
	for key, value := range r.paths {
		prospectivePaths[key] = value
	}
	prospectivePaths[registryKey(repo.Path)] = repo.Path
	prospectiveSelected := r.selected
	if prospectiveSelected == "" {
		prospectiveSelected = repo.Path
	}
	if err := r.saveState(prospectivePaths, prospectiveSelected); err != nil {
		return gitx.Repository{}, err
	}
	r.paths = prospectivePaths
	r.selected = prospectiveSelected
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
	if err := r.saveState(r.paths, resolved); err != nil {
		return err
	}
	r.selected = resolved
	return nil
}

// Inspect resolves a path using the registry's platform-aware identity rules
// and returns freshly inspected repository state for that registered root.
func (r *Registry) Inspect(ctx context.Context, path string) (gitx.Repository, error) {
	resolved, err := r.Resolve(path)
	if err != nil {
		return gitx.Repository{}, err
	}
	return r.inspectExpected(ctx, resolved)
}

func (r *Registry) inspectExpected(ctx context.Context, expectedPath string) (gitx.Repository, error) {
	if r.inspector == nil {
		return gitx.Repository{}, errors.New("repository inspector is unavailable")
	}
	repo, err := r.inspector.Inspect(ctx, expectedPath)
	if err != nil {
		return gitx.Repository{}, err
	}
	if !gitx.SameRepositoryPath(repo.Path, expectedPath) {
		return gitx.Repository{}, fmt.Errorf("inspected Git root %q does not match expected repository path %q", repo.Path, expectedPath)
	}
	return repo, nil
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
		if repo, err := r.inspectExpected(ctx, path); err == nil {
			repos = append(repos, repo)
		}
	}
	return repos
}

// Identity returns a stable, platform-aware correlation key for a registered
// repository. It does not grant authorization; mutations continue to resolve
// and authorize repository paths through the registry.
func (r *Registry) Identity(path string) (string, error) {
	resolved, err := r.Resolve(path)
	if err != nil {
		return "", err
	}
	return registryKey(resolved), nil
}

func (r *Registry) Selected() string { r.mu.RLock(); defer r.mu.RUnlock(); return r.selected }
