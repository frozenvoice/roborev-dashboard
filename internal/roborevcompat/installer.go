package roborevcompat

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

type Installer struct {
	Downloader Downloader
	Home       string
	Manifest   Manifest
	Progress   func(phase string, percent int)
	GOOS       string
	GOARCH     string
}

func (i *Installer) SetProgress(progress func(string, int)) { i.Progress = progress }

func (i Installer) Target() string {
	home := i.Home
	if home == "" {
		home, _ = os.UserHomeDir()
	}
	return ManagedTarget(home)
}

func (i Installer) Install(ctx context.Context) (string, error) {
	manifest := i.Manifest
	if manifest.URL == "" {
		manifest = Manifest064()
	}
	if i.Downloader.HTTP == nil && !isPinnedManifest(manifest) {
		return "", fmt.Errorf("%w: %w: installer manifest is not the pinned v0.64.0 release", ErrInstallPolicy, ErrUnsupportedVersion)
	}
	goos, goarch := i.GOOS, i.GOARCH
	if goos == "" {
		goos = runtime.GOOS
	}
	if goarch == "" {
		goarch = runtime.GOARCH
	}
	if err := RequireSupportedPlatform(goos, goarch); err != nil {
		return "", err
	}
	target := i.Target()
	if target == "" {
		return "", fmt.Errorf("%w: user home directory is unavailable", ErrDownloadContract)
	}
	if err := ensureSafeComponents(filepath.Dir(target)); err != nil {
		return "", err
	}
	if _, err := os.Lstat(target); err == nil {
		return "", fmt.Errorf("%w: %s", ErrExistingBinary, target)
	} else if !os.IsNotExist(err) {
		return "", fmt.Errorf("%w: inspect target: %v", ErrExistingBinary, err)
	}
	if i.Progress != nil {
		i.Progress("downloading", 20)
	}
	archive, err := i.Downloader.Download(ctx, manifest)
	if err != nil {
		return "", err
	}
	if i.Progress != nil {
		i.Progress("verifying", 55)
	}
	if i.Progress != nil {
		i.Progress("extracting", 70)
	}
	executable, err := ExtractExecutable(archive, manifest)
	if err != nil {
		return "", err
	}
	parent := filepath.Dir(target)
	staging, err := os.MkdirTemp(parent, ".roborev-staging-")
	if err != nil {
		return "", fmt.Errorf("create RoboRev staging directory: %w", err)
	}
	defer os.RemoveAll(staging)
	if err := ensureSafeComponents(parent); err != nil {
		return "", err
	}
	stagePath := filepath.Join(staging, filepath.Base(target))
	file, err := os.OpenFile(stagePath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o700)
	if err != nil {
		return "", fmt.Errorf("create staged RoboRev executable: %w", err)
	}
	writeErr := func() error {
		if _, err := file.Write(executable); err != nil {
			return err
		}
		if err := file.Sync(); err != nil {
			return err
		}
		return file.Close()
	}()
	if writeErr != nil {
		_ = file.Close()
		return "", fmt.Errorf("write staged RoboRev executable: %w", writeErr)
	}
	if i.Progress != nil {
		i.Progress("installing", 85)
	}
	// Re-check the destination immediately before publication. This closes the
	// window where a directory component could be replaced after staging.
	if err := ensureSafeComponents(parent); err != nil {
		return "", err
	}
	if err := noOverwriteRename(stagePath, target); err != nil {
		return "", fmt.Errorf("install RoboRev executable without overwriting existing files: %w", err)
	}
	return target, nil
}

func noOverwriteRename(source, target string) error {
	if _, err := os.Lstat(target); err == nil {
		return ErrExistingBinary
	} else if !os.IsNotExist(err) {
		return err
	}
	// A hard-link create is atomic and fails with EEXIST instead of replacing
	// a target that appeared after the preflight check. Both paths are in the
	// same staging parent, so this is the no-overwrite equivalent of a move.
	if err := os.Link(source, target); err != nil {
		if errors.Is(err, os.ErrExist) {
			return ErrExistingBinary
		}
		return err
	}
	if err := os.Remove(source); err != nil {
		return err
	}
	return nil
}

func ensureSafeComponents(path string) error {
	clean := filepath.Clean(path)
	volume := filepath.VolumeName(clean)
	remainder := strings.TrimPrefix(clean, volume)
	parts := strings.FieldsFunc(remainder, func(r rune) bool { return r == '/' || r == '\\' })
	current := volume + string(filepath.Separator)
	if volume == "" {
		current = string(filepath.Separator)
	}
	for _, part := range parts {
		if part == "." || part == "" {
			continue
		}
		current = filepath.Join(current, part)
		info, err := os.Lstat(current)
		if err != nil {
			if os.IsNotExist(err) {
				if err := os.Mkdir(current, 0o700); err != nil && !os.IsExist(err) {
					return err
				}
				continue
			}
			return fmt.Errorf("inspect install directory: %w", err)
		}
		if isUnsafeDirectory(current, info) || !info.IsDir() {
			return fmt.Errorf("%w: install path component is not a real directory: %s", ErrInstallPolicy, current)
		}
	}
	return nil
}
