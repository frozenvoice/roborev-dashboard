package app

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"github.com/froze/roborev-dashboard/internal/operation"
	"github.com/froze/roborev-dashboard/internal/roborev"
	"github.com/froze/roborev-dashboard/internal/roborevcompat"
	"github.com/froze/roborev-dashboard/internal/runtimecontrol"
)

type runtimeCommandDTO struct {
	Executable string   `json:"executable"`
	Args       []string `json:"args"`
}

type runtimeControlDTO struct {
	DaemonRunning   bool                         `json:"daemonRunning"`
	DaemonOperation string                       `json:"daemonOperation,omitempty"`
	OperationActive bool                         `json:"operationActive"`
	QueueState      string                       `json:"queueState"`
	CanPause        bool                         `json:"canPause"`
	CanResume       bool                         `json:"canResume"`
	CanStart        bool                         `json:"canStart"`
	CanStop         bool                         `json:"canStop"`
	CanRestart      bool                         `json:"canRestart"`
	DisabledReason  string                       `json:"disabledReason,omitempty"`
	Error           string                       `json:"error,omitempty"`
	StartCommand    runtimeCommandDTO            `json:"startCommand"`
	StopCommand     runtimeCommandDTO            `json:"stopCommand"`
	RestartCommand  runtimeCommandDTO            `json:"restartCommand"`
	DaemonCommands  map[string]runtimeCommandDTO `json:"daemonCommands"`
}

func (s *Server) runtimeControlStatus(w http.ResponseWriter, r *http.Request) {
	if s.Runtime == nil {
		writeError(w, http.StatusServiceUnavailable, "runtime controls are unavailable", "runtime-control")
		return
	}
	if err := s.requireRuntimeVersion(r.Context()); err != nil {
		state := s.Runtime.Snapshot()
		state.DisabledReason = err.Error()
		writeJSON(w, http.StatusOK, s.runtimeControlDTO(state))
		return
	}
	writeJSON(w, http.StatusOK, s.runtimeControlDTO(s.Runtime.Refresh(r.Context())))
}

func (s *Server) runtimeControlDTO(state runtimecontrol.State) runtimeControlDTO {
	queueState := "unknown"
	if state.DaemonRunning && state.QueueKnown {
		if state.QueuePaused {
			queueState = "paused"
		} else {
			queueState = "processing"
		}
	}
	reason := state.DisabledReason
	blocked := state.Active || state.DisabledReason != ""
	if s.Gate != nil {
		snapshot := s.Gate.SnapshotDetailed()
		switch {
		case snapshot.Setup:
			reason, blocked = "RoboRev Setup is active.", true
		case snapshot.Refine > 0:
			reason, blocked = "Panel Refine is active.", true
		case snapshot.Fixes > 0:
			reason, blocked = "A Fix operation is active.", true
		case snapshot.Mutations > 0:
			reason, blocked = "A RoboRev mutation is active.", true
		case snapshot.RuntimeControl:
			reason, blocked = "A runtime control operation is active.", true
		}
	}
	startArgs, _ := roborev.DaemonArgs("start")
	stopArgs, _ := roborev.DaemonArgs("stop")
	restartArgs, _ := roborev.DaemonArgs("restart")
	// The concrete executable is an internal implementation detail. The
	// browser receives only the semantic command preview; execution still
	// uses the server-owned, validated service binary.
	const executable = "roborev"
	return runtimeControlDTO{
		DaemonRunning: state.DaemonRunning, DaemonOperation: safeRuntimeOperation(state.Operation),
		OperationActive: state.Active, QueueState: queueState,
		CanPause:       !blocked && state.DaemonRunning && state.QueueKnown && !state.QueuePaused,
		CanResume:      !blocked && state.DaemonRunning && state.QueueKnown && state.QueuePaused,
		CanStart:       !blocked && state.DaemonKnown && !state.DaemonRunning,
		CanStop:        !blocked && state.DaemonKnown && state.DaemonRunning,
		CanRestart:     !blocked && state.DaemonKnown,
		DisabledReason: safeRuntimeReason(reason), Error: safeRuntimeError(state.Error),
		StartCommand:   runtimeCommandDTO{Executable: executable, Args: startArgs},
		StopCommand:    runtimeCommandDTO{Executable: executable, Args: stopArgs},
		RestartCommand: runtimeCommandDTO{Executable: executable, Args: restartArgs},
		DaemonCommands: map[string]runtimeCommandDTO{
			"start":   {Executable: executable, Args: startArgs},
			"stop":    {Executable: executable, Args: stopArgs},
			"restart": {Executable: executable, Args: restartArgs},
		},
	}
}

func safeRuntimeOperation(operation runtimecontrol.Operation) string {
	switch operation {
	case runtimecontrol.OperationStart, runtimecontrol.OperationStop,
		runtimecontrol.OperationRestart, runtimecontrol.OperationPause,
		runtimecontrol.OperationResume:
		return string(operation)
	default:
		return ""
	}
}

