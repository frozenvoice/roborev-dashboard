// Package pathsafety contains the race-aware, server-side filesystem
// primitives used by mutation previews.  It intentionally does not enumerate
// directories or retain file contents.
package pathsafety

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

const (
	MaxFileBytes      int64 = 1 << 20
	MaxAggregateBytes int64 = 8 << 20
)

var (
	ErrInvalidPath = errors.New("path is invalid")
	ErrUnsafePath  = errors.New("path is unsafe")
	ErrUnsafeFile  = errors.New("file is unsafe")
	ErrChanged     = errors.New("path changed during inspection")
	ErrTooLarge    = errors.New("file exceeds the size limit")
	ErrUnavailable = errors.New("filesystem is unavailable")
	ErrMissing     = errors.New("required path is missing")
)

// File is an already-open file handle. Identity must describe that handle;
// implementations must not reopen the path after Open.
type File interface {
	io.ReadCloser
	Stat() (os.FileInfo, error)
	Identity() (string, error)
}

// FileSystem is injectable so Windows C:-style race cases can be tested
// without touching a user's profile.
type FileSystem interface {
	Lstat(string) (os.FileInfo, error)
	Open(string) (File, error)
	EvalSymlinks(string) (string, error)
	Identity(string, os.FileInfo) (string, error)
	IsReparse(string, os.FileInfo) bool
}

type OSFileSystem struct{}

type osFile struct{ *os.File }

func (f *osFile) Identity() (string, error)                 { return handleIdentity(f.File) }
func (OSFileSystem) Lstat(path string) (os.FileInfo, error) { return os.Lstat(path) }
func (OSFileSystem) Open(path string) (File, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	return &osFile{File: file}, nil
}
func (OSFileSystem) EvalSymlinks(path string) (string, error) { return filepath.EvalSymlinks(path) }
func (OSFileSystem) Identity(path string, info os.FileInfo) (string, error) {
	return fileIdentity(path, info)
}
func (OSFileSystem) IsReparse(path string, info os.FileInfo) bool {
	return isReparsePoint(path, info)
}

// HandleIdentity and FileIdentity expose the platform-specific identity
// primitives for other server-side safety packages without exposing handles
// or paths to browser-facing code.
func HandleIdentity(file *os.File) (string, error) { return handleIdentity(file) }
func FileIdentity(path string, info os.FileInfo) (string, error) {
	return fileIdentity(path, info)
}
func IsReparsePoint(path string, info os.FileInfo) bool {
	return isReparsePoint(path, info)
}

type NodeKind uint8

const (
	AnyNode NodeKind = iota
	RegularFile
	Directory
)

// AbsenceWitness proves that Path was absent beneath the identified nearest
// existing ancestor at capture time. It is deliberately explicit so callers
// cannot mistake a missing target for an uninspected target.
type AbsenceWitness struct {
	Path             string
	AncestorPath     string
	AncestorIdentity string
}

// Snapshot contains bounded metadata only. Fingerprint is SHA-256 of a
// regular file's bounded bytes; raw contents are never retained.
type Snapshot struct {
	Path             string
	Exists           bool
	Kind             NodeKind
	Size             int64
	Fingerprint      string
	FileIdentity     string
	AncestorPath     string
	AncestorIdentity string
	Absence          AbsenceWitness
}

func (s Snapshot) Equal(other Snapshot) bool { return s == other }

// CaptureFile captures a regular file, allowing the final node to be absent
// with an explicit ancestor witness. Existing files are read through an open
// handle and checked again after the read.
func CaptureFile(fs FileSystem, rawPath string) (Snapshot, error) {
	return Capture(fs, rawPath, RegularFile)
}

// CaptureDirectory captures an existing directory. Use CapturePlannedDirectory
// when a directory is an allowed future write target and may be absent.
func CaptureDirectory(fs FileSystem, rawPath string) (Snapshot, error) {
	snapshot, err := Capture(fs, rawPath, Directory)
	if err != nil {
		return Snapshot{}, err
	}
	if !snapshot.Exists {
		return Snapshot{}, ErrMissing
	}
	return snapshot, nil
}

// CapturePlannedDirectory captures a directory that may be absent. If it is
// absent, its nearest safe ancestor is identity-bound.
func CapturePlannedDirectory(fs FileSystem, rawPath string) (Snapshot, error) {
	return Capture(fs, rawPath, Directory)
}

