package app

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/froze/roborev-dashboard/internal/roborev"
	"github.com/froze/roborev-dashboard/internal/webui"
)

type Server struct {
	RoboRev     roborev.Service
	Registry    *Registry
	AllowedHost string
	csrfToken   string
	mux         *http.ServeMux
}

func NewServer(service roborev.Service, registry *Registry, allowedHost string) (*Server, error) {
	if service == nil || registry == nil {
		return nil, errors.New("service and repository registry are required")
	}
	tokenBytes := make([]byte, 32)
	if _, err := rand.Read(tokenBytes); err != nil {
		return nil, err
	}
	s := &Server{RoboRev: service, Registry: registry, AllowedHost: allowedHost, csrfToken: base64.RawURLEncoding.EncodeToString(tokenBytes), mux: http.NewServeMux()}
	s.routes()
	return s, nil
}

func (s *Server) Handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'; img-src 'self' data:; connect-src 'self'")
		if strings.HasPrefix(r.URL.Path, "/api/") && s.AllowedHost != "" && !strings.EqualFold(r.Host, s.AllowedHost) {
			writeError(w, http.StatusForbidden, "invalid host", "host")
			return
		}
		s.mux.ServeHTTP(w, r)
	})
}

func (s *Server) routes() {
	s.mux.HandleFunc("GET /api/bootstrap", s.bootstrap)
	s.mux.HandleFunc("GET /api/reviews", s.listReviews)
	s.mux.HandleFunc("GET /api/reviews/{id}", s.showReview)
	s.mux.HandleFunc("GET /api/reviews/{id}/prompt", s.showPrompt)
	s.mux.HandleFunc("GET /api/reviews/{id}/log", s.showLog)
	s.mux.HandleFunc("POST /api/reviews", s.mutation(s.createReview))
	s.mux.HandleFunc("POST /api/reviews/{id}/comment", s.mutation(s.comment))
	s.mux.HandleFunc("POST /api/reviews/{id}/comment-close", s.mutation(s.commentClose))
	s.mux.HandleFunc("POST /api/reviews/{id}/close", s.mutation(s.closeReview))
	s.mux.HandleFunc("POST /api/reviews/{id}/reopen", s.mutation(s.reopenReview))
	s.mux.HandleFunc("POST /api/reviews/{id}/cancel", s.mutation(s.cancelReview))
	s.mux.HandleFunc("POST /api/reviews/{id}/rerun", s.mutation(s.rerunReview))
	s.mux.HandleFunc("POST /api/repos", s.mutation(s.addRepository))
	s.mux.HandleFunc("POST /api/repos/select", s.mutation(s.selectRepository))
	s.mux.HandleFunc("POST /api/daemon/start", s.mutation(s.startDaemon))
	s.mux.Handle("/", spaHandler())
}

func (s *Server) mutation(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-CSRF-Token") != s.csrfToken {
			writeError(w, http.StatusForbidden, "invalid CSRF token", "csrf")
			return
		}
		if origin := r.Header.Get("Origin"); origin != "" {
			parsed, err := url.Parse(origin)
			if err != nil || !strings.EqualFold(parsed.Host, r.Host) {
				writeError(w, http.StatusForbidden, "cross-origin mutation rejected", "origin")
				return
			}
		}
		next(w, r)
	}
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func writeRawJSON(w http.ResponseWriter, status int, raw json.RawMessage) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_, _ = w.Write(raw)
}

func writeError(w http.ResponseWriter, status int, message, phase string) {
	writeJSON(w, status, map[string]any{"error": message, "phase": phase})
}

func decodeBody(w http.ResponseWriter, r *http.Request, target any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request: "+err.Error(), "request")
		return false
	}
	return true
}

func parseID(w http.ResponseWriter, r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		writeError(w, http.StatusBadRequest, "invalid job ID", "request")
		return 0, false
	}
	return id, true
}

