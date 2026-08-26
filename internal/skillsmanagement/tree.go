package skillsmanagement

import (
	"errors"
	"path/filepath"

	"github.com/froze/roborev-dashboard/internal/pathsafety"
)

type ManagedFileSnapshot struct {
	RelativePath string
	Snapshot     pathsafety.Snapshot
}

type TreeSnapshot struct {
	Agent              string
	Version            string
	ConfigDir          string
	SkillsDir          string
	ConfigRoot         pathsafety.Snapshot
	SkillsDirectory    pathsafety.Snapshot
	ManagedDirectories []pathsafety.Snapshot
	ManagedFiles       []ManagedFileSnapshot
	Legacy             pathsafety.Snapshot
	AggregateBytes     int64
}

func (s TreeSnapshot) Equal(other TreeSnapshot) bool {
	if s.Agent != other.Agent || s.Version != other.Version || s.ConfigDir != other.ConfigDir || s.SkillsDir != other.SkillsDir || s.ConfigRoot != other.ConfigRoot || s.SkillsDirectory != other.SkillsDirectory || s.Legacy != other.Legacy || s.AggregateBytes != other.AggregateBytes {
		return false
	}
	if len(s.ManagedDirectories) != len(other.ManagedDirectories) || len(s.ManagedFiles) != len(other.ManagedFiles) {
		return false
	}
	for i := range s.ManagedDirectories {
		if s.ManagedDirectories[i] != other.ManagedDirectories[i] {
			return false
		}
	}
	for i := range s.ManagedFiles {
		if s.ManagedFiles[i].RelativePath != other.ManagedFiles[i].RelativePath || s.ManagedFiles[i].Snapshot != other.ManagedFiles[i].Snapshot {
			return false
		}
	}
	return true
}

type Planner struct {
	FS       pathsafety.FileSystem
	Resolver TargetResolver
}

func NewPlanner(fs pathsafety.FileSystem, userHome string) Planner {
	resolver := NewTargetResolver(fs, userHome)
	return Planner{FS: fs, Resolver: resolver}
}

func (p Planner) fileSystem() pathsafety.FileSystem {
	if p.FS != nil {
		return p.FS
	}
	if p.Resolver.FS != nil {
		return p.Resolver.FS
	}
	return pathsafety.OSFileSystem{}
}

// CaptureStable performs two complete, deterministic captures before a
// preview snapshot is returned. The planner only touches paths in the exact
// manifest-derived write set and the legacy witness.
func (p Planner) CaptureStable(target Target) (TreeSnapshot, error) {
	first, err := p.captureOnce(target)
	if err != nil {
		return TreeSnapshot{}, err
	}
	second, err := p.captureOnce(target)
	if err != nil {
		return TreeSnapshot{}, err
	}
	if !first.Equal(second) {
		return TreeSnapshot{}, ErrTreeChanged
	}
	return second, nil
}

func (p Planner) Capture(target Target) (TreeSnapshot, error) {
	return p.captureOnce(target)
}

// Revalidate requires an unchanged preview-bound tree. It is the strict
// pre-execution check; use ValidatePostState for detached post-readback where
// managed files are expected to be created or replaced.
func (p Planner) Revalidate(before TreeSnapshot, target Target) (TreeSnapshot, error) {
	current, err := p.Capture(target)
	if err != nil {
		return TreeSnapshot{}, err
	}
	if !before.Equal(current) {
		return TreeSnapshot{}, ErrTreeChanged
	}
	return current, nil
}

