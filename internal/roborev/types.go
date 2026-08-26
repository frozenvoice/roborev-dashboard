package roborev

import (
	"context"
	"encoding/json"

	"github.com/froze/roborev-dashboard/internal/insights"
	"github.com/froze/roborev-dashboard/internal/repoinventory"
	"github.com/froze/roborev-dashboard/internal/repomerge"
	"github.com/froze/roborev-dashboard/internal/repomove"
	"github.com/froze/roborev-dashboard/internal/reporename"
)

// GitHookAction is the intentionally tiny mutation transport vocabulary.
// It is separate from Service so existing RoboRev fakes remain source
// compatible; management is available only when an adapter implements the
// optional interface below.
type GitHookAction string

const (
	GitHookInstall GitHookAction = "install"
	GitHookRepair  GitHookAction = "repair"
	GitHookRemove  GitHookAction = "remove"
)

type GitHookBinding struct {
	Binary     string
	Version    string
	Generation uint64
	FileID     string
}

type GitHookExecution struct {
	Started  bool
	ExitCode int
}

// GitHookTransport is optional and deliberately narrower than Service. The
// binding probe is fresh (uncached) and RunGitHook rechecks it immediately
// before one typed CLI launch.
type GitHookTransport interface {
	ProbeGitHookBinding(context.Context) (GitHookBinding, error)
	RunGitHook(context.Context, string, GitHookAction, GitHookBinding) (GitHookExecution, error)
}

type Installation struct {
	Installed    bool            `json:"installed"`
	Binary       string          `json:"binary,omitempty"`
	Version      string          `json:"version,omitempty"`
	Status       json.RawMessage `json:"status,omitempty"`
	Error        string          `json:"error,omitempty"`
	FailureCode  string          `json:"failureCode,omitempty"`
	Capabilities Capabilities    `json:"capabilities"`
	// DaemonHealth is an internal typed probe result. It is intentionally
	// hidden from bootstrap JSON; browser callers consume the bounded Status
	// summary instead.
	DaemonHealth DaemonHealthProbeState `json:"-"`
	// DaemonCondition is the typed health condition for a reachable daemon.
	// It is intentionally hidden from browser bootstrap JSON.
	DaemonCondition DaemonHealthCondition `json:"-"`
	// DaemonVersion is the exact version reported by the daemon health probe.
	// It is intentionally hidden from bootstrap JSON.
	DaemonVersion string `json:"-"`
}

type DaemonHealthProbeState string

const (
	DaemonHealthUnknown DaemonHealthProbeState = "unknown"
	DaemonHealthOnline  DaemonHealthProbeState = "online"
	DaemonHealthOffline DaemonHealthProbeState = "offline"
	DaemonHealthInvalid DaemonHealthProbeState = "invalid"
)

type DaemonHealthCondition string

const (
	DaemonConditionUnknown   DaemonHealthCondition = "unknown"
	DaemonConditionHealthy   DaemonHealthCondition = "healthy"
	DaemonConditionUnhealthy DaemonHealthCondition = "unhealthy"
)

type Capabilities struct {
	ReviewEnqueueAPI   bool `json:"reviewEnqueueApi"`
	FrozenRerunAPI     bool `json:"frozenRerunApi"`
	JobListAPI         bool `json:"jobListApi"`
	JobOutputStatusAPI bool `json:"jobOutputStatusApi"`
	PanelReview        bool `json:"panelReview"`
	FixCommand         bool `json:"fixCommand"`
	PanelMembersAPI    bool `json:"panelMembersApi"`
	PromptLazyLoad     bool `json:"promptLazyLoad"`
	RawLog             bool `json:"rawLog"`
	StreamEvents       bool `json:"streamEvents"`
	// AnalyzeIntrospection reports only the supported v0.64 catalog/prompt
	// inspection surface. v0.64 analysis enqueue is intentionally not exposed
	// by the Dashboard because upstream runs those jobs in agentic mode.
	AnalyzeIntrospection bool `json:"analyzeIntrospection"`
	// Configuration introspection/mutation cover the bounded, allowlisted
	// `config list --show-origin` / `config get` / `config set` CLI surface.
	// Raw or sensitive config editing is intentionally not exposed.
	ConfigIntrospection bool `json:"configIntrospection"`
	ConfigMutation      bool `json:"configMutation"`
	SummaryStats        bool `json:"summaryStats"`
	CostStats           bool `json:"costStats"`
	GuidelineInsights   bool `json:"guidelineInsights"`
	RepositoryInventory bool `json:"repositoryInventory"`
	RepositoryRename    bool `json:"repositoryRename"`
	RepositoryMove      bool `json:"repositoryMove"`
	RepositoryMerge     bool `json:"repositoryMerge"`
}

