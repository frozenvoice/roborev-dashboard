// Package hookmanagement contains the server-side, fail-closed policy for
// the two RoboRev automation hooks. It intentionally does not execute or
// emulate hook mutations; the narrow CLI transport does that exactly once
// after this package's fresh preflight has passed.
package hookmanagement

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/froze/roborev-dashboard/internal/gitx"
	"github.com/froze/roborev-dashboard/internal/hookstatus"
)

const (
	PostCommit  = "post-commit"
	PostRewrite = "post-rewrite"
)

const (
	PostCommitMarker  = "post-commit hook v4"
	PostRewriteMarker = "post-rewrite hook v2"
)

type Action string

const (
	Install Action = "install"
	Repair  Action = "repair"
	Remove  Action = "remove"
)

func (a Action) Valid() bool { return a == Install || a == Repair || a == Remove }

type Classification string

const (
	Absent             Classification = "absent"
	Unmanaged          Classification = "unmanaged"
	ManagedCurrent     Classification = "managed-current"
	ManagedOutdated    Classification = "managed-outdated"
	ManagedStaleBinary Classification = "managed-stale-binary"
	Ambiguous          Classification = "ambiguous"
	Unreadable         Classification = "unreadable"
)

type Hook struct {
	Name                string            `json:"name"`
	Status              hookstatus.Status `json:"status"`
	Classification      Classification    `json:"classification"`
	Managed             bool              `json:"managed"`
	Shell               bool              `json:"shell"`
	BinaryCurrent       bool              `json:"binaryCurrent"`
	Fingerprint         string            `json:"-"`
	ResidualFingerprint string            `json:"-"`
	Present             bool              `json:"-"`
	Content             []byte            `json:"-"`
}

type Safety struct {
	HooksDirectorySafe            bool `json:"hooksDirectorySafe"`
	ConfigurationMutationRequired bool `json:"configurationMutationRequired"`
	UnmanagedHookPresent          bool `json:"unmanagedHookPresent"`
	NonShellHookPresent           bool `json:"nonShellHookPresent"`
	AmbiguousRoborevContent       bool `json:"ambiguousRoborevContent"`
	UnreadableHookPresent         bool `json:"unreadableHookPresent"`
}

type Availability struct {
	Available   bool   `json:"available"`
	Recommended bool   `json:"recommended,omitempty"`
	Reason      string `json:"reason,omitempty"`
}

type Preflight struct {
	RepositoryIdentity string       `json:"repositoryIdentity"`
	Install            Availability `json:"install"`
	Repair             Availability `json:"repair"`
	Remove             Availability `json:"remove"`
	Safety             Safety       `json:"safety"`
	PostCommit         Hook         `json:"postCommit"`
	PostRewrite        Hook         `json:"postRewrite"`
	SummaryCode        string       `json:"summaryCode"`
	WarningCodes       []string     `json:"warningCodes,omitempty"`

	// Internal binding facts. They have no JSON tags and are copied into the
	// opaque preview record by the app layer, never sent to the browser.
	LayoutDigest string `json:"-"`
	HooksDir     string `json:"-"`
	Root         string `json:"-"`
}

type LayoutResolver interface {
	ResolveHookLayout(context.Context, string) (gitx.HookLayout, error)
}

type Service struct {
	Layout LayoutResolver
	FS     hookstatus.FileSystem
}

func New(layout LayoutResolver, fs hookstatus.FileSystem) *Service {
	return &Service{Layout: layout, FS: fs}
}

func (s *Service) Inspect(ctx context.Context, identity, repoPath, binary string) (Preflight, error) {
	if s == nil || s.Layout == nil {
		return Preflight{RepositoryIdentity: identity}, errors.New("hook management is unavailable")
	}
	layout, err := s.Layout.ResolveHookLayout(ctx, repoPath)
	if err != nil {
		return Preflight{RepositoryIdentity: identity}, err
	}
	postCommit, err := s.inspectHook(filepath.Join(layout.HooksPath, PostCommit), PostCommit, binary)
	if err != nil && postCommit.Classification == "" {
		postCommit = Hook{Name: PostCommit, Status: hookstatus.Unknown, Classification: Unreadable}
	}
	postRewrite, err2 := s.inspectHook(filepath.Join(layout.HooksPath, PostRewrite), PostRewrite, binary)
	if err2 != nil && postRewrite.Classification == "" {
		postRewrite = Hook{Name: PostRewrite, Status: hookstatus.Unknown, Classification: Unreadable}
	}
	preflight := Preflight{RepositoryIdentity: identity, PostCommit: postCommit, PostRewrite: postRewrite, Root: layout.Root, HooksDir: layout.HooksPath}
	preflight.Safety = safetyFor(layout, postCommit, postRewrite)
	preflight.LayoutDigest = layoutDigest(layout)
	preflight.Install, preflight.Repair, preflight.Remove = availabilityFor(preflight)
	preflight.SummaryCode = summaryFor(preflight)
	preflight.WarningCodes = warningCodes(preflight)
	return preflight, errors.Join(err, err2)
}

