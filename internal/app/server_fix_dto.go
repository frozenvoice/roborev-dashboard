package app

import (
	"errors"
	"strconv"
	"time"

	"github.com/froze/roborev-dashboard/internal/fixoperation"
)

// fixCommandDTO is a display-only command projection. The executable that
// the manager resolved may be an absolute, user-private path; browser output
// is deliberately fixed to the product name. Arguments are rebuilt from the
// safe semantic job id, rather than copied from manager command state or any
// configured executable.
type fixCommandDTO struct {
	Executable string   `json:"executable"`
	Args       []string `json:"args"`
}

type fixPreviewDTO struct {
	RepositoryIdentity string              `json:"repositoryIdentity"`
	StartingHead       string              `json:"startingHead"`
	StartingBranch     string              `json:"startingBranch"`
	Target             fixoperation.Target `json:"target"`
	Agent              string              `json:"agent"`
	Command            fixCommandDTO       `json:"command"`
	PreviewToken       string              `json:"previewToken"`
}

// fixOperationDTO intentionally excludes RepositoryPath and Error. The
// manager keeps those values for its internal authority and postcondition
// bookkeeping, but an arbitrary process error may contain private paths or
// command output. ErrorCode is the stable, typed history signal safe for the
// browser. This projection also ensures manager-resolved binaries never cross
// the HTTP boundary.
type fixOperationDTO struct {
	ID                 string              `json:"id"`
	RepositoryIdentity string              `json:"repositoryIdentity"`
	Target             fixoperation.Target `json:"target"`
	Agent              string              `json:"agent"`
	Phase              fixoperation.Status `json:"phase"`
	Status             fixoperation.Status `json:"status,omitempty"`
	Command            fixCommandDTO       `json:"command"`
	StartingHead       string              `json:"startingHead"`
	StartingBranch     string              `json:"startingBranch"`
	NewHead            string              `json:"newHead,omitempty"`
	EndingHead         string              `json:"endingHead,omitempty"`
	EndingBranch       string              `json:"endingBranch,omitempty"`
	EndingDirty        bool                `json:"endingDirty,omitempty"`
	StartedAt          time.Time           `json:"startedAt"`
	UpdatedAt          time.Time           `json:"updatedAt"`
	CompletedAt        *time.Time          `json:"completedAt,omitempty"`
	ErrorCode          string              `json:"errorCode,omitempty"`
}

type fixOperationsResponseDTO struct {
	Operations []*fixOperationDTO `json:"operations"`
	Operation  *fixOperationDTO   `json:"operation,omitempty"`
}

func displayFixCommand(jobID int64) fixCommandDTO {
	return fixCommandDTO{
		Executable: "roborev",
		Args:       []string{"fix", "--agent", "codex", "--quiet", strconv.FormatInt(jobID, 10)},
	}
}

func toFixPreviewDTO(preview *fixoperation.Preview) fixPreviewDTO {
	if preview == nil {
		return fixPreviewDTO{}
	}
	return fixPreviewDTO{
		RepositoryIdentity: preview.RepositoryIdentity,
		StartingHead:       preview.StartingHead,
		StartingBranch:     preview.StartingBranch,
		Target:             preview.Target,
		Agent:              preview.Agent,
		Command:            displayFixCommand(preview.Target.JobID),
		PreviewToken:       preview.PreviewToken,
	}
}

func toFixOperationDTO(item *fixoperation.Operation) *fixOperationDTO {
	if item == nil {
		return nil
	}
	return &fixOperationDTO{
		ID:                 item.ID,
		RepositoryIdentity: item.RepositoryIdentity,
		Target:             item.Target,
		Agent:              item.Agent,
		Phase:              item.Phase,
		Status:             item.Status,
		Command:            displayFixCommand(item.Target.JobID),
		StartingHead:       item.StartingHead,
		StartingBranch:     item.StartingBranch,
		NewHead:            item.NewHead,
		EndingHead:         item.EndingHead,
		EndingBranch:       item.EndingBranch,
		EndingDirty:        item.EndingDirty,
		StartedAt:          item.StartedAt,
		UpdatedAt:          item.UpdatedAt,
		CompletedAt:        item.CompletedAt,
		ErrorCode:          item.ErrorCode,
	}
}

func toFixOperationsResponseDTO(response fixoperation.ListResponse) fixOperationsResponseDTO {
	operations := make([]*fixOperationDTO, 0, len(response.Operations))
	for _, item := range response.Operations {
		operations = append(operations, toFixOperationDTO(item))
	}
	return fixOperationsResponseDTO{Operations: operations, Operation: toFixOperationDTO(response.Operation)}
}

// safeFixError keeps manager diagnostics out of the HTTP contract. In
// particular, daemon/process errors can contain stderr, output, executable
// paths, or working-directory paths. The browser only needs a stable typed
// classification and may use errorCode from historical operations.
func safeFixError(err error) string {
	if err == nil {
		return "Fix operation failed"
	}
	var typed *fixoperation.Error
	if errors.As(err, &typed) {
		switch typed.Kind {
		case fixoperation.ErrorInvalid:
			return "Fix request is invalid"
		case fixoperation.ErrorForbidden:
			return "repository is not authorized"
		case fixoperation.ErrorNotFound:
			return "target job was not found"
		case fixoperation.ErrorConflict:
			return "Fix could not proceed because repository or target state changed"
		case fixoperation.ErrorUnavailable:
			return "Fix is unavailable"
		}
	}
	return "Fix operation failed"
}
