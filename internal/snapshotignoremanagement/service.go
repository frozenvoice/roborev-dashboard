// Package snapshotignoremanagement owns the narrow, append-only
// repository-root .gitignore mutation used by Snapshot Ignore Management.
//
// It deliberately has no Git or RoboRev dependency.  The HTTP adapter must
// finish all slow configuration/Git probes before calling this package.
package snapshotignoremanagement

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/froze/roborev-dashboard/internal/pathsafety"
	"github.com/froze/roborev-dashboard/internal/setupreadiness"
)

const (
	Version = "v0.64.0"
	// MaxBytes is the final .gitignore artifact ceiling. Existing content and
	// the exact append suffix together must fit in one MiB, keeping every
	// preview and post-write verification bounded even for user-owned files.
	MaxBytes int64 = 1 << 20
	Target         = ".gitignore"
)

var (
	ErrUnavailable = errors.New("snapshot ignore management is unavailable")
	ErrUnsafe      = errors.New("snapshot ignore target is unsafe")
	ErrStale       = errors.New("snapshot ignore plan is stale")
	ErrDuplicate   = errors.New("snapshot ignore rule is already present")
	ErrTooLarge    = errors.New("snapshot ignore target exceeds the size limit")
)

// File is the same opened handle on which Stat/Identity, one bounded write,
// Sync and Close operate.  The implementation must not reopen the path to
// establish write authority.
type File interface {
	io.Writer
	Sync() error
	Stat() (os.FileInfo, error)
	Identity() (string, error)
	Close() error
}

// RootCreateAuthority keeps the preview-bound repository root open while a
// missing root/.gitignore is created. CreateGitignoreExclusive intentionally
// has no path argument: the only child this authority can create is the
// literal repository-root .gitignore file.
type RootCreateAuthority interface {
	Stat() (os.FileInfo, error)
	Identity() (string, error)
	CreateGitignoreExclusive() (File, error)
	Close() error
}

// FileSystem is injectable for deterministic race and partial-write tests.
// OpenAppend never creates/truncates. Missing targets use the root-bound
// authority below; there is deliberately no arbitrary full-path create API.
type FileSystem interface {
	pathsafety.FileSystem
	OpenAppend(string) (File, error)
	OpenRootCreateAuthority(string) (RootCreateAuthority, error)
}

type osFileSystem struct{}
type osFile struct{ *os.File }

// OSFileSystem is the production filesystem implementation.
type OSFileSystem = osFileSystem

func (osFileSystem) Lstat(path string) (os.FileInfo, error) { return os.Lstat(path) }
func (osFileSystem) Open(path string) (pathsafety.File, error) {
	// ReadStableFile performs a second path/handle identity check, but the
	// initial open must itself not follow a final symlink or block on a FIFO
	// swapped in during that check.
	f, err := openRead(path)
	if err != nil {
		return nil, err
	}
	return &osReadFile{File: f}, nil
}
func (osFileSystem) EvalSymlinks(path string) (string, error) { return filepath.EvalSymlinks(path) }
func (osFileSystem) Identity(path string, info os.FileInfo) (string, error) {
	return pathsafety.FileIdentity(path, info)
}
func (osFileSystem) IsReparse(path string, info os.FileInfo) bool {
	return pathsafety.IsReparsePoint(path, info)
}
func (osFileSystem) OpenAppend(path string) (File, error) {
	f, err := openAppend(path)
	if err != nil {
		return nil, err
	}
	return &osFile{File: f}, nil
}
func (osFileSystem) OpenRootCreateAuthority(path string) (RootCreateAuthority, error) {
	return openRootCreateAuthority(path)
}

type osReadFile struct{ *os.File }

func (f *osReadFile) Identity() (string, error) { return pathsafety.HandleIdentity(f.File) }
func (f *osFile) Identity() (string, error)     { return pathsafety.HandleIdentity(f.File) }

