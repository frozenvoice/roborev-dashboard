package app

import (
	"strings"

	"github.com/froze/roborev-dashboard/internal/refine"
)

// Panel Refine keeps its full state machine in the manager. These DTOs are an
// app-layer privacy boundary: repository paths, resolved binaries, and raw
// process/daemon diagnostics never become browser data.
type panelRefineCommandDTO struct {
	Executable string   `json:"executable"`
	Args       []string `json:"args"`
}

type panelRefineIterationDTO struct {
	Number       int    `json:"number"`
	TargetHead   string `json:"targetHead"`
	Panel        string `json:"panel"`
	ReviewJobID  int64  `json:"reviewJobId"`
	PanelRunUUID string `json:"panelRunUUID"`
	ReviewResult string `json:"reviewResult"`
	FixAttempted bool   `json:"fixAttempted"`
	FixAgent     string `json:"fixAgent,omitempty"`
	HeadAfterFix string `json:"headAfterFix,omitempty"`
	ErrorCode    string `json:"errorCode,omitempty"`
}

type panelRefineCancelStateDTO struct {
	Available bool   `json:"available"`
	Head      string `json:"head,omitempty"`
	Branch    string `json:"branch,omitempty"`
	Dirty     bool   `json:"dirty,omitempty"`
	ErrorCode string `json:"errorCode,omitempty"`
}

type panelRefineRunDTO struct {
	ID                  string                     `json:"id"`
	RepositoryIdentity  string                     `json:"repositoryIdentity"`
	Branch              string                     `json:"branch"`
	IterationPanel      string                     `json:"iterationPanel"`
	FinalPanel          string                     `json:"finalPanel,omitempty"`
	FixAgent            string                     `json:"fixAgent"`
	MaxFixAttempts      int                        `json:"maxFixAttempts"`
	FixAttempts         int                        `json:"fixAttempts"`
	Phase               refine.Phase               `json:"phase"`
	StartingHead        string                     `json:"startingHead"`
	CurrentHead         string                     `json:"currentHead"`
	CurrentReviewJobID  int64                      `json:"currentReviewJobId,omitempty"`
	CurrentPanelRunUUID string                     `json:"currentPanelRunUUID,omitempty"`
	StartedAt           string                     `json:"startedAt"`
	UpdatedAt           string                     `json:"updatedAt"`
	CompletedAt         *string                    `json:"completedAt,omitempty"`
	ErrorCode           string                     `json:"errorCode,omitempty"`
	StopReason          string                     `json:"stopReason,omitempty"`
	Iterations          []panelRefineIterationDTO  `json:"iterations"`
	Command             panelRefineCommandDTO      `json:"command"`
	CancelState         *panelRefineCancelStateDTO `json:"cancelState,omitempty"`
}

type panelRefineOptionsDTO struct {
	Executable            string   `json:"executable"`
	Args                  []string `json:"args"`
	AllowedFixAgents      []string `json:"allowedFixAgents"`
	DefaultFixAgent       string   `json:"defaultFixAgent"`
	DefaultMaxFixAttempts int      `json:"defaultMaxFixAttempts"`
	MinFixAttempts        int      `json:"minFixAttempts"`
	MaxFixAttempts        int      `json:"maxFixAttempts"`
}

type panelRefineResponseDTO struct {
	Runs    []*panelRefineRunDTO  `json:"runs"`
	Run     *panelRefineRunDTO    `json:"run,omitempty"`
	Options panelRefineOptionsDTO `json:"options"`
}

func semanticPanelRefineCommand() panelRefineCommandDTO {
	return panelRefineCommandDTO{Executable: "roborev", Args: []string{"fix", "--agent", "codex", "--quiet", "<synthesisJobID>"}}
}