func (s *Server) repoForRequest(w http.ResponseWriter, path string) (string, bool) {
	repo, err := s.Registry.Resolve(path)
	if err != nil {
		writeError(w, http.StatusForbidden, err.Error(), "repository")
		return "", false
	}
	return repo, true
}

func (s *Server) bootstrap(w http.ResponseWriter, r *http.Request) {
	repos := s.Registry.InspectAll(r.Context())
	installation := s.RoboRev.Installation(r.Context())
	var preview *roborev.CommandPreview
	if selected, err := s.Registry.Resolve(""); err == nil {
		for _, repo := range repos {
			if strings.EqualFold(repo.Path, selected) {
				p, e := s.RoboRev.ReviewCommand(roborev.ReviewRequest{RepoPath: repo.Path, GitRef: repo.Head, Branch: repo.Branch, Panel: "dashboard_cross"})
				if e == nil {
					preview = &p
				}
				break
			}
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"csrfToken": s.csrfToken, "repositories": repos, "selectedRepository": s.Registry.Selected(), "roborev": installation, "reviewHeadCommand": preview})
}

func (s *Server) listReviews(w http.ResponseWriter, r *http.Request) {
	repo, ok := s.repoForRequest(w, r.URL.Query().Get("repo"))
	if !ok {
		return
	}
	raw, err := s.RoboRev.List(r.Context(), repo, 100)
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error(), "list")
		return
	}
	writeRawJSON(w, http.StatusOK, raw)
}

func (s *Server) showReview(w http.ResponseWriter, r *http.Request) {
	id, ok := parseID(w, r)
	if !ok {
		return
	}
	raw, err := s.RoboRev.Show(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error(), "detail")
		return
	}
	writeRawJSON(w, http.StatusOK, raw)
}
func (s *Server) showPrompt(w http.ResponseWriter, r *http.Request) {
	id, ok := parseID(w, r)
	if !ok {
		return
	}
	value, err := s.RoboRev.Prompt(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error(), "prompt")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"text": value})
}
func (s *Server) showLog(w http.ResponseWriter, r *http.Request) {
	id, ok := parseID(w, r)
	if !ok {
		return
	}
	value, err := s.RoboRev.Log(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error(), "log")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"text": value})
}

type repoBody struct {
	RepoPath string `json:"repoPath"`
}
type reviewBody struct {
	RepoPath string `json:"repoPath"`
	Panel    string `json:"panel"`
}
type commentBody struct {
	Comment string `json:"comment"`
}

func (s *Server) createReview(w http.ResponseWriter, r *http.Request) {
	var body reviewBody
	if !decodeBody(w, r, &body) {
		return
	}
	path, ok := s.repoForRequest(w, body.RepoPath)
	if !ok {
		return
	}
	var repoInfo any
	for _, repo := range s.Registry.InspectAll(r.Context()) {
		if strings.EqualFold(repo.Path, path) {
			repoInfo = repo
			request := roborev.ReviewRequest{RepoPath: repo.Path, GitRef: repo.Head, Branch: repo.Branch, Panel: body.Panel}
			if request.Panel == "" {
				request.Panel = "dashboard_cross"
			}
			raw, err := s.RoboRev.Review(r.Context(), request)
			if err != nil {
				writeError(w, http.StatusBadGateway, err.Error(), "enqueue")
				return
			}
			writeRawJSON(w, http.StatusCreated, raw)
			return
		}
	}
	_ = repoInfo
	writeError(w, http.StatusConflict, "repository state is unavailable", "repository")
}

func (s *Server) comment(w http.ResponseWriter, r *http.Request) {
	id, ok := parseID(w, r)
	if !ok {
		return
	}
	var body commentBody
	if !decodeBody(w, r, &body) {
		return
	}
	if err := s.RoboRev.Comment(r.Context(), id, body.Comment); err != nil {
		writeError(w, http.StatusBadGateway, err.Error(), "comment")
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"success": true})
}