// TargetStatus is an internal allowlist.  It is safe to use in a sanitized
// HTTP response, but Plan itself contains private paths, bytes and witnesses.
type TargetStatus string

const (
	TargetAbsentSafe TargetStatus = "absent-safe"
	TargetExists     TargetStatus = "exists"
	TargetUnsafe     TargetStatus = "unsafe"
	TargetDuplicate  TargetStatus = "duplicate"
)

type directoryPlan struct {
	path     string
	snapshot pathsafety.Snapshot
}

// Plan is a complete, server-only, identity-bound snapshot.  No fields are
// exported intentionally: encoding/json cannot disclose path/content state.
type Plan struct {
	version       string
	root          string
	rootSnapshot  pathsafety.Snapshot
	snapshotDirs  []directoryPlan
	targetPath    string
	target        pathsafety.Snapshot
	existing      []byte
	existingHash  string
	pattern       string
	suffix        []byte
	expected      []byte
	expectedHash  string
	duplicateRule bool
}

func (p Plan) Equal(other Plan) bool {
	return p.version == other.version && p.root == other.root && p.rootSnapshot.Equal(other.rootSnapshot) &&
		p.targetPath == other.targetPath && p.target.Equal(other.target) && bytes.Equal(p.existing, other.existing) &&
		p.existingHash == other.existingHash && p.pattern == other.pattern && bytes.Equal(p.suffix, other.suffix) &&
		bytes.Equal(p.expected, other.expected) && p.expectedHash == other.expectedHash &&
		p.duplicateRule == other.duplicateRule && equalDirectoryPlans(p.snapshotDirs, other.snapshotDirs)
}

func equalDirectoryPlans(left, right []directoryPlan) bool {
	if len(left) != len(right) {
		return false
	}
	for i := range left {
		if left[i].path != right[i].path || !left[i].snapshot.Equal(right[i].snapshot) {
			return false
		}
	}
	return true
}

func (p Plan) Status() TargetStatus {
	if p.duplicateRule {
		return TargetDuplicate
	}
	return p.targetStatus()
}
func (p Plan) targetStatus() TargetStatus {
	if p.target.Exists {
		return TargetExists
	}
	if p.target.Absence.Path == "" {
		return TargetUnsafe
	}
	return TargetAbsentSafe
}
func (p Plan) Valid() bool {
	return p.version == Version && p.root != "" && p.rootSnapshot.Exists && p.rootSnapshot.Kind == pathsafety.Directory &&
		p.targetPath == filepath.Join(p.root, Target) && p.targetStatus() != TargetUnsafe &&
		len(p.expected) > 0 && int64(len(p.expected)) <= MaxBytes && len(p.pattern) > 0 && !p.duplicateRule
}
func (p Plan) WriteAvailable() bool {
	return p.Valid() && (p.Status() == TargetAbsentSafe || p.Status() == TargetExists)
}
func (p Plan) Target() string { return Target }

// Root and private plan values are available only to server-side adapters.
// They intentionally do not appear in JSON because their names are lower
// case and Plan has no exported fields.
func (p Plan) rootPath() string { return p.root }

// ResultStatus distinguishes pre-mutation rejection from honest outcomes.
type ResultStatus string

const (
	StatusPreMutationStale    ResultStatus = "pre-mutation-stale"
	StatusPreMutationCanceled ResultStatus = "pre-mutation-canceled"
	StatusVerifiedSuccess     ResultStatus = "verified-success"
	StatusFailedUnverified    ResultStatus = "failed-unverified"
)

type ReasonCode string

