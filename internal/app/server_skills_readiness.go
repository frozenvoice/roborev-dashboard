package app

import (
	"context"
	"errors"
	"net/http"

	"github.com/froze/roborev-dashboard/internal/roborev"
)

// skillsReadiness is a global, read-only probe. Authorization and exact query
// rejection happen before optional adapter lookup or any RoboRev process work.
func (s *Server) skillsReadiness(w http.ResponseWriter, r *http.Request) {
	if !s.authorizeStrictBrowserRead(w, r) {
		return
	}
	if r.URL.RawQuery != "" {
		writeError(w, http.StatusBadRequest, "skills readiness does not accept query parameters", "request")
		return
	}
	service, ok := s.RoboRev.(roborev.SkillsReadinessService)
	if !ok {
		writeError(w, http.StatusServiceUnavailable, "skills readiness is unavailable", "skills-readiness")
		return
	}
	readiness, err := service.SkillsReadiness(r.Context())
	if err != nil {
		status := http.StatusBadGateway
		message := "skills readiness is unavailable"
		switch {
		case errors.Is(err, context.DeadlineExceeded):
			status = http.StatusGatewayTimeout
			message = "skills readiness timed out"
		case errors.Is(err, context.Canceled), errors.Is(err, roborev.ErrBindingChanged), errors.Is(err, roborev.ErrSkillsReadinessBinding):
			status = http.StatusServiceUnavailable
		case errors.Is(err, roborev.ErrMalformedSkillsReadinessOutput):
			status = http.StatusBadGateway
			message = "skills readiness response is invalid"
		}
		writeError(w, status, message, "skills-readiness")
		return
	}
	if err := roborev.ValidateSkillsReadiness(readiness); err != nil {
		writeError(w, http.StatusBadGateway, "skills readiness response is invalid", "skills-readiness")
		return
	}
	writeJSON(w, http.StatusOK, readiness)
}