func safeRuntimeReason(reason string) string {
	switch reason {
	case "RoboRev Setup is active.", "Panel Refine is active.",
		"A Fix operation is active.", "A RoboRev mutation is active.",
		"A runtime control operation is active.":
		return reason
	case "":
		return ""
	default:
		return "Runtime control is unavailable."
	}
}

func safeRuntimeError(message string) string {
	if message == "" {
		return ""
	}
	return "Runtime control operation failed."
}

func (s *Server) requireRuntimeVersion(ctx context.Context) error {
	installation := s.RoboRev.Installation(ctx)
	if !installation.Installed || !roborevcompat.IsSupportedVersion(installation.Version) {
		version := installation.Version
		if version == "" {
			version = "unknown"
		}
		return fmt.Errorf("%w: runtime controls require RoboRev v0.64.0 (detected %s)", roborevcompat.ErrUnsupportedVersion, version)
	}
	return nil
}

func (s *Server) startDaemon(w http.ResponseWriter, r *http.Request) {
	s.daemonAction(w, r, runtimecontrol.OperationStart)
}
func (s *Server) stopDaemon(w http.ResponseWriter, r *http.Request) {
	s.daemonAction(w, r, runtimecontrol.OperationStop)
}
func (s *Server) restartDaemon(w http.ResponseWriter, r *http.Request) {
	s.daemonAction(w, r, runtimecontrol.OperationRestart)
}

func (s *Server) daemonAction(w http.ResponseWriter, r *http.Request, action runtimecontrol.Operation) {
	if !requireEmptyBody(w, r) {
		return
	}
	if s.Runtime == nil {
		writeError(w, http.StatusServiceUnavailable, "runtime controls are unavailable", "runtime-control")
		return
	}
	if err := s.requireRuntimeVersion(r.Context()); err != nil {
		writeRuntimeControlError(w, err, runtimeControlDTO{DisabledReason: err.Error()})
		return
	}
	var state runtimecontrol.State
	var err error
	switch action {
	case runtimecontrol.OperationStart:
		state, err = s.Runtime.Start(r.Context())
	case runtimecontrol.OperationStop:
		state, err = s.Runtime.Stop(r.Context())
	case runtimecontrol.OperationRestart:
		state, err = s.Runtime.Restart(r.Context())
	default:
		err = runtimecontrol.ErrUnsupported
	}
	if err != nil {
		writeRuntimeControlError(w, err, s.runtimeControlDTO(state))
		return
	}
	writeJSON(w, http.StatusAccepted, s.runtimeControlDTO(state))
}

func (s *Server) pauseQueue(w http.ResponseWriter, r *http.Request)  { s.queueAction(w, r, true) }
func (s *Server) resumeQueue(w http.ResponseWriter, r *http.Request) { s.queueAction(w, r, false) }

func (s *Server) queueAction(w http.ResponseWriter, r *http.Request, paused bool) {
	if !requireEmptyBody(w, r) {
		return
	}
	if s.Runtime == nil {
		writeError(w, http.StatusServiceUnavailable, "runtime controls are unavailable", "runtime-control")
		return
	}
	if err := s.requireRuntimeVersion(r.Context()); err != nil {
		writeRuntimeControlError(w, err, runtimeControlDTO{DisabledReason: err.Error()})
		return
	}
	state, err := s.Runtime.SetQueuePaused(r.Context(), paused)
	if err != nil {
		writeRuntimeControlError(w, err, s.runtimeControlDTO(state))
		return
	}
	writeJSON(w, http.StatusOK, s.runtimeControlDTO(state))
}

func writeRuntimeControlError(w http.ResponseWriter, err error, _ runtimeControlDTO) {
	status := http.StatusBadGateway
	switch {
	case errors.Is(err, operation.ErrBusy):
		status = http.StatusConflict
	case errors.Is(err, runtimecontrol.ErrDaemonOffline), errors.Is(err, runtimecontrol.ErrManagerClosed):
		status = http.StatusServiceUnavailable
	case errors.Is(err, roborevcompat.ErrUnsupportedVersion):
		status = http.StatusServiceUnavailable
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		status = http.StatusRequestTimeout
	}
	message := "Runtime control operation failed."
	switch {
	case errors.Is(err, operation.ErrBusy):
		message = "Another Dashboard operation is active."
	case errors.Is(err, runtimecontrol.ErrDaemonOffline):
		message = "RoboRev daemon is offline."
	case errors.Is(err, runtimecontrol.ErrManagerClosed):
		message = "Runtime controls are unavailable."
	case errors.Is(err, roborevcompat.ErrUnsupportedVersion):
		message = "RoboRev v0.64.0 is required."
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		message = "Runtime control request timed out."
	}
	writeError(w, status, message, "runtime-control")
}