func (p Planner) captureOnce(target Target) (TreeSnapshot, error) {
	if !IsSupportedAgent(target.Agent) || !filepath.IsAbs(target.ConfigDir) || !filepath.IsAbs(target.SkillsDir) {
		return TreeSnapshot{}, ErrRelativeTarget
	}
	fs := p.fileSystem()
	configRoot, err := pathsafety.CaptureDirectory(fs, target.ConfigDir)
	if err != nil {
		return TreeSnapshot{}, ErrConfigUnavailable
	}
	skillsDirectory, err := pathsafety.CapturePlannedDirectory(fs, target.SkillsDir)
	if err != nil {
		return TreeSnapshot{}, mapTreeError(err)
	}
	dirs, err := ManagedSkillDirectories(target.Agent)
	if err != nil {
		return TreeSnapshot{}, err
	}
	directorySnapshots := make([]pathsafety.Snapshot, 0, len(dirs))
	for _, rel := range dirs {
		snapshot, captureErr := pathsafety.CapturePlannedDirectory(fs, filepath.Join(target.SkillsDir, filepath.FromSlash(rel)))
		if captureErr != nil {
			return TreeSnapshot{}, mapTreeError(captureErr)
		}
		directorySnapshots = append(directorySnapshots, snapshot)
	}
	relativeFiles, err := ManagedRelativePaths(target.Agent)
	if err != nil {
		return TreeSnapshot{}, err
	}
	files := make([]ManagedFileSnapshot, 0, len(relativeFiles))
	var aggregate int64
	for _, rel := range relativeFiles {
		snapshot, captureErr := pathsafety.CaptureFile(fs, filepath.Join(target.SkillsDir, filepath.FromSlash(rel)))
		if captureErr != nil {
			return TreeSnapshot{}, mapTreeError(captureErr)
		}
		if snapshot.Exists {
			aggregate += snapshot.Size
			if aggregate > pathsafety.MaxAggregateBytes {
				return TreeSnapshot{}, ErrAggregateTooLarge
			}
		}
		files = append(files, ManagedFileSnapshot{RelativePath: rel, Snapshot: snapshot})
	}
	legacy, legacyErr := pathsafety.Capture(fs, filepath.Join(target.SkillsDir, "roborev-address"), pathsafety.AnyNode)
	if legacyErr != nil {
		return TreeSnapshot{}, ErrLegacyPresent
	}
	if legacy.Exists {
		return TreeSnapshot{}, ErrLegacyPresent
	}
	return TreeSnapshot{
		Agent: target.Agent, Version: Version, ConfigDir: target.ConfigDir, SkillsDir: target.SkillsDir,
		ConfigRoot: configRoot, SkillsDirectory: skillsDirectory, ManagedDirectories: directorySnapshots,
		ManagedFiles: files, Legacy: legacy, AggregateBytes: aggregate,
	}, nil
}

func mapTreeError(err error) error {
	switch {
	case errors.Is(err, pathsafety.ErrTooLarge):
		return ErrAggregateTooLarge
	case errors.Is(err, pathsafety.ErrChanged):
		return ErrTreeChanged
	case errors.Is(err, pathsafety.ErrUnsafePath), errors.Is(err, pathsafety.ErrUnsafeFile):
		return ErrTreeUnsafe
	default:
		return err
	}
}

// ValidatePostState permits expected managed-file creation/replacement while
// retaining the preview's config/skills parent identities and legacy absence.
func ValidatePostState(before, after TreeSnapshot) error {
	if before.Agent != after.Agent || before.Version != after.Version || before.ConfigDir != after.ConfigDir || before.SkillsDir != after.SkillsDir {
		return ErrTreeChanged
	}
	if before.ConfigRoot != after.ConfigRoot || before.ConfigRoot.FileIdentity != after.ConfigRoot.FileIdentity {
		return ErrTreeChanged
	}
	if after.Legacy.Exists || after.Legacy.Path != before.Legacy.Path || after.Legacy.Absence.Path != after.Legacy.Path || after.Legacy.Absence.AncestorIdentity == "" {
		return ErrLegacyPresent
	}
	if before.SkillsDirectory.Exists && (!after.SkillsDirectory.Exists || before.SkillsDirectory.FileIdentity != after.SkillsDirectory.FileIdentity) {
		return ErrTreeChanged
	}
	if len(before.ManagedDirectories) != len(after.ManagedDirectories) || len(before.ManagedFiles) != len(after.ManagedFiles) {
		return ErrTreeChanged
	}
	for i := range before.ManagedDirectories {
		oldDir, newDir := before.ManagedDirectories[i], after.ManagedDirectories[i]
		if oldDir.Path != newDir.Path {
			return ErrTreeChanged
		}
		if oldDir.Exists && (!newDir.Exists || oldDir.FileIdentity != newDir.FileIdentity) {
			return ErrTreeChanged
		}
	}
	for i := range before.ManagedFiles {
		if before.ManagedFiles[i].RelativePath != after.ManagedFiles[i].RelativePath {
			return ErrTreeChanged
		}
	}
	return nil
}
