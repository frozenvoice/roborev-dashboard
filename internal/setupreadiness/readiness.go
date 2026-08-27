// Package setupreadiness owns the narrow, content-free repository setup
// readiness contract.  It deliberately knows nothing about HTTP, RoboRev
// command output, or browser-facing paths.
package setupreadiness

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/froze/roborev-dashboard/internal/pathsafety"
)

const (
	Version      = "v0.64.0"
	DefaultDir   = ".roborev"
	CheckTimeout = 25 * time.Second
)

type Overall string
type Configuration string
type Snapshot string
type GitHooks string
type Daemon string
type Tracking string

const (
	OverallReady             Overall       = "ready"
	OverallNeedsAttention    Overall       = "needs-attention"
	OverallUnavailable       Overall       = "unavailable"
	ConfigurationReady       Configuration = "ready"
	ConfigurationUnavailable Configuration = "unavailable"
	SnapshotReady            Snapshot      = "ready"
	SnapshotNotIgnored       Snapshot      = "not-ignored"
	SnapshotTrackedFiles     Snapshot      = "tracked-files"
	SnapshotUnsafe           Snapshot      = "unsafe"
	SnapshotUnavailable      Snapshot      = "unavailable"
	GitHooksReady            GitHooks      = "ready"
	GitHooksNeedsAttention   GitHooks      = "needs-attention"
	GitHooksNotInstalled     GitHooks      = "not-installed"
	GitHooksUnknown          GitHooks      = "unknown"
	DaemonOnline             Daemon        = "online"
	DaemonOffline            Daemon        = "offline"
	DaemonUnavailable        Daemon        = "unavailable"
	TrackingTracked          Tracking      = "tracked"
	TrackingNotTracked       Tracking      = "not-tracked"
	TrackingUnavailable      Tracking      = "unavailable"
)

// Result is the complete browser DTO.  No member can carry paths, command
// arguments, process output, or content.
type Result struct {
	Version            string                         `json:"version"`
	RepositoryIdentity string                         `json:"repositoryIdentity"`
	Overall            Overall                        `json:"overall"`
	Configuration      StatusComponent[Configuration] `json:"configuration"`
	Snapshot           StatusComponent[Snapshot]      `json:"snapshot"`
	GitHooks           StatusComponent[GitHooks]      `json:"gitHooks"`
	Daemon             StatusComponent[Daemon]        `json:"daemon"`
	Tracking           StatusComponent[Tracking]      `json:"tracking"`
}

// StatusComponent gives every readiness component one exact status key on
// the wire. The generic is server-side compile-time structure only; JSON is
// always {"status":"..."}.
type StatusComponent[T ~string] struct {
	Status T `json:"status"`
}

// SnapshotObservation is server-side output from the typed Git probe.
type SnapshotObservation struct {
	Tracked bool
	Ignored bool
}

// DaemonObservation is server-side output from the combined health/inventory
// seam. InventoryRoots is never copied into Result.
type DaemonObservation struct {
	State              Daemon
	InventoryAvailable bool
	InventoryRoots     []string
}

// Dependencies are injected so all command, daemon, and filesystem behavior
// can be tested using deterministic fakes.
type Dependencies struct {
	Configuration func(context.Context, string) (string, error)
	Snapshot      func(context.Context, string, string) (SnapshotObservation, error)
	Hooks         func(context.Context, string, string) (GitHooks, error)
	Daemon        func(context.Context) (DaemonObservation, error)
	// TrackingRoot optionally maps the authorized checkout root to the
	// effective root used by RoboRev's repository inventory. It is intentionally
	// separate from Configuration/Snapshot/Hooks, which remain scoped to the
	// selected checkout. Legacy fakes may omit it and retain the prior behavior.
	TrackingRoot func(context.Context, string) (string, error)
	FS           pathsafety.FileSystem
}

type Checker struct{ deps Dependencies }

func New(deps Dependencies) *Checker { return &Checker{deps: deps} }

// ResolveSnapshotDir applies the v0.64 repository-local snapshot_dir
// semantics. The returned value is relative and server-owned.
func ResolveSnapshotDir(raw string) (string, error) {
	value := strings.TrimSpace(raw)
	if value == "" {
		value = DefaultDir
	}
	if !utf8.ValidString(value) || hasControl(value) {
		return "", errors.New("snapshot directory is invalid")
	}
	// Normalize slash forms before filepath validation so a Windows path
	// remains unsafe even when this package is tested on another OS.
	normalized := strings.ReplaceAll(value, "\\", "/")
	if strings.HasPrefix(normalized, "/") || strings.HasPrefix(normalized, "//") || isDrivePath(normalized) {
		return "", errors.New("snapshot directory must be repository-local")
	}
	clean := filepath.Clean(filepath.FromSlash(normalized))
	if clean == "." || !filepath.IsLocal(clean) {
		return "", errors.New("snapshot directory must be repository-local")
	}
	parts := strings.FieldsFunc(strings.ReplaceAll(clean, "\\", "/"), func(r rune) bool { return r == '/' })
	if len(parts) > 0 && strings.EqualFold(parts[0], ".git") {
		return "", errors.New("snapshot directory cannot be inside .git")
	}
	return clean, nil
}

