package app

import (
	"context"
	"errors"
	"net/http"

	"github.com/froze/roborev-dashboard/internal/roborev"
)

// agentHookReadiness is a global, read-only probe. Browser authorization and
// query rejection intentionally happen before optional adapter lookup or any
// executable/version process work.
func (s *Server) agentHookReadiness(w http.ResponseWriter, r *http.Request) {
	if !s.authorizeStrictBrowserRead(w, r) {
		return
	}
	if r.URL.RawQuery != "" {
		writeError(w, http.StatusBadRequest, "agent hook readiness does not accept query parameters", "request")
		return
	}
	service, ok := s.RoboRev.(roborev.AgentHookReadinessService)
	if !ok {
		writeError(w, http.StatusServiceUnavailable, "agent hook readiness is unavailable", "agent-hook-readiness")
		return
	}
	readiness, err := service.AgentHookReadiness(r.Context())
	if err != nil {
		status := http.StatusBadGateway
		message := "agent hook readiness is unavailable"
		switch {
		case errors.Is(err, context.DeadlineExceeded):
			status = http.StatusGatewayTimeout
			message = "agent hook readiness timed out"
		case errors.Is(err, context.Canceled), errors.Is(err, roborev.ErrBindingChanged), errors.Is(err, roborev.ErrAgentHookReadinessBinding):
			status = http.StatusServiceUnavailable
		}
		writeError(w, status, message, "agent-hook-readiness")
		return
	}
	writeJSON(w, http.StatusOK, readiness)
}
