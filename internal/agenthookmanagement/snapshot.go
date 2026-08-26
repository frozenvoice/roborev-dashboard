// Package agenthookmanagement contains the narrow, server-side safety boundary
// for one RoboRev agent-hook configuration file. It intentionally has no
// knowledge of the upstream JSON schema or of browser requests.
package agenthookmanagement

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

const MaxConfigBytes int64 = 1 << 20

var (
	ErrInvalidPath       = errors.New("agent hook config path is invalid")
	ErrUnsafePath        = errors.New("agent hook config path is unsafe")
	ErrUnsafeConfig      = errors.New("agent hook config file is unsafe")
	ErrConfigChanged     = errors.New("agent hook config changed during inspection")
	ErrConfigTooLarge    = errors.New("agent hook config exceeds the size limit")
	ErrConfigUnavailable = errors.New("agent hook config is unavailable")
)

// File is the minimum race-aware file surface required by Capture.
type File interface {
	io.ReadCloser
	Stat() (os.FileInfo, error)
	// Identity returns the identity of this already-open handle. It must not
	// reopen the path, because the directory entry may be replaced after Open.
	Identity() (string, error)
}

// FileSystem is injectable so C:-style safety cases can be tested without
// touching a user's profile or any real agent configuration.
type FileSystem interface {
	Lstat(string) (os.FileInfo, error)
	Open(string) (File, error)
	EvalSymlinks(string) (string, error)
	Identity(string, os.FileInfo) (string, error)
	IsReparse(string, os.FileInfo) bool
}

type OSFileSystem struct{}

type osFile struct {
	*os.File
}

func (f *osFile) Identity() (string, error) {
	return handleIdentity(f.File)
}

func (OSFileSystem) Lstat(path string) (os.FileInfo, error) { return os.Lstat(path) }
func (OSFileSystem) Open(path string) (File, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	return &osFile{File: file}, nil
}
func (OSFileSystem) EvalSymlinks(path string) (string, error) {
	return filepath.EvalSymlinks(path)
}
func (OSFileSystem) Identity(path string, info os.FileInfo) (string, error) {
	return fileIdentity(path, info)
}
func (OSFileSystem) IsReparse(path string, info os.FileInfo) bool {
	return isReparsePoint(path, info)
}

type Snapshot struct {
	Path             string
	Exists           bool
	Fingerprint      string
	FileIdentity     string
	AncestorPath     string
	AncestorIdentity string
}

func (s Snapshot) Equal(other Snapshot) bool {
	return s == other
}

// Capture validates the complete destination chain and reads a bounded
// regular file through an open handle. A missing final file is safe only when
// its nearest existing ancestor is itself proven safe and identity-bound.
func Capture(fs FileSystem, rawPath string) (Snapshot, error) {
	if fs == nil {
		return Snapshot{}, ErrConfigUnavailable
	}
	path, err := canonicalAbsolutePath(fs, rawPath)
	if err != nil {
		return Snapshot{}, err
	}
	ancestorPath, ancestorIdentity, finalInfo, exists, err := inspectPathChain(fs, path)
	if err != nil {
		return Snapshot{}, err
	}
	snapshot := Snapshot{Path: path, Exists: exists, AncestorPath: ancestorPath, AncestorIdentity: ancestorIdentity}
	if !exists {
		latestAncestorPath, latestAncestorIdentity, _, latestExists, chainErr := inspectPathChain(fs, path)
		if chainErr != nil {
			return Snapshot{}, chainErr
		}
		if latestExists || !samePath(latestAncestorPath, ancestorPath) || latestAncestorIdentity != ancestorIdentity {
			return Snapshot{}, ErrConfigChanged
		}
		return snapshot, nil
	}
	if finalInfo.IsDir() || !finalInfo.Mode().IsRegular() || fs.IsReparse(path, finalInfo) {
		return Snapshot{}, ErrUnsafeConfig
	}
	if finalInfo.Size() < 0 || finalInfo.Size() > MaxConfigBytes {
		return Snapshot{}, ErrConfigTooLarge
	}
	identity, err := fs.Identity(path, finalInfo)
	if err != nil || strings.TrimSpace(identity) == "" {
		return Snapshot{}, ErrUnsafeConfig
	}
	file, err := fs.Open(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return Snapshot{}, ErrConfigChanged
		}
		return Snapshot{}, ErrConfigUnavailable
	}
	data, readErr := io.ReadAll(io.LimitReader(file, MaxConfigBytes+1))
	openedInfo, statErr := file.Stat()
	openedIdentity, identityErr := file.Identity()
	closeErr := file.Close()
	if readErr != nil || statErr != nil || identityErr != nil || closeErr != nil {
		return Snapshot{}, ErrConfigUnavailable
	}
	if int64(len(data)) > MaxConfigBytes {
		return Snapshot{}, ErrConfigTooLarge
	}
	if openedIdentity != identity || openedInfo.Size() != finalInfo.Size() {
		return Snapshot{}, ErrConfigChanged
	}
	latestAncestorPath, latestAncestorIdentity, latest, latestExists, chainErr := inspectPathChain(fs, path)
	if chainErr != nil {
		return Snapshot{}, chainErr
	}
	if !latestExists || latest == nil || !samePath(latestAncestorPath, ancestorPath) || latestAncestorIdentity != ancestorIdentity {
		return Snapshot{}, ErrConfigChanged
	}
	if latest.IsDir() || !latest.Mode().IsRegular() || fs.IsReparse(path, latest) {
		return Snapshot{}, ErrConfigChanged
	}
	latestIdentity, err := fs.Identity(path, latest)
	if err != nil || latestIdentity != identity || latest.Size() != int64(len(data)) {
		return Snapshot{}, ErrConfigChanged
	}
	hash := sha256.Sum256(data)
	snapshot.Fingerprint = hex.EncodeToString(hash[:])
	snapshot.FileIdentity = identity
	return snapshot, nil
}

