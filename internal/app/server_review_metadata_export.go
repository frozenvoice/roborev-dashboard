package app

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strings"

	"github.com/froze/roborev-dashboard/internal/roborev"
)

const reviewMetadataExportFilenameMax = 64

// reviewMetadataExport is an explicit, repository-scoped read. The order of
// operations is security-sensitive: browser authorization, strict query
// validation, opaque registry authorization, fresh root inspection, and only
// then the optional RoboRev export adapter.
func (s *Server) reviewMetadataExport(w http.ResponseWriter, r *http.Request) {
	if !s.authorizeStrictBrowserRead(w, r) {
		return
	}
	query, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil {
		writeError(w, http.StatusBadRequest, "repositoryIdentity is required", "repository")
		return
	}
	identities, ok := query["repositoryIdentity"]
	if !ok || len(query) != 1 || len(identities) != 1 || strings.TrimSpace(identities[0]) == "" {
		writeError(w, http.StatusBadRequest, "repositoryIdentity is required", "repository")
		return
	}
	identity := identities[0]
	registeredPath, err := s.Registry.ResolveIdentity(identity)
	if err != nil {
		writeError(w, http.StatusForbidden, "repository is not authorized", "repository")
		return
	}
	freshRepository, err := s.Registry.Inspect(r.Context(), registeredPath)
	if err != nil {
		writeError(w, http.StatusConflict, "repository state changed; refresh and retry", "repository")
		return
	}
	service, ok := s.RoboRev.(roborev.ReviewMetadataExportService)
	if !ok {
		writeError(w, http.StatusServiceUnavailable, "review metadata export is unavailable", "review-export")
		return
	}
	result, err := service.ReviewMetadataExport(r.Context(), freshRepository.Path)
	if err != nil {
		writeReviewMetadataExportError(w, err)
		return
	}
	if len(result.Raw) == 0 {
		writeError(w, http.StatusBadGateway, "review metadata export response is invalid", "review-export")
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Disposition", `attachment; filename="`+reviewMetadataExportFilename(freshRepository.Name)+`.json"`)
	if result.Truncated {
		w.Header().Set("X-RoboRev-Export-Truncated", "true")
	} else {
		w.Header().Set("X-RoboRev-Export-Truncated", "false")
	}
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(result.Raw)
}

func writeReviewMetadataExportError(w http.ResponseWriter, err error) {
	status := http.StatusBadGateway
	message := "review metadata export is unavailable"
	switch {
	case errors.Is(err, context.DeadlineExceeded):
		status = http.StatusGatewayTimeout
		message = "review metadata export timed out"
	case errors.Is(err, roborev.ErrReviewMetadataExportConflict), errors.Is(err, roborev.ErrBindingChanged):
		status = http.StatusConflict
		message = "repository state changed; refresh and retry"
	case errors.Is(err, roborev.ErrUnsupportedReviewMetadataExport), errors.Is(err, roborev.ErrReviewMetadataExportOffline), errors.Is(err, roborev.ErrReviewMetadataExportUnhealthy):
		status = http.StatusServiceUnavailable
		message = "review metadata export is unavailable"
	case errors.Is(err, roborev.ErrMalformedReviewMetadataExport):
		status = http.StatusBadGateway
		message = "review metadata export response is invalid"
	}
	writeError(w, status, message, "review-export")
}

func reviewMetadataExportFilename(name string) string {
	var builder strings.Builder
	for _, char := range name {
		if (char >= 'a' && char <= 'z') || (char >= 'A' && char <= 'Z') || (char >= '0' && char <= '9') || char == '-' || char == '_' || char == '.' {
			builder.WriteRune(char)
		} else {
			builder.WriteByte('-')
		}
		if builder.Len() >= reviewMetadataExportFilenameMax {
			break
		}
	}
	safe := strings.Trim(builder.String(), ".-_ ")
	if safe == "" {
		safe = "repository"
	}
	if len(safe) > reviewMetadataExportFilenameMax {
		safe = safe[:reviewMetadataExportFilenameMax]
	}
	return "roborev-reviews-" + safe
}
