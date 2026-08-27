package ciworkflowmanagement

import (
	"bytes"
	"context"
	"path/filepath"
	"strings"

	"github.com/froze/roborev-dashboard/internal/pathsafety"
)

// ExistingComparison is the complete browser-safe result of comparing the
// fixed existing workflow with one server-generated preview. It intentionally
// contains no content, path, digest, or filesystem error details.
type ExistingComparison string

const (
	ExistingExactMatch  ExistingComparison = "exact-match"
	ExistingDifferent   ExistingComparison = "different"
	ExistingUnavailable ExistingComparison = "unavailable"
)

// CompareExisting performs one bounded, stable, read-only comparison against
// the fixed target captured by an existing-target Plan. The plan remains the
// authority for target and ancestor identity: a race is unavailable, never a
// byte difference. Absent and unsafe plans return before opening the target.
func (s *Service) CompareExisting(ctx context.Context, plan Plan, previewBytes []byte) ExistingComparison {
	if ctx == nil {
		ctx = context.Background()
	}
	if ctx.Err() != nil || s == nil || s.FS == nil || !validComparisonPlan(plan) || !validContent(previewBytes) {
		return ExistingUnavailable
	}
	if !s.revalidateComparisonAncestors(plan) || ctx.Err() != nil {
		return ExistingUnavailable
	}

	// This is deliberately the only content read in this operation. The helper
	// proves regular-file, no-symlink/reparse, bounded-size, open-handle and
	// pre/post identity stability for the fixed target.
	existing, snapshot, err := pathsafety.ReadStableFile(s.FS, plan.target.path, MaxBytes)
	if err != nil || !snapshot.Equal(plan.target.snapshot) {
		return ExistingUnavailable
	}
	if !s.revalidateComparisonAncestors(plan) || ctx.Err() != nil {
		return ExistingUnavailable
	}
	if ctx.Err() != nil {
		return ExistingUnavailable
	}
	exact := bytes.Equal(existing, previewBytes)
	if ctx.Err() != nil {
		return ExistingUnavailable
	}
	if exact {
		return ExistingExactMatch
	}
	return ExistingDifferent
}

// validComparisonPlan validates the private topology before any target read.
// It accepts only a plan produced for an existing safe target; forged plans,
// absent targets, and unsafe ancestors fail closed.
func validComparisonPlan(plan Plan) bool {
	if plan.root == "" || strings.TrimSpace(plan.root) == "" || !filepath.IsAbs(plan.root) || filepath.Clean(plan.root) != plan.root {
		return false
	}
	if plan.target.status != TargetExists || plan.rootSnap.Path != plan.root || !validDirectorySnapshot(plan.rootSnap) {
		return false
	}
	githubPath := filepath.Join(plan.root, ".github")
	workflowsPath := filepath.Join(githubPath, "workflows")
	targetPath := filepath.Join(workflowsPath, "roborev.yml")
	if plan.github.status != nodePresent || plan.workflows.status != nodePresent ||
		plan.github.path != githubPath || plan.workflows.path != workflowsPath || plan.target.path != targetPath {
		return false
	}
	if !validDirectorySnapshot(plan.github.snapshot) || !validDirectorySnapshot(plan.workflows.snapshot) ||
		!comparisonSamePath(plan.github.snapshot.Path, plan.github.path) || !comparisonSamePath(plan.workflows.snapshot.Path, plan.workflows.path) {
		return false
	}
	target := plan.target.snapshot
	return target.Exists && target.Kind == pathsafety.RegularFile && target.Path == targetPath &&
		target.Size >= 0 && target.Size <= MaxBytes && strings.TrimSpace(target.FileIdentity) != "" &&
		target.AncestorPath == plan.workflows.path && strings.TrimSpace(target.AncestorIdentity) != "" &&
		target.Absence == (pathsafety.AbsenceWitness{}) && validFingerprint(target.Fingerprint)
}

func comparisonSamePath(left, right string) bool {
	left, right = filepath.Clean(left), filepath.Clean(right)
	if filepath.VolumeName(left) != "" || filepath.VolumeName(right) != "" {
		return strings.EqualFold(left, right)
	}
	return left == right
}

func validDirectorySnapshot(snapshot pathsafety.Snapshot) bool {
	return snapshot.Exists && snapshot.Kind == pathsafety.Directory && snapshot.Size == 0 && snapshot.Fingerprint == "" && snapshot.Absence == (pathsafety.AbsenceWitness{}) && strings.TrimSpace(snapshot.FileIdentity) != ""
}

func validFingerprint(value string) bool {
	if len(value) != 64 {
		return false
	}
	for _, char := range value {
		if !(char >= '0' && char <= '9' || char >= 'a' && char <= 'f') {
			return false
		}
	}
	return true
}

func (s *Service) revalidateComparisonAncestors(plan Plan) bool {
	root, rootErr := pathsafety.CaptureDirectory(s.FS, plan.root)
	github, githubErr := pathsafety.CaptureDirectory(s.FS, plan.github.path)
	workflows, workflowsErr := pathsafety.CaptureDirectory(s.FS, plan.workflows.path)
	return rootErr == nil && githubErr == nil && workflowsErr == nil &&
		root.Equal(plan.rootSnap) && github.Equal(plan.github.snapshot) && workflows.Equal(plan.workflows.snapshot)
}