const (
	ReasonInvalidPlan      ReasonCode = "invalid-plan"
	ReasonStale            ReasonCode = "stale"
	ReasonCanceled         ReasonCode = "canceled"
	ReasonUnsafe           ReasonCode = "unsafe"
	ReasonDuplicate        ReasonCode = "duplicate"
	ReasonOpen             ReasonCode = "open-failed"
	ReasonCreate           ReasonCode = "create-failed"
	ReasonHandleIdentity   ReasonCode = "handle-identity-failed"
	ReasonHandleStat       ReasonCode = "handle-stat-failed"
	ReasonWrite            ReasonCode = "write-failed"
	ReasonZeroProgress     ReasonCode = "zero-progress"
	ReasonSync             ReasonCode = "sync-failed"
	ReasonClose            ReasonCode = "close-failed"
	ReasonReadback         ReasonCode = "readback-failed"
	ReasonReadbackMismatch ReasonCode = "readback-mismatch"
)

type WarningCode string

const (
	WarningWrite WarningCode = "write-error"
	WarningSync  WarningCode = "sync-error"
	WarningClose WarningCode = "close-error"
)

// Receipt remains opaque to HTTP. It is enough to perform the second stable
// filesystem verification after slow post-Git checks.
type Receipt struct {
	rootPath       string
	targetPath     string
	rootSnapshot   pathsafety.Snapshot
	targetSnapshot pathsafety.Snapshot
	targetIdentity string
	expected       []byte
	expectedHash   string
}

type Result struct {
	Status  ResultStatus
	Reason  ReasonCode
	Warning WarningCode
	Receipt *Receipt
}

type Service struct{ FS FileSystem }

func New(fs FileSystem) *Service {
	if fs == nil {
		fs = osFileSystem{}
	}
	return &Service{FS: fs}
}
func NewService(fs FileSystem) *Service { return New(fs) }

// IgnorePatternForDir reproduces roborev v0.64 IgnorePatternForDir: a
// repository-rooted Git directory pattern using forward slashes.
func IgnorePatternForDir(root, relative string) (string, error) {
	if strings.TrimSpace(root) == "" {
		return "", ErrUnsafe
	}
	rel, err := setupreadiness.ResolveSnapshotDir(relative)
	if err != nil {
		return "", ErrUnsafe
	}
	return "/" + filepath.ToSlash(filepath.Clean(rel)) + "/", nil
}

// ValidateSnapshotDirectory performs the pre-Git filesystem safety gate.
// This method intentionally does not inspect .gitignore, allowing the HTTP
// layer to preserve upstream order: resolve -> validate -> tracked -> ignore
// -> target append planning.
func (s *Service) ValidateSnapshotDirectory(_ context.Context, root, relative string) error {
	if s == nil || s.FS == nil {
		return ErrUnavailable
	}
	// ResolveSnapshotDir is intentionally the only relative-config operation
	// here. Pattern derivation belongs after this filesystem safety gate and
	// after the Git tracked probe, matching the upstream ordering contract.
	if strings.TrimSpace(root) == "" {
		return ErrUnsafe
	}
	if _, err := setupreadiness.ResolveSnapshotDir(relative); err != nil {
		return ErrUnsafe
	}
	rootPath, err := pathsafety.CanonicalAbsolutePath(s.FS, root)
	if err != nil {
		return ErrUnsafe
	}
	rootSnapshot, err := pathsafety.CaptureDirectory(s.FS, rootPath)
	if err != nil || !rootSnapshot.Exists {
		return ErrUnsafe
	}
	for _, path := range snapshotChain(rootPath, relative) {
		snapshot, captureErr := pathsafety.CapturePlannedDirectory(s.FS, path)
		if captureErr != nil {
			return ErrUnsafe
		}
		if snapshot.Exists && snapshot.Kind != pathsafety.Directory {
			return ErrUnsafe
		}
	}
	return nil
}

func snapshotChain(root, relative string) []string {
	parts := strings.FieldsFunc(filepath.ToSlash(filepath.Clean(relative)), func(r rune) bool { return r == '/' })
	paths := make([]string, 0, len(parts))
	current := root
	for _, part := range parts {
		current = filepath.Join(current, part)
		paths = append(paths, current)
	}
	return paths
}