func (s *Service) inspectHook(path, name, binary string) (Hook, error) {
	snapshot, err := hookstatus.ReadBounded(s.FS, path)
	hook := Hook{Name: name, Fingerprint: hex.EncodeToString(snapshot.Fingerprint[:]), Present: snapshot.Present, Content: snapshot.Content}
	if err != nil {
		hook.Status, hook.Classification = hookstatus.Unknown, Unreadable
		return hook, err
	}
	if !snapshot.Present {
		hook.Status, hook.Classification = hookstatus.NotInstalled, Absent
		return hook, nil
	}
	content := string(snapshot.Content)
	hook.Shell = isShellHook(content)
	marker := markerFor(name)
	markerCount := managedMarkerCount(content, name)
	genericMarkerCount := genericManagedMarkerCount(content)
	markerPresent := markerCount > 0
	if markerPresent {
		hook.Managed = true
		if markerCount != 1 || genericMarkerCount != 1 {
			hook.Status, hook.Classification = hookstatus.Unknown, Unreadable
			return hook, errors.New("managed hook blocks are ambiguous")
		}
		if residual, safe := RemoveResidual(content, name); safe {
			hook.ResidualFingerprint = digestBytes([]byte(residual))
		} else {
			hook.Status, hook.Classification = hookstatus.Unknown, Unreadable
			return hook, errors.New("managed hook block is ambiguous")
		}
		hook.BinaryCurrent = hookUsesBinary(content, binary)
		switch {
		case strings.Contains(content, marker) && hook.BinaryCurrent:
			hook.Status, hook.Classification = hookstatus.Current, ManagedCurrent
		case strings.Contains(content, marker):
			hook.Status, hook.Classification = hookstatus.Outdated, ManagedStaleBinary
		default:
			hook.Status, hook.Classification = hookstatus.Outdated, ManagedOutdated
		}
		return hook, nil
	}
	if genericMarkerCount > 0 {
		hook.Status, hook.Classification = hookstatus.Outdated, Ambiguous
		hook.ResidualFingerprint = digestBytes([]byte(normalizeResidual(content)))
		return hook, nil
	}
	if strings.Contains(strings.ToLower(content), "roborev") {
		hook.ResidualFingerprint = digestBytes([]byte(normalizeResidual(content)))
		hook.Status, hook.Classification = hookstatus.Outdated, Ambiguous
		return hook, nil
	}
	hook.ResidualFingerprint = digestBytes([]byte(normalizeResidual(content)))
	hook.Status, hook.Classification = hookstatus.NotInstalled, Unmanaged
	return hook, nil
}

func markerFor(name string) string {
	if name == PostCommit {
		return PostCommitMarker
	}
	return PostRewriteMarker
}

func hasManagedMarker(content, name string) bool {
	return managedMarkerCount(content, name) > 0
}

func managedMarkerCount(content, name string) int {
	// Ownership is version-independent. The upstream uninstall predicate
	// recognizes the fixed hook-name prefix, while current-version detection
	// below remains tied to the exact v0.64 marker text.
	prefix := "# roborev " + name + " hook"
	count := 0
	for _, line := range strings.Split(content, "\n") {
		if strings.HasPrefix(strings.ToLower(strings.TrimSpace(line)), prefix) {
			count++
		}
	}
	return count
}

func genericManagedMarkerCount(content string) int {
	return managedMarkerCount(content, PostCommit) + managedMarkerCount(content, PostRewrite)
}

func hookUsesBinary(content, binary string) bool {
	return strings.Contains(content, "ROBOREV="+strconv.Quote(binary))
}