// RepositoryInventoryService is deliberately optional: the base Service
// interface is shared by existing review/task consumers and must not acquire
// a new read surface that would break legacy fakes.
type RepositoryInventoryService interface {
	RepositoryInventory(context.Context) (repoinventory.Inventory, error)
}

// RepositoryRenameService is optional so existing Service fakes and legacy
// adapters remain source-compatible. Implementations must preflight again at
// the process boundary and must not infer success from human CLI output.
type RepositoryRenameService interface {
	RepositoryRenamePreflight(context.Context, reporename.Request) (reporename.Preflight, error)
	RepositoryRename(context.Context, reporename.Request, reporename.Preflight) (reporename.ExecutionResult, error)
}

// RepositoryMoveService is optional so existing Service fakes remain source
// compatible. Implementations must perform a fresh process-boundary
// validation and one authoritative inventory readback after a started CLI.
type RepositoryMoveService interface {
	RepositoryMovePreflight(context.Context, repomove.Request) (repomove.Preflight, error)
	RepositoryMove(context.Context, repomove.Request, repomove.Preflight) (repomove.ExecutionResult, error)
}

// RepositoryMergeService is optional so the long-lived base Service remains
// source-compatible with existing fakes. Implementations must revalidate the
// complete source/target inventory and active-job safety facts immediately
// before admitting the one typed CLI mutation.
type RepositoryMergeService interface {
	RepositoryMergeEligibility(context.Context, string) (repomerge.Eligibility, error)
	RepositoryMergePreflight(context.Context, repomerge.Request) (repomerge.Preflight, error)
	RepositoryMerge(context.Context, repomerge.Request, repomerge.Preflight) (repomerge.ExecutionResult, error)
}

// LazyContentAuthorizationService is the narrow, content-safe lazy-read
// surface used by the HTTP handlers.  It is deliberately optional so the
// long-lived Service interface remains source-compatible with existing
// adapters and fakes.  Implementations must authorize the job against the
// supplied registered repository snapshot before fetching prompt/log bytes,
// and must keep that authorization and content read on one daemon binding.
type LazyContentAuthorizationService interface {
	AuthorizedPrompt(context.Context, int64, []string) (string, error)
	AuthorizedLog(context.Context, int64, []string) (string, error)
}

// InsightsService is deliberately optional: the base Service interface is
// shared by existing review/task consumers and must not acquire a new
// lifecycle operation. Implementations must preflight again immediately
// before launching and must return an unverified result for any post-launch
// ambiguity because v0.64 enqueues a daemon job before returning its receipt.
type InsightsService interface {
	InsightsPreflight(context.Context, insights.Request) (insights.Preflight, error)
	Insights(context.Context, insights.Request, insights.Preflight) (insights.LaunchResult, error)
}

// RuntimeDiagnosticsService is deliberately optional so the long-lived
// Service interface remains source-compatible with existing adapters and
// fakes. It exposes only the Dashboard-owned, sanitized diagnostics DTO; the
// upstream status and health payloads never cross this boundary.
type RuntimeDiagnosticsService interface {
	RuntimeDiagnostics(context.Context) (RuntimeDiagnostics, error)
}