// Plan captures exact bounded bytes and witnesses after all Git eligibility
// probes have completed.
func (s *Service) Plan(ctx context.Context, rawRoot, relative string) (Plan, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return Plan{}, err
	}
	if s == nil || s.FS == nil {
		return Plan{}, ErrUnavailable
	}
	pattern, err := IgnorePatternForDir(rawRoot, relative)
	if err != nil {
		return Plan{}, ErrUnsafe
	}
	root, err := pathsafety.CanonicalAbsolutePath(s.FS, rawRoot)
	if err != nil {
		return Plan{}, ErrUnsafe
	}
	rootSnap, err := pathsafety.CaptureDirectory(s.FS, root)
	if err != nil {
		return Plan{}, ErrUnsafe
	}
	dirs := make([]directoryPlan, 0)
	for _, path := range snapshotChain(root, relative) {
		snapshot, captureErr := pathsafety.CapturePlannedDirectory(s.FS, path)
		if captureErr != nil || (snapshot.Exists && snapshot.Kind != pathsafety.Directory) {
			return Plan{}, ErrUnsafe
		}
		dirs = append(dirs, directoryPlan{path: path, snapshot: snapshot})
	}
	targetPath := filepath.Join(root, Target)
	target, existing, err := captureTarget(s.FS, targetPath)
	if err != nil {
		return Plan{}, err
	}
	suffix := appendSuffix(existing, pattern)
	if int64(len(existing))+int64(len(suffix)) > MaxBytes {
		return Plan{}, ErrTooLarge
	}
	expected := append(append([]byte(nil), existing...), suffix...)
	duplicate := bytes.Equal(suffix, nil)
	return Plan{version: Version, root: root, rootSnapshot: rootSnap, snapshotDirs: dirs, targetPath: targetPath, target: target, existing: append([]byte(nil), existing...), existingHash: digest(existing), pattern: pattern, suffix: append([]byte(nil), suffix...), expected: expected, expectedHash: digest(expected), duplicateRule: duplicate}, nil
}

func captureTarget(fs FileSystem, path string) (pathsafety.Snapshot, []byte, error) {
	data, snapshot, err := pathsafety.ReadStableFile(fs, path, MaxBytes)
	if err == nil {
		return snapshot, data, nil
	}
	if !errors.Is(err, pathsafety.ErrMissing) {
		if errors.Is(err, pathsafety.ErrTooLarge) {
			return pathsafety.Snapshot{}, nil, ErrTooLarge
		}
		return pathsafety.Snapshot{}, nil, ErrUnsafe
	}
	missing, captureErr := pathsafety.CaptureFile(fs, path)
	if captureErr != nil || missing.Exists || missing.Absence.Path == "" {
		return pathsafety.Snapshot{}, nil, ErrUnsafe
	}
	return missing, nil, nil
}

// appendSuffix exactly preserves upstream bytes: only a missing final LF on
// non-empty content receives a leading LF. Duplicate detection splits only
// on LF and trims each line, matching AppendIgnorePatternFile semantics.
func appendSuffix(existing []byte, pattern string) []byte {
	for _, line := range strings.Split(string(existing), "\n") {
		if strings.TrimSpace(line) == pattern {
			return nil
		}
	}
	suffix := []byte("# roborev snapshots\n" + pattern + "\n")
	if len(existing) > 0 && existing[len(existing)-1] != '\n' {
		suffix = append([]byte{'\n'}, suffix...)
	}
	return suffix
}

func digest(data []byte) string {
	hash := sha256.Sum256(data)
	return hex.EncodeToString(hash[:])
}

func validExpected(data []byte) bool {
	// Existing .gitignore bytes are user-owned and may use a legacy or binary
	// encoding.  The operation preserves them byte-for-byte; UTF-8 validity is
	// therefore intentionally not part of this gate.
	return len(data) > 0 && int64(len(data)) <= MaxBytes
}

var errZeroProgress = errors.New("zero write progress")

