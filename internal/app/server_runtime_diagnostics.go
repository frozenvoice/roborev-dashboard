package app

import (
	"context"
	"errors"
	"net/http"

	"github.com/froze/roborev-dashboard/internal/roborev"
)

// runtimeDiagnostics is a global read-only daemon surface. Authorization and
// query validation deliberately precede the optional service assertion and
// invocation so rejected browser requests have zero adapter side effects.
func (s *Server) runtimeDiagnostics(w http.ResponseWriter, r *http.Request) {
	if !s.authorizeStrictBrowserRead(w, r) {
		return
	}
	if r.URL.RawQuery != "" {
		writeError(w, http.StatusBadRequest, "runtime diagnostics does not accept query parameters", "request")
		return
	}
	service, ok := s.RoboRev.(roborev.RuntimeDiagnosticsService)
	if !ok {
		writeError(w, http.StatusServiceUnavailable, "runtime diagnostics are unavailable", "runtime-diagnostics")
		return
	}
	diagnostics, err := service.RuntimeDiagnostics(r.Context())
	if err != nil {
		status := http.StatusBadGateway
		message := "runtime diagnostics are unavailable"
		switch {
		case errors.Is(err, context.DeadlineExceeded):
			status = http.StatusGatewayTimeout
			message = "runtime diagnostics timed out"
		case errors.Is(err, roborev.ErrRuntimeDiagnosticsOffline), errors.Is(err, roborev.ErrUnsupportedRuntimeDiagnostics), errors.Is(err, context.Canceled), errors.Is(err, roborev.ErrBindingChanged):
			status = http.StatusServiceUnavailable
			message = "runtime diagnostics are unavailable"
		case errors.Is(err, roborev.ErrMalformedRuntimeDiagnostics):
			status = http.StatusBadGateway
			message = "runtime diagnostics response is invalid"
		}
		writeError(w, status, message, "runtime-diagnostics")
		return
	}
	writeJSON(w, http.StatusOK, diagnostics)
}