func canonicalAbsolutePath(fs FileSystem, raw string) (string, error) {
	if strings.TrimSpace(raw) == "" {
		return "", ErrInvalidPath
	}
	if !filepath.IsAbs(raw) {
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
		return "", ErrConfigUnavailable
	}
	return clean, nil
}

func inspectPathChain(fs FileSystem, path string) (string, string, os.FileInfo, bool, error) {
	volume := filepath.VolumeName(path)
	remainder := strings.TrimPrefix(path, volume)
	separator := string(filepath.Separator)
	root := volume + separator
	if volume == "" {
		root = separator
	}
	parts := strings.FieldsFunc(remainder, func(r rune) bool { return r == '/' || r == '\\' })
	current := root
	ancestorPath := root
	rootInfo, rootErr := fs.Lstat(root)
	if rootErr != nil {
		if errors.Is(rootErr, os.ErrNotExist) {
			return "", "", nil, false, ErrUnsafePath
		}
		return "", "", nil, false, ErrConfigUnavailable
	}
	if fs.IsReparse(root, rootInfo) || rootInfo.Mode()&os.ModeSymlink != 0 || !rootInfo.IsDir() {
		return "", "", nil, false, ErrUnsafePath
	}
	ancestorIdentity, identityErr := fs.Identity(root, rootInfo)
	if identityErr != nil || strings.TrimSpace(ancestorIdentity) == "" {
		return "", "", nil, false, ErrUnsafePath
	}
	var finalInfo os.FileInfo
	exists := true
	for index, part := range parts {
		if part == "" || part == "." {
			continue
		}
		current = filepath.Join(current, part)
		info, err := fs.Lstat(current)
		if errors.Is(err, os.ErrNotExist) {
			exists = false
			break
		}
		if err != nil {
			return "", "", nil, false, ErrConfigUnavailable
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
	if !exists {
		if ancestorIdentity == "" {
			return "", "", nil, false, ErrUnsafePath
		}
		return ancestorPath, ancestorIdentity, nil, false, nil
	}
	if finalInfo == nil {
		return "", "", nil, false, fmt.Errorf("%w: empty path", ErrInvalidPath)
	}
	return ancestorPath, ancestorIdentity, finalInfo, true, nil
}

func samePath(left, right string) bool {
	left, right = filepath.Clean(left), filepath.Clean(right)
	if strings.EqualFold(filepath.VolumeName(left), filepath.VolumeName(right)) && strings.EqualFold(filepath.VolumeName(left), "") == false {
		return strings.EqualFold(left, right)
	}
	return left == right
}