func panelRefineOptionsDTOFrom(_ refine.Options) panelRefineOptionsDTO {
	// Options are intentionally rebuilt from the fixed Dashboard contract;
	// manager-provided executable and arbitrary args are never serialized.
	return panelRefineOptionsDTO{
		Executable: "roborev", Args: []string{"fix", "--agent", "codex", "--quiet", "<synthesisJobID>"},
		AllowedFixAgents: []string{"codex"}, DefaultFixAgent: "codex", DefaultMaxFixAttempts: 3,
		MinFixAttempts: 1, MaxFixAttempts: 10,
	}
}

func safePanelRefineStopReason(reason refine.StopReason) string {
	switch reason {
	case refine.StopMaximumFixAttempts, refine.StopRepositoryChanged,
		refine.StopFixDidNotCommit, refine.StopFixLeftDirty,
		refine.StopReviewFailed, refine.StopFixFailed,
		refine.StopMalformedReviewReceipt, refine.StopMalformedReviewStatus,
		refine.StopCancelled:
		return string(reason)
	default:
		return ""
	}
}

func panelRefineErrorCode(message string) string {
	if strings.TrimSpace(message) == "" {
		return ""
	}
	return "panel_refine_error"
}

func panelRefineRunDTOFrom(run *refine.PanelRefineRun) *panelRefineRunDTO {
	if run == nil {
		return nil
	}
	result := &panelRefineRunDTO{
		ID: run.ID, RepositoryIdentity: run.RepositoryIdentity, Branch: run.Branch,
		IterationPanel: run.IterationPanel, FinalPanel: run.FinalPanel,
		FixAgent: run.FixAgent, MaxFixAttempts: run.MaxFixAttempts,
		FixAttempts: run.FixAttempts, Phase: run.Phase,
		StartingHead: run.StartingHead, CurrentHead: run.CurrentHead,
		CurrentReviewJobID: run.CurrentReviewJobID, CurrentPanelRunUUID: run.CurrentPanelRunUUID,
		StartedAt: run.StartedAt.UTC().Format("2006-01-02T15:04:05.999999999Z07:00"),
		UpdatedAt: run.UpdatedAt.UTC().Format("2006-01-02T15:04:05.999999999Z07:00"),
		ErrorCode: panelRefineErrorCode(run.Error), StopReason: safePanelRefineStopReason(run.StopReason),
		Iterations: make([]panelRefineIterationDTO, 0, len(run.Iterations)), Command: semanticPanelRefineCommand(),
	}
	if run.CompletedAt != nil {
		completed := run.CompletedAt.UTC().Format("2006-01-02T15:04:05.999999999Z07:00")
		result.CompletedAt = &completed
	}
	for _, iteration := range run.Iterations {
		result.Iterations = append(result.Iterations, panelRefineIterationDTO{
			Number: iteration.Number, TargetHead: iteration.TargetHead, Panel: iteration.Panel,
			ReviewJobID: iteration.ReviewJobID, PanelRunUUID: iteration.PanelRunUUID,
			ReviewResult: iteration.ReviewResult, FixAttempted: iteration.FixAttempted,
			FixAgent: iteration.FixAgent, HeadAfterFix: iteration.HeadAfterFix,
			ErrorCode: panelRefineErrorCode(iteration.Error),
		})
	}
	if state := run.CancelState; state != nil {
		result.CancelState = &panelRefineCancelStateDTO{
			Available: state.Available, Head: state.Head, Branch: state.Branch,
			Dirty: state.Dirty, ErrorCode: func() string {
				if state.Error == "" {
					return ""
				}
				return "repository_state_unavailable"
			}(),
		}
	}
	return result
}

func panelRefineResponseDTOFrom(response refine.ListResponse) panelRefineResponseDTO {
	result := panelRefineResponseDTO{Runs: make([]*panelRefineRunDTO, 0, len(response.Runs)), Options: panelRefineOptionsDTOFrom(response.Options)}
	for _, run := range response.Runs {
		result.Runs = append(result.Runs, panelRefineRunDTOFrom(run))
	}
	result.Run = panelRefineRunDTOFrom(response.Run)
	return result
}