// Append executes one append/create attempt. It never retries, truncates,
// rewrites, removes or rolls back user-owned .gitignore bytes.
func (s *Service) Append(ctx context.Context, plan Plan) Result {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return Result{Status: StatusPreMutationCanceled, Reason: ReasonCanceled}
	}
	if s == nil || s.FS == nil || !plan.Valid() || !validExpected(plan.expected) || plan.duplicateRule {
		return Result{Status: StatusPreMutationStale, Reason: ReasonInvalidPlan}
	}
	if !s.revalidatePlan(plan) {
		return Result{Status: StatusPreMutationStale, Reason: ReasonStale}
	}
	if err := ctx.Err(); err != nil {
		return Result{Status: StatusPreMutationCanceled, Reason: ReasonCanceled}
	}
	receipt := &Receipt{rootPath: plan.root, targetPath: plan.targetPath, rootSnapshot: plan.rootSnapshot, targetSnapshot: plan.target, expected: append([]byte(nil), plan.expected...), expectedHash: plan.expectedHash}
	if plan.target.Exists {
		handle, err := s.FS.OpenAppend(plan.targetPath)
		if err != nil || handle == nil {
			return Result{Status: StatusPreMutationStale, Reason: ReasonOpen, Receipt: receipt}
		}
		// The path snapshot is only an admission hint. Verify the actual
		// opened handle before the first byte is written so a replacement
		// cannot redirect an append into another file.
		info, statErr := handle.Stat()
		identity, identityErr := handle.Identity()
		if statErr != nil || info == nil || identityErr != nil || strings.TrimSpace(identity) == "" ||
			!info.Mode().IsRegular() || info.IsDir() || info.Size() != plan.target.Size || identity != plan.target.FileIdentity {
			_ = handle.Close()
			return Result{Status: StatusPreMutationStale, Reason: ReasonStale, Receipt: receipt}
		}
		receipt.targetIdentity = identity
		return s.appendHandle(handle, receipt, plan.suffix, true)
	}
	// A missing target must be created relative to a handle opened from the
	// preview-bound root. A full-path CREATE_NEW after this point could follow
	// a replacement root and create a file in an unapproved checkout.
	authority, err := s.FS.OpenRootCreateAuthority(plan.root)
	if err != nil || authority == nil {
		return Result{Status: StatusPreMutationStale, Reason: ReasonCreate, Receipt: receipt}
	}
	rootInfo, statErr := authority.Stat()
	rootIdentity, identityErr := authority.Identity()
	if statErr != nil || rootInfo == nil || !rootInfo.IsDir() || rootInfo.Mode()&os.ModeSymlink != 0 || s.FS.IsReparse(plan.root, rootInfo) || identityErr != nil || rootIdentity == "" || rootIdentity != plan.rootSnapshot.FileIdentity {
		_ = authority.Close()
		return Result{Status: StatusPreMutationStale, Reason: ReasonStale, Receipt: receipt}
	}
	currentRoot, rootErr := pathsafety.CaptureDirectory(s.FS, plan.root)
	if rootErr != nil || !currentRoot.Equal(plan.rootSnapshot) {
		_ = authority.Close()
		return Result{Status: StatusPreMutationStale, Reason: ReasonStale, Receipt: receipt}
	}
	handle, err := authority.CreateGitignoreExclusive()
	if err != nil || handle == nil {
		_ = authority.Close()
		return Result{Status: StatusPreMutationStale, Reason: ReasonCreate, Receipt: receipt}
	}
	result := s.appendHandle(handle, receipt, plan.expected, false)
	if closeErr := authority.Close(); closeErr != nil && result.Status == StatusVerifiedSuccess {
		result.Status = StatusFailedUnverified
		result.Reason = ReasonClose
		result.Warning = ""
	}
	return result
}