func hasControl(value string) bool {
	for _, r := range value {
		if r < 0x20 || r == 0x7f {
			return true
		}
	}
	return false
}

func isDrivePath(value string) bool {
	return len(value) >= 2 && ((value[0] >= 'a' && value[0] <= 'z') || (value[0] >= 'A' && value[0] <= 'Z')) && value[1] == ':'
}

// Check executes the bounded sequential readiness probe and validates the
// resulting DTO before returning it.
func (c *Checker) Check(ctx context.Context, identity, root string) (Result, error) {
	if c == nil {
		return Result{}, errors.New("readiness checker is unavailable")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	ctx, cancel := context.WithTimeout(ctx, CheckTimeout)
	defer cancel()
	if strings.TrimSpace(identity) == "" || strings.TrimSpace(root) == "" {
		return Result{}, errors.New("repository identity and root are required")
	}
	result := Result{Version: Version, RepositoryIdentity: identity, Overall: OverallUnavailable, Configuration: StatusComponent[Configuration]{Status: ConfigurationUnavailable}, Snapshot: StatusComponent[Snapshot]{Status: SnapshotUnavailable}, GitHooks: StatusComponent[GitHooks]{Status: GitHooksUnknown}, Daemon: StatusComponent[Daemon]{Status: DaemonUnavailable}, Tracking: StatusComponent[Tracking]{Status: TrackingUnavailable}}
	if c.deps.Configuration == nil {
	} else {
		snapshotDir, configErr := c.deps.Configuration(ctx, root)
		if configErr == nil {
			result.Configuration.Status = ConfigurationReady
			relative, pathErr := ResolveSnapshotDir(snapshotDir)
			if pathErr != nil {
				result.Snapshot.Status = SnapshotUnsafe
			} else if c.deps.Snapshot == nil {
				result.Snapshot.Status = SnapshotUnavailable
			} else {
				result.Snapshot.Status = c.checkSnapshot(ctx, root, relative)
			}
		}
	}
	if err := ctx.Err(); err != nil {
		return Result{}, err
	}
	if c.deps.Hooks != nil {
		if value, err := c.deps.Hooks(ctx, identity, root); err == nil {
			result.GitHooks.Status = value
		}
	}
	if err := ctx.Err(); err != nil {
		return Result{}, err
	}
	if c.deps.Daemon != nil {
		observation, err := c.deps.Daemon(ctx)
		if err == nil || observation.State == DaemonOffline {
			result.Daemon.Status = observation.State
			if observation.State == DaemonOnline && observation.InventoryAvailable {
				trackingRoot := root
				trackingRootOK := true
				if c.deps.TrackingRoot != nil {
					trackingRoot, err = c.deps.TrackingRoot(ctx, root)
					trackingRootOK = err == nil && strings.TrimSpace(trackingRoot) != ""
				}
				if trackingRootOK {
					result.Tracking.Status = trackingForRoot(observation.InventoryRoots, trackingRoot)
				}
			} else if observation.State == DaemonOnline {
				result.Tracking.Status = TrackingUnavailable
			} else {
				result.Tracking.Status = TrackingUnavailable
			}
		}
	}
	if err := ctx.Err(); err != nil {
		return Result{}, err
	}
	result.Overall = deriveOverall(result)
	return c.finish(result)
}

func (c *Checker) checkSnapshot(ctx context.Context, root, relative string) Snapshot {
	fs := c.deps.FS
	if fs == nil {
		fs = pathsafety.OSFileSystem{}
	}
	rootBefore, err := pathsafety.CaptureDirectory(fs, root)
	if err != nil {
		return SnapshotUnsafe
	}
	target := filepath.Join(root, relative)
	targetBefore, err := pathsafety.CapturePlannedDirectory(fs, target)
	if err != nil {
		return SnapshotUnsafe
	}
	observation, err := c.deps.Snapshot(ctx, root, relative)
	if err != nil {
		return SnapshotUnavailable
	}
	rootAfter, err := pathsafety.CaptureDirectory(fs, root)
	if err != nil || !rootBefore.Equal(rootAfter) {
		return SnapshotUnsafe
	}
	targetAfter, err := pathsafety.CapturePlannedDirectory(fs, target)
	if err != nil || !targetBefore.Equal(targetAfter) {
		return SnapshotUnsafe
	}
	if observation.Tracked {
		return SnapshotTrackedFiles
	}
	if !observation.Ignored {
		return SnapshotNotIgnored
	}
	return SnapshotReady
}

func trackingForRoot(roots []string, root string) Tracking {
	if !validInventoryRoot(root) {
		return TrackingUnavailable
	}
	selectedKey := repositoryRootKey(root)
	seen := make(map[string]struct{}, len(roots))
	found := 0
	for _, candidate := range roots {
		if !validInventoryRoot(candidate) {
			return TrackingUnavailable
		}
		key := repositoryRootKey(candidate)
		if _, exists := seen[key]; exists {
			return TrackingUnavailable
		}
		seen[key] = struct{}{}
		if key == selectedKey {
			found++
		}
	}
	if found == 1 {
		return TrackingTracked
	}
	return TrackingNotTracked
}

func sameRoot(left, right string) bool {
	return repositoryRootKey(left) == repositoryRootKey(right)
}

func validInventoryRoot(value string) bool {
	return value != "" && strings.TrimSpace(value) == value && utf8.ValidString(value) && !hasControl(value) && isAbsoluteRoot(value)
}

func isAbsoluteRoot(value string) bool {
	if filepath.IsAbs(value) {
		return true
	}
	normalized := strings.ReplaceAll(value, "\\", "/")
	return strings.HasPrefix(normalized, "/") || (isDrivePath(normalized) && len(normalized) >= 3 && normalized[2] == '/')
}

func repositoryRootKey(value string) string {
	windowsStyle := isDrivePath(strings.ReplaceAll(value, "\\", "/")) || strings.HasPrefix(value, `\\`) || strings.HasPrefix(value, "//")
	normalized := strings.ReplaceAll(value, "\\", "/")
	clean := filepath.Clean(filepath.FromSlash(normalized))
	clean = strings.ReplaceAll(clean, "\\", "/")
	if windowsStyle {
		return strings.ToLower(clean)
	}
	return clean
}

func deriveOverall(result Result) Overall {
	if result.Configuration.Status == ConfigurationReady && result.Snapshot.Status == SnapshotReady && result.GitHooks.Status == GitHooksReady && result.Daemon.Status == DaemonOnline && result.Tracking.Status == TrackingTracked {
		return OverallReady
	}
	if result.Snapshot.Status == SnapshotUnsafe || result.GitHooks.Status == GitHooksUnknown || result.Daemon.Status == DaemonUnavailable || result.Configuration.Status == ConfigurationUnavailable || result.Snapshot.Status == SnapshotUnavailable || result.Tracking.Status == TrackingUnavailable && result.Daemon.Status == DaemonOnline {
		return OverallUnavailable
	}
	if result.Daemon.Status == DaemonOffline && result.Tracking.Status == TrackingUnavailable {
		return OverallNeedsAttention
	}
	return OverallNeedsAttention
}

func (c *Checker) finish(result Result) (Result, error) {
	if err := result.Validate(); err != nil {
		return Result{}, err
	}
	return result, nil
}

func (r Result) Validate() error {
	if r.Version != Version || strings.TrimSpace(r.RepositoryIdentity) == "" || !utf8.ValidString(r.RepositoryIdentity) || hasControl(r.RepositoryIdentity) {
		return fmt.Errorf("invalid readiness identity or version")
	}
	if !validOverall(r.Overall) || !validConfiguration(r.Configuration.Status) || !validSnapshot(r.Snapshot.Status) || !validHooks(r.GitHooks.Status) || !validDaemon(r.Daemon.Status) || !validTracking(r.Tracking.Status) {
		return errors.New("invalid readiness state")
	}
	if r.Configuration.Status == ConfigurationUnavailable && r.Snapshot.Status != SnapshotUnavailable {
		return errors.New("unavailable configuration requires unavailable snapshot")
	}
	if r.Daemon.Status != DaemonOnline && r.Tracking.Status != TrackingUnavailable {
		return errors.New("offline or unavailable daemon requires unavailable tracking")
	}
	if r.Overall != deriveOverall(r) {
		return errors.New("readiness overall state is inconsistent")
	}
	return nil
}

func validOverall(v Overall) bool {
	return v == OverallReady || v == OverallNeedsAttention || v == OverallUnavailable
}
func validConfiguration(v Configuration) bool {
	return v == ConfigurationReady || v == ConfigurationUnavailable
}
func validSnapshot(v Snapshot) bool {
	return v == SnapshotReady || v == SnapshotNotIgnored || v == SnapshotTrackedFiles || v == SnapshotUnsafe || v == SnapshotUnavailable
}
func validHooks(v GitHooks) bool {
	return v == GitHooksReady || v == GitHooksNeedsAttention || v == GitHooksNotInstalled || v == GitHooksUnknown
}
func validDaemon(v Daemon) bool {
	return v == DaemonOnline || v == DaemonOffline || v == DaemonUnavailable
}
func validTracking(v Tracking) bool {
	return v == TrackingTracked || v == TrackingNotTracked || v == TrackingUnavailable
}