// Capture captures a bounded file or directory node. Any missing final node
// is represented by Absence; missing intermediate nodes are represented by
// the nearest existing ancestor in the same witness.
func Capture(fs FileSystem, rawPath string, want NodeKind) (Snapshot, error) {
	if fs == nil {
		return Snapshot{}, ErrUnavailable
	}
	path, err := CanonicalAbsolutePath(fs, rawPath)
	if err != nil {
		return Snapshot{}, err
	}
	ancestorPath, ancestorIdentity, finalInfo, exists, err := inspectPathChain(fs, path)
	if err != nil {
		return Snapshot{}, err
	}
	snapshot := Snapshot{
		Path:             path,
		Exists:           exists,
		Kind:             want,
		AncestorPath:     ancestorPath,
		AncestorIdentity: ancestorIdentity,
	}
	if !exists {
		latestAncestorPath, latestAncestorIdentity, _, latestExists, chainErr := inspectPathChain(fs, path)
		if chainErr != nil {
			return Snapshot{}, chainErr
		}
		if latestExists || !samePath(latestAncestorPath, ancestorPath) || latestAncestorIdentity != ancestorIdentity {
			return Snapshot{}, ErrChanged
		}
		snapshot.Absence = AbsenceWitness{Path: path, AncestorPath: ancestorPath, AncestorIdentity: ancestorIdentity}
		return snapshot, nil
	}

	if fs.IsReparse(path, finalInfo) || finalInfo.Mode()&os.ModeSymlink != 0 {
		return Snapshot{}, ErrUnsafePath
	}
	if want == Directory && !finalInfo.IsDir() {
		return Snapshot{}, ErrUnsafePath
	}
	if want == RegularFile && (finalInfo.IsDir() || !finalInfo.Mode().IsRegular()) {
		return Snapshot{}, ErrUnsafeFile
	}
	if want == AnyNode {
		if finalInfo.IsDir() {
			want = Directory
		} else if finalInfo.Mode().IsRegular() {
			want = RegularFile
		} else {
			return Snapshot{}, ErrUnsafePath
		}
		snapshot.Kind = want
	}
	identity, err := fs.Identity(path, finalInfo)
	if err != nil || strings.TrimSpace(identity) == "" {
		return Snapshot{}, ErrUnsafePath
	}
	if want == Directory {
		snapshot.FileIdentity = identity
		latestAncestorPath, latestAncestorIdentity, latest, latestExists, chainErr := inspectPathChain(fs, path)
		if chainErr != nil {
			return Snapshot{}, chainErr
		}
		if !latestExists || latest == nil || !latest.IsDir() || fs.IsReparse(path, latest) || latestAncestorIdentity != ancestorIdentity || !samePath(latestAncestorPath, ancestorPath) {
			return Snapshot{}, ErrChanged
		}
		latestIdentity, identityErr := fs.Identity(path, latest)
		if identityErr != nil || latestIdentity != identity {
			return Snapshot{}, ErrChanged
		}
		return snapshot, nil
	}

	if finalInfo.Size() < 0 || finalInfo.Size() > MaxFileBytes {
		return Snapshot{}, ErrTooLarge
	}
	file, err := fs.Open(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return Snapshot{}, ErrChanged
		}
		return Snapshot{}, ErrUnavailable
	}
	data, readErr := io.ReadAll(io.LimitReader(file, MaxFileBytes+1))
	openedInfo, statErr := file.Stat()
	openedIdentity, identityErr := file.Identity()
	closeErr := file.Close()
	if readErr != nil || statErr != nil || identityErr != nil || closeErr != nil {
		return Snapshot{}, ErrUnavailable
	}
	if int64(len(data)) > MaxFileBytes {
		return Snapshot{}, ErrTooLarge
	}
	if openedIdentity != identity || openedInfo.Size() != finalInfo.Size() {
		return Snapshot{}, ErrChanged
	}
	latestAncestorPath, latestAncestorIdentity, latest, latestExists, chainErr := inspectPathChain(fs, path)
	if chainErr != nil {
		return Snapshot{}, chainErr
	}
	if !latestExists || latest == nil || latest.IsDir() || !latest.Mode().IsRegular() || fs.IsReparse(path, latest) || !samePath(latestAncestorPath, ancestorPath) || latestAncestorIdentity != ancestorIdentity {
		return Snapshot{}, ErrChanged
	}
	latestIdentity, identityErr := fs.Identity(path, latest)
	if identityErr != nil || latestIdentity != identity || latest.Size() != int64(len(data)) {
		return Snapshot{}, ErrChanged
	}
	hash := sha256.Sum256(data)
	snapshot.Fingerprint = hex.EncodeToString(hash[:])
	snapshot.Size = int64(len(data))
	snapshot.FileIdentity = identity
	return snapshot, nil
}