type RuntimeDiagnostics struct {
	Daemon  RuntimeDiagnosticsDaemon  `json:"daemon"`
	Workers RuntimeDiagnosticsWorkers `json:"workers"`
	Queue   RuntimeDiagnosticsQueue   `json:"queue"`
	Jobs    RuntimeDiagnosticsJobs    `json:"jobs"`
	Health  RuntimeDiagnosticsHealth  `json:"health"`
}

type RuntimeDiagnosticsDaemon struct {
	Running bool   `json:"running"`
	Version string `json:"version"`
	Uptime  string `json:"uptime"`
}

type RuntimeDiagnosticsWorkers struct {
	Active int64 `json:"active"`
	Max    int64 `json:"max"`
}

type RuntimeDiagnosticsQueue struct {
	Paused  bool  `json:"paused"`
	Queued  int64 `json:"queued"`
	Running int64 `json:"running"`
}

type RuntimeDiagnosticsJobs struct {
	Completed int64 `json:"completed"`
	Failed    int64 `json:"failed"`
	Skipped   int64 `json:"skipped"`
}

type RuntimeDiagnosticsHealth struct {
	Healthy       bool                          `json:"healthy"`
	ErrorCount24h int64                         `json:"errorCount24h"`
	Components    []RuntimeDiagnosticsComponent `json:"components"`
}

type RuntimeDiagnosticsComponent struct {
	Name    string `json:"name"`
	Healthy bool   `json:"healthy"`
}

type JobMetadata struct {
	ID           int64  `json:"id"`
	RepoPath     string `json:"repo_path"`
	GitRef       string `json:"git_ref"`
	Branch       string `json:"branch"`
	PanelName    string `json:"panel_name"`
	PanelRunUUID string `json:"panel_run_uuid"`
	PanelRole    string `json:"panel_role"`
	JobType      string `json:"job_type"`
	Status       string `json:"status"`
	Verdict      string `json:"verdict"`
	Skipped      bool   `json:"skipped"`
	// Closed is a pointer because v0.64's job API makes closure an explicit
	// nullable field. Callers that authorize agentic fixes must reject a
	// missing value instead of treating it as false.
	Closed *bool `json:"closed"`
}

// PanelEnqueueResponse is the exact v0.64 panel synthesis receipt returned by
// /api/enqueue. Unknown fields are tolerated for forward compatibility, but
// every identity field below is required and validated by the adapter.
type PanelEnqueueResponse struct {
	ID           int64  `json:"id"`
	RepoPath     string `json:"repo_path"`
	GitRef       string `json:"git_ref"`
	Branch       string `json:"branch"`
	PanelName    string `json:"panel_name"`
	PanelRunUUID string `json:"panel_run_uuid"`
	PanelRole    string `json:"panel_role"`
	JobType      string `json:"job_type"`
	Skipped      bool   `json:"skipped"`
}

type SynthesisStatus struct {
	JobMetadata
}

// JobOutputStatus is the bounded status prefix returned by RoboRev v0.64's
// /api/job/output endpoint. The endpoint may contain very large output lines;
// the adapter deliberately reads only through the status prefix.
type JobOutputStatus struct {
	JobID  int64  `json:"job_id"`
	Status string `json:"status"`
}

type ReviewRequest struct {
	RepoPath string `json:"repoPath"`
	GitRef   string `json:"gitRef"`
	Branch   string `json:"branch,omitempty"`
	Panel    string `json:"panel,omitempty"`
}

// FixRequest identifies one terminal v0.64 review job to fix. RepoPath is
// used only as the process working directory; findings and prompts are never
// passed to the command line. FixArgs is the sole source of the argv contract.
type FixRequest struct {
	RepoPath string
	JobID    int64
	Agent    string
}

type CommentRequest struct {
	JobID   int64  `json:"jobId"`
	Comment string `json:"comment"`
}