func (s *Service) revalidatePlan(plan Plan) bool {
	latest, err := s.Plan(context.Background(), plan.root, filepath.ToSlash(filepath.Join(snapshotRelative(plan.root, plan.snapshotDirs), "")))
	if err != nil {
		return false
	}
	return latest.Equal(plan)
}

func snapshotRelative(root string, dirs []directoryPlan) string {
	if len(dirs) == 0 {
		return "."
	}
	last := dirs[len(dirs)-1].path
	rel, err := filepath.Rel(root, last)
	if err != nil {
		return "."
	}
	return rel
}

func (s *Service) appendHandle(handle File, receipt *Receipt, content []byte, existing bool) Result {
	if handle == nil {
		return Result{Status: StatusFailedUnverified, Reason: ReasonOpen, Receipt: receipt}
	}
	var writeErr, syncErr, statErr, identityErr, closeErr error
	preInfo, preStatErr := handle.Stat()
	preIdentity, preIdentityErr := handle.Identity()
	if preStatErr != nil || preInfo == nil || preIdentityErr != nil || strings.TrimSpace(preIdentity) == "" ||
		!preInfo.Mode().IsRegular() || preInfo.IsDir() || preInfo.Size() != int64(len(receipt.expected)-len(content)) ||
		(existing && preIdentity != receipt.targetIdentity) {
		_ = handle.Close()
		if existing {
			return Result{Status: StatusPreMutationStale, Reason: ReasonStale, Receipt: receipt}
		}
		return Result{Status: StatusFailedUnverified, Reason: ReasonHandleIdentity, Receipt: receipt}
	}
	receipt.targetIdentity = preIdentity
	// This is the last path-chain guard immediately before the sole write. The
	// earlier plan revalidation protects the slow-probe boundary; this second
	// root witness closes the final root replacement window while the target
	// handle is already open.
	currentRoot, rootErr := pathsafety.CaptureDirectory(s.FS, receipt.rootPath)
	if rootErr != nil || !currentRoot.Equal(receipt.rootSnapshot) {
		_ = handle.Close()
		if existing {
			return Result{Status: StatusPreMutationStale, Reason: ReasonStale, Receipt: receipt}
		}
		return Result{Status: StatusFailedUnverified, Reason: ReasonStale, Receipt: receipt}
	}
	fullWrite := false
	// Exactly one bounded Write call is made. A short write is unverified even
	// when the handle reports nil error; it is never retried.
	n, err := handle.Write(content)
	if n < 0 || n > len(content) {
		writeErr = errors.New("invalid write progress")
	} else if n != len(content) {
		if n == 0 && err == nil {
			writeErr = errZeroProgress
		} else if err != nil {
			writeErr = err
		} else {
			writeErr = errors.New("short write")
		}
	} else if err != nil {
		writeErr = err
		fullWrite = true
	} else {
		fullWrite = true
	}
	if err := handle.Sync(); err != nil {
		syncErr = err
	}
	info, err := handle.Stat()
	if err != nil {
		statErr = err
	} else if info == nil || info.IsDir() || !info.Mode().IsRegular() || (existing && info.Size() < int64(len(receipt.expected)-len(content))) || info.Size() != int64(len(receipt.expected)) {
		statErr = errors.New("unexpected target handle")
	}
	identity, err := handle.Identity()
	if err != nil || strings.TrimSpace(identity) == "" || identity != preIdentity {
		identityErr = err
		if identityErr == nil {
			identityErr = errors.New("target identity changed")
		}
	}
	closeErr = handle.Close()
	if identityErr == nil {
		// Bind readback to the identity of the handle that actually accepted
		// the append. The target size/fingerprint necessarily change, while
		// the identity and parent witness must remain stable.
		receipt.targetIdentity = identity
		receipt.targetSnapshot = pathsafety.Snapshot{
			Path: receipt.targetPath, Exists: true, Kind: pathsafety.RegularFile,
			Size: int64(len(receipt.expected)), Fingerprint: digest(receipt.expected),
			FileIdentity: identity, AncestorPath: receipt.targetSnapshot.AncestorPath,
			AncestorIdentity: receipt.targetSnapshot.AncestorIdentity,
		}
	}
	// Keep the handle identity in the receipt only indirectly through target
	// revalidation; the final readback rejects replacements before success.
	verified := s.verifyReceipt(context.Background(), *receipt)
	// A short/zero write is never upgraded even if a malicious/injected
	// filesystem happens to expose the expected final bytes.  A full write
	// with a reported error may be upgraded after exact readback.
	if verified && (writeErr == nil || fullWrite) && statErr == nil && identityErr == nil {
		result := Result{Status: StatusVerifiedSuccess, Receipt: receipt}
		if writeErr != nil {
			result.Warning, result.Reason = WarningWrite, ReasonWrite
		} else if syncErr != nil {
			result.Warning, result.Reason = WarningSync, ReasonSync
		} else if closeErr != nil {
			result.Warning, result.Reason = WarningClose, ReasonClose
		}
		return result
	}
	if writeErr != nil {
		if errors.Is(writeErr, errZeroProgress) {
			return Result{Status: StatusFailedUnverified, Reason: ReasonZeroProgress, Receipt: receipt}
		}
		return Result{Status: StatusFailedUnverified, Reason: ReasonWrite, Receipt: receipt}
	}
	if syncErr != nil {
		return Result{Status: StatusFailedUnverified, Reason: ReasonSync, Receipt: receipt}
	}
	if statErr != nil {
		return Result{Status: StatusFailedUnverified, Reason: ReasonHandleStat, Receipt: receipt}
	}
	if identityErr != nil {
		return Result{Status: StatusFailedUnverified, Reason: ReasonHandleIdentity, Receipt: receipt}
	}
	if closeErr != nil {
		return Result{Status: StatusFailedUnverified, Reason: ReasonClose, Receipt: receipt}
	}
	return Result{Status: StatusFailedUnverified, Reason: ReasonReadbackMismatch, Receipt: receipt}
}