// CanonicalAbsolutePath proves an existing final node is not a symlink or
// reparse point and returns a cleaned absolute path. Missing final nodes are
// retained as lexical paths so their absence witness can be captured.
func CanonicalAbsolutePath(fs FileSystem, raw string) (string, error) {
	if fs == nil {
		return "", ErrUnavailable
	}
	if strings.TrimSpace(raw) == "" || !filepath.IsAbs(raw) {
		return "", ErrInvalidPath
	}
	abs, err := filepath.Abs(raw)
	if err != nil || !filepath.IsAbs(abs) {
		return "", ErrInvalidPath
	}
	clean := filepath.Clean(abs)
	if info, statErr := fs.Lstat(clean); statErr == nil {
		if fs.IsReparse(clean, info) || info.Mode()&os.ModeSymlink != 0 {
			return "", ErrUnsafePath
		}
		resolved, resolveErr := fs.EvalSymlinks(clean)
		if resolveErr != nil || !samePath(clean, resolved) {
			return "", ErrUnsafePath
		}
	} else if !errors.Is(statErr, os.ErrNotExist) {
		return "", ErrUnavailable
	}
	return clean, nil
}

func inspectPathChain(fs FileSystem, path string) (string, string, os.FileInfo, bool, error) {
	volume := filepath.VolumeName(path)
	remainder := strings.TrimPrefix(path, volume)
	root := volume + string(filepath.Separator)
	if volume == "" {
		root = string(filepath.Separator)
	}
	parts := strings.FieldsFunc(remainder, func(r rune) bool { return r == '/' || r == '\\' })
	current, ancestorPath := root, root
	rootInfo, rootErr := fs.Lstat(root)
	if rootErr != nil {
		if errors.Is(rootErr, os.ErrNotExist) {
			return "", "", nil, false, ErrUnsafePath
		}
		return "", "", nil, false, ErrUnavailable
	}
	if rootInfo.Mode()&os.ModeSymlink != 0 || fs.IsReparse(root, rootInfo) || !rootInfo.IsDir() {
		return "", "", nil, false, ErrUnsafePath
	}
	ancestorIdentity, identityErr := fs.Identity(root, rootInfo)
	if identityErr != nil || strings.TrimSpace(ancestorIdentity) == "" {
		return "", "", nil, false, ErrUnsafePath
	}
	var finalInfo os.FileInfo
	for index, part := range parts {
		if part == "" || part == "." {
			continue
		}
		current = filepath.Join(current, part)
		info, err := fs.Lstat(current)
		if errors.Is(err, os.ErrNotExist) {
			return ancestorPath, ancestorIdentity, nil, false, nil
		}
		if err != nil {
			return "", "", nil, false, ErrUnavailable
		}
		if fs.IsReparse(current, info) || info.Mode()&os.ModeSymlink != 0 {
			return "", "", nil, false, ErrUnsafePath
		}
		isFinal := index == len(parts)-1
		if !isFinal && !info.IsDir() {
			return "", "", nil, false, ErrUnsafePath
		}
		identity, identityErr := fs.Identity(current, info)
		if identityErr != nil || strings.TrimSpace(identity) == "" {
			return "", "", nil, false, ErrUnsafePath
		}
		if isFinal {
			finalInfo = info
		} else {
			ancestorPath, ancestorIdentity = current, identity
		}
	}
	if finalInfo == nil {
		return "", "", nil, false, fmt.Errorf("%w: empty path", ErrInvalidPath)
	}
	return ancestorPath, ancestorIdentity, finalInfo, true, nil
}

func samePath(left, right string) bool {
	left, right = filepath.Clean(left), filepath.Clean(right)
	if filepath.VolumeName(left) != "" || filepath.VolumeName(right) != "" {
		return strings.EqualFold(left, right)
	}
	return left == right
}