func digestBytes(content []byte) string {
	if len(content) == 0 {
		return ""
	}
	h := sha256.Sum256(content)
	return hex.EncodeToString(h[:])
}

// normalizeResidual mirrors the fixed-file normalization performed by the
// upstream uninstall path: a shebang and whitespace-only remainder are not
// user content, while content without a terminal newline is compared after
// adding the newline the generated hook necessarily writes.
func normalizeResidual(content string) string {
	lines := strings.Split(content, "\n")
	if strings.TrimSpace(content) == "" {
		return ""
	}
	if len(lines) > 0 && strings.HasPrefix(strings.TrimSpace(lines[0]), "#!") && strings.TrimSpace(strings.Join(lines[1:], "\n")) == "" {
		return ""
	}
	if !strings.HasSuffix(content, "\n") {
		content += "\n"
	}
	return content
}

func isShellHook(content string) bool {
	first, _, _ := strings.Cut(content, "\n")
	first = strings.TrimSpace(first)
	for _, sh := range []string{"sh", "bash", "zsh", "ksh", "dash"} {
		if strings.HasPrefix(first, "#!/bin/"+sh) || strings.HasPrefix(first, "#!/usr/bin/env "+sh) {
			return true
		}
	}
	return false
}

func safetyFor(layout gitx.HookLayout, hooks ...Hook) Safety {
	safety := Safety{HooksDirectorySafe: layout.Safe && !layout.Ambiguous, ConfigurationMutationRequired: layout.EffectiveHooksPathIsRelative}
	for _, hook := range hooks {
		if hook.Classification == Unmanaged {
			safety.UnmanagedHookPresent = true
		}
		if hook.Present && !hook.Shell {
			safety.NonShellHookPresent = true
		}
		if hook.Classification == Ambiguous {
			safety.AmbiguousRoborevContent = true
		}
		if hook.Classification == Unreadable {
			safety.UnreadableHookPresent = true
		}
	}
	return safety
}

func availabilityFor(p Preflight) (Availability, Availability, Availability) {
	if !p.Safety.HooksDirectorySafe {
		return unavailable("unsafe-hooks-directory"), unavailable("unsafe-hooks-directory"), unavailable("unsafe-hooks-directory")
	}
	if p.Safety.UnreadableHookPresent {
		return unavailable("unreadable-hook"), unavailable("unreadable-hook"), unavailable("unreadable-hook")
	}
	if p.Safety.AmbiguousRoborevContent {
		// Uninstall's marker predicate is generic across the two fixed files;
		// an ambiguous companion could therefore be mistaken for a removable
		// upstream block. Keep every action fail-closed until it is understood.
		return unavailable("ambiguous-roborev-content"), unavailable("ambiguous-roborev-content"), unavailable("ambiguous-roborev-content")
	}
	nonShellManaged := (p.PostCommit.Managed && !p.PostCommit.Shell) || (p.PostRewrite.Managed && !p.PostRewrite.Shell)
	install := Availability{}
	repair := availableRepairRemove(p)
	if nonShellManaged {
		repair = unavailable("non-shell-hook")
	}
	if p.Safety.NonShellHookPresent {
		install = unavailable("non-shell-hook")
	} else if p.PostCommit.Classification == ManagedCurrent && p.PostRewrite.Classification == ManagedCurrent {
		install = unavailable("already-current")
	} else if p.PostCommit.Managed && p.PostRewrite.Managed && repair.Available {
		// Once both fixed files are already managed, Repair is the upstream
		// narrow operation and keeps Install from being used as a hidden
		// overwrite/upgrade fallback.
		install = unavailable("repair-recommended")
	} else {
		install = Availability{Available: true, Reason: "eligible"}
	}
	if p.Safety.ConfigurationMutationRequired && install.Available {
		// EnsureAbsoluteHooksPath is invoked only by upstream Install. Repair
		// and Remove do not normalize configuration, so they remain eligible
		// after all file/layout safety gates above pass.
		install = unavailable("relative-hooks-path-normalization")
	}
	remove := availableRemove(p)
	return install, repair, remove
}

func availableRepairRemove(p Preflight) Availability {
	if p.PostCommit.Classification == ManagedOutdated || p.PostCommit.Classification == ManagedStaleBinary || p.PostRewrite.Classification == ManagedOutdated || p.PostRewrite.Classification == ManagedStaleBinary {
		return Availability{Available: true, Recommended: true, Reason: "managed-hook-needs-repair"}
	}
	return unavailable("no-managed-hook-needs-repair")
}