// Verify performs the independent exact readback used after post-Git probes.
func (s *Service) Verify(ctx context.Context, receipt Receipt) Result {
	if ctx == nil {
		ctx = context.Background()
	}
	// Verification is intentionally bounded by the detached caller's context.
	// ReadStableFile itself is synchronous, so check both sides of the stable
	// readback and never grant success after a deadline/cancellation arrives.
	if ctx.Err() == nil && s.verifyReceipt(ctx, receipt) && ctx.Err() == nil {
		return Result{Status: StatusVerifiedSuccess, Receipt: &receipt}
	}
	return Result{Status: StatusFailedUnverified, Reason: ReasonReadbackMismatch, Receipt: &receipt}
}

func (s *Service) verifyReceipt(ctx context.Context, receipt Receipt) bool {
	if ctx == nil {
		ctx = context.Background()
	}
	if ctx.Err() != nil {
		return false
	}
	if s == nil || s.FS == nil || receipt.rootPath == "" || receipt.targetPath == "" || !validExpected(receipt.expected) {
		return false
	}
	root, err := pathsafety.CaptureDirectory(s.FS, receipt.rootPath)
	if err != nil || !root.Equal(receipt.rootSnapshot) {
		return false
	}
	if ctx.Err() != nil {
		return false
	}
	data, target, err := pathsafety.ReadStableFile(s.FS, receipt.targetPath, MaxBytes)
	if ctx.Err() != nil {
		return false
	}
	if err != nil || !target.Exists || target.FileIdentity != receipt.targetIdentity || target.AncestorPath != receipt.targetSnapshot.AncestorPath || target.AncestorIdentity != receipt.targetSnapshot.AncestorIdentity ||
		target.Size != int64(len(receipt.expected)) || !bytes.Equal(data, receipt.expected) || digest(data) != receipt.expectedHash {
		return false
	}
	return ctx.Err() == nil
}