func (s *Server) commentClose(w http.ResponseWriter, r *http.Request) {
	id, ok := parseID(w, r)
	if !ok {
		return
	}
	var body commentBody
	if !decodeBody(w, r, &body) {
		return
	}
	if err := s.RoboRev.Comment(r.Context(), id, body.Comment); err != nil {
		writeError(w, http.StatusBadGateway, err.Error(), "comment")
		return
	}
	if err := s.RoboRev.Close(r.Context(), id, false); err != nil {
		writeError(w, http.StatusBadGateway, "comment succeeded but close failed: "+err.Error(), "close")
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"success": true})
}

func (s *Server) closeReview(w http.ResponseWriter, r *http.Request)  { s.closeOrReopen(w, r, false) }
func (s *Server) reopenReview(w http.ResponseWriter, r *http.Request) { s.closeOrReopen(w, r, true) }
func (s *Server) closeOrReopen(w http.ResponseWriter, r *http.Request, reopen bool) {
	id, ok := parseID(w, r)
	if !ok {
		return
	}
	if err := s.RoboRev.Close(r.Context(), id, reopen); err != nil {
		writeError(w, http.StatusBadGateway, err.Error(), "close")
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"success": true})
}
func (s *Server) cancelReview(w http.ResponseWriter, r *http.Request) {
	id, ok := parseID(w, r)
	if !ok {
		return
	}
	if err := s.RoboRev.Cancel(r.Context(), id); err != nil {
		writeError(w, http.StatusBadGateway, err.Error(), "cancel")
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"success": true})
}
func (s *Server) rerunReview(w http.ResponseWriter, r *http.Request) {
	id, ok := parseID(w, r)
	if !ok {
		return
	}
	if err := s.RoboRev.Rerun(r.Context(), id); err != nil {
		writeError(w, http.StatusBadGateway, err.Error(), "rerun")
		return
	}
	writeJSON(w, http.StatusCreated, map[string]bool{"success": true})
}

func (s *Server) addRepository(w http.ResponseWriter, r *http.Request) {
	var body repoBody
	if !decodeBody(w, r, &body) {
		return
	}
	repo, err := s.Registry.Add(r.Context(), body.RepoPath)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error(), "repository")
		return
	}
	writeJSON(w, http.StatusCreated, repo)
}
func (s *Server) selectRepository(w http.ResponseWriter, r *http.Request) {
	var body repoBody
	if !decodeBody(w, r, &body) {
		return
	}
	if err := s.Registry.Select(body.RepoPath); err != nil {
		writeError(w, http.StatusForbidden, err.Error(), "repository")
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"success": true})
}
func (s *Server) startDaemon(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	if err := s.RoboRev.StartDaemon(ctx); err != nil {
		writeError(w, http.StatusBadGateway, err.Error(), "daemon")
		return
	}
	time.Sleep(200 * time.Millisecond)
	writeJSON(w, http.StatusOK, map[string]bool{"success": true})
}

func spaHandler() http.Handler {
	assets, err := fs.Sub(webui.Files, "dist")
	if err != nil {
		return http.NotFoundHandler()
	}
	files := http.FileServer(http.FS(assets))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := strings.TrimPrefix(r.URL.Path, "/")
		if path == "" {
			path = "index.html"
		}
		if _, err := fs.Stat(assets, path); err == nil {
			clone := r.Clone(r.Context())
			clone.URL.Path = "/" + path
			files.ServeHTTP(w, clone)
			return
		}
		if _, err := fs.Stat(assets, "index.html"); err != nil {
			http.Error(w, "frontend assets are not built; run scripts/build.ps1", http.StatusServiceUnavailable)
			return
		}
		clone := r.Clone(r.Context())
		clone.URL.Path = "/index.html"
		files.ServeHTTP(w, clone)
	})
}

var _ = fmt.Sprintf