func availableRemove(p Preflight) Availability {
	if p.PostCommit.Managed || p.PostRewrite.Managed {
		return Availability{Available: true, Reason: "managed-hook-present"}
	}
	return unavailable("no-managed-hook")
}

func unavailable(reason string) Availability { return Availability{Reason: reason} }

func summaryFor(p Preflight) string {
	if !p.Safety.HooksDirectorySafe || p.Safety.NonShellHookPresent || p.Safety.AmbiguousRoborevContent || p.Safety.UnreadableHookPresent {
		return "blocked"
	}
	if p.Repair.Available {
		return "repair-recommended"
	}
	if p.PostCommit.Classification == ManagedCurrent && p.PostRewrite.Classification == ManagedCurrent {
		return "managed-current"
	}
	if p.PostCommit.Classification == Absent && p.PostRewrite.Classification == Absent {
		return "not-installed"
	}
	return "attention"
}

func warningCodes(p Preflight) []string {
	var result []string
	if p.Safety.ConfigurationMutationRequired {
		result = append(result, "relative-hooks-path-normalization")
	}
	if p.Safety.NonShellHookPresent {
		result = append(result, "non-shell-hook")
	}
	if p.Safety.AmbiguousRoborevContent {
		result = append(result, "ambiguous-roborev-content")
	}
	if !p.Safety.HooksDirectorySafe {
		result = append(result, "unsafe-hooks-directory")
	}
	return result
}

func layoutDigest(layout gitx.HookLayout) string {
	h := sha256.New()
	fmt.Fprintf(h, "%s\x00%s\x00%s\x00%s\x00%s\x00%t\x00%t\x00%t\x00", layout.Root, layout.GitDir, layout.CommonDir, layout.HooksPath, layout.EffectiveHooksPath, layout.EffectiveHooksPathSet, layout.Safe, layout.EffectiveHooksPathIsRelative)
	return hex.EncodeToString(h.Sum(nil))
}

// FingerprintsChanged compares only the two fixed files and the layout facts.
func FingerprintsChanged(before, after Preflight) bool {
	return before.LayoutDigest != after.LayoutDigest || before.PostCommit.Fingerprint != after.PostCommit.Fingerprint || before.PostRewrite.Fingerprint != after.PostRewrite.Fingerprint
}

// Postcondition classifies a fresh readback after one CLI invocation.
type Outcome string

const (
	VerifiedSuccess  Outcome = "verified-success"
	Partial          Outcome = "partial"
	FailedUnverified Outcome = "failed-unverified"
)

func Verify(action Action, before, after Preflight) Outcome {
	return VerifyExecution(action, before, after, true)
}

// VerifyExecution classifies a readback while retaining the process-start
// fact. A process that never started cannot be credited for any state that an
// external actor may have changed into place.
func VerifyExecution(action Action, before, after Preflight, started bool) Outcome {
	if !started {
		return FailedUnverified
	}
	if !after.Safety.HooksDirectorySafe || after.Safety.UnreadableHookPresent {
		return FailedUnverified
	}
	if before.LayoutDigest != after.LayoutDigest {
		return FailedUnverified
	}
	if action != Remove && after.Safety.AmbiguousRoborevContent {
		return FailedUnverified
	}
	success := func(h Hook) bool { return h.Classification == ManagedCurrent }
	preserved := residualsPreserved(before, after)
	switch action {
	case Install:
		if success(after.PostCommit) && success(after.PostRewrite) && preserved {
			return VerifiedSuccess
		}
		if preserved && (transitionedToCurrent(before.PostCommit, after.PostCommit) || transitionedToCurrent(before.PostRewrite, after.PostRewrite)) {
			return Partial
		}
	case Repair:
		pcOK := !before.PostCommit.Managed || success(after.PostCommit)
		rwOK := !before.PostRewrite.Managed || success(after.PostRewrite)
		if pcOK && rwOK && preserved {
			return VerifiedSuccess
		}
		if preserved && (repairedTarget(before.PostCommit, after.PostCommit) || repairedTarget(before.PostRewrite, after.PostRewrite)) {
			return Partial
		}
	case Remove:
		pcOK := !after.PostCommit.Managed
		rwOK := !after.PostRewrite.Managed
		if pcOK && rwOK && preserved {
			return VerifiedSuccess
		}
		if preserved && (removedTarget(before.PostCommit, after.PostCommit) || removedTarget(before.PostRewrite, after.PostRewrite)) {
			return Partial
		}
	}
	return FailedUnverified
}

func residualsPreserved(before, after Preflight) bool {
	return before.PostCommit.ResidualFingerprint == after.PostCommit.ResidualFingerprint && before.PostRewrite.ResidualFingerprint == after.PostRewrite.ResidualFingerprint
}

func transitionedToCurrent(before, after Hook) bool {
	return before.Classification != ManagedCurrent && after.Classification == ManagedCurrent
}

func repairedTarget(before, after Hook) bool {
	return before.Managed && before.Classification != ManagedCurrent && after.Classification == ManagedCurrent
}

func removedTarget(before, after Hook) bool {
	return before.Managed && !after.Managed
}

// RemoveResidual verifies the exact upstream generated block parser's key
// postcondition without exposing content. It is intentionally conservative:
// if the marker exists but the block contains an unknown line, removal cannot
// be proven safe and the caller must fail closed.
func RemoveResidual(content, hookName string) (string, bool) {
	if managedMarkerCount(content, hookName) != 1 || genericManagedMarkerCount(content) != 1 {
		return content, false
	}
	lines := strings.Split(content, "\n")
	start := -1
	prefix := "# roborev " + hookName + " hook"
	for i, line := range lines {
		if strings.HasPrefix(strings.ToLower(strings.TrimSpace(line)), prefix) {
			start = i
			break
		}
	}
	if start < 0 {
		return content, false
	}
	end := start
	inIf, inFunc := false, false
	for i := start + 1; i < len(lines); i++ {
		trimmed := strings.TrimSpace(lines[i])
		if trimmed == "" {
			if i+1 < len(lines) && isSnippetLine(lines[i+1]) {
				end = i
				continue
			}
			break
		}
		if isSnippetLine(trimmed) {
			end = i
			if strings.HasPrefix(trimmed, "if ") {
				inIf = true
			}
			if strings.HasSuffix(trimmed, "() {") {
				inFunc = true
			}
			continue
		}
		if trimmed == "fi" && inIf {
			end = i
			inIf = false
			continue
		}
		if trimmed == "}" && inFunc {
			end = i
			inFunc = false
			continue
		}
		break
	}
	remaining := append(append([]string{}, lines[:start]...), lines[end+1:]...)
	result := strings.Join(remaining, "\n")
	residualLines := strings.Split(result, "\n")
	if len(residualLines) > 0 && strings.HasPrefix(strings.TrimSpace(residualLines[0]), "#!") {
		residualLines = residualLines[1:]
	}
	if strings.TrimSpace(strings.Join(residualLines, "\n")) == "" {
		return "", true
	}
	if !strings.HasSuffix(result, "\n") {
		result += "\n"
	}
	return result, true
}

func isSnippetLine(line string) bool {
	line = strings.TrimSpace(line)
	return strings.HasPrefix(line, "ROBOREV=") || strings.HasPrefix(line, "ROBOREV=$(") ||
		commandPrefix(line, "\"$ROBOREV\" post-commit") || commandPrefix(line, "\"$ROBOREV\" remap --quiet") ||
		commandPrefix(line, "\"$ROBOREV\" enqueue --quiet") || commandPrefix(line, "roborev post-commit") ||
		commandPrefix(line, "roborev enqueue") || commandPrefix(line, "roborev remap") ||
		strings.HasPrefix(line, "if [ ! -x \"$ROBOREV\"") || strings.HasPrefix(line, "if [ -z \"$ROBOREV\"") ||
		strings.HasPrefix(line, "[ -z \"$ROBOREV\"") || strings.HasPrefix(line, "[ ! -x \"$ROBOREV\"") ||
		line == "return 0" || strings.HasPrefix(line, "_roborev_hook") || strings.HasPrefix(line, "_roborev_remap")
}

func commandPrefix(line, prefix string) bool {
	if !strings.HasPrefix(line, prefix) {
		return false
	}
	if len(line) == len(prefix) {
		return true
	}
	next := line[len(prefix)]
	return next == ' ' || next == '\t' || next == '>' || next == '|' || next == '&' || next == ';'
}
