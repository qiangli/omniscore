// Package server wires the OmniScore HTTP API together. All persistence
// goes through internal/store; all timing through internal/session.
package server

import (
	"encoding/json"
	"errors"
	"io/fs"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"

	"github.com/qiangli/omniscore/internal/content"
	"github.com/qiangli/omniscore/internal/session"
	"github.com/qiangli/omniscore/internal/store"
)

// Server holds wiring for the HTTP handlers.
type Server struct {
	Store       *store.Store
	Cookies     *cookieSigner
	Static      fs.FS // embedded frontend; may be nil during early bootstrap
	FiguresRoot string // contentRoot — base for per-exam <exam>/figures/ trees
	Logger      *slog.Logger
}

// New builds a Server with a HMAC cookie key persisted at keyPath. figuresRoot
// is the same path passed as -content; it lets the static handler at
// /api/figures/<exam>/<slug>/<file> resolve images on disk.
func New(s *store.Store, keyPath string, static fs.FS, figuresRoot string, logger *slog.Logger) (*Server, error) {
	key, err := loadOrCreateKey(keyPath)
	if err != nil {
		return nil, err
	}
	return &Server{Store: s, Cookies: &cookieSigner{key: key}, Static: static, FiguresRoot: figuresRoot, Logger: logger}, nil
}

// Router returns the configured chi.Mux. Mount under "/" of an http.Server.
func (s *Server) Router() http.Handler {
	r := chi.NewRouter()
	r.Use(middleware.Recoverer)
	r.Use(middleware.RequestID)
	r.Use(s.requestLogger)

	r.Get("/healthz", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})

	r.Route("/api", func(r chi.Router) {
		r.Post("/students", s.postStudent)
		r.Get("/tests", s.getTests)
		r.Get("/tests/{slug}", s.getTest)
		if s.FiguresRoot != "" {
			r.Get("/figures/{exam}/{slug}/*", s.serveFigure)
		}

		r.Group(func(r chi.Router) {
			r.Use(s.requireStudent)
			r.Post("/sessions", s.postSession)
			r.Get("/sessions/{id}", s.getSession)
			r.Patch("/sessions/{id}/answer", s.patchAnswer)
			r.Post("/sessions/{id}/advance", s.postAdvance)
			r.Post("/sessions/{id}/submit", s.postSubmit)
			r.Get("/sessions/{id}/results", s.getResults)
			r.Post("/sessions/{id}/highlights", s.postHighlight)
			r.Delete("/sessions/{id}/highlights/{hid}", s.deleteHighlight)
		})
	})

	// Serve embedded frontend (or 404 if not present yet).
	if s.Static != nil {
		r.Handle("/*", spaHandler(s.Static))
	}
	return r
}

func (s *Server) requestLogger(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		ww := middleware.NewWrapResponseWriter(w, r.ProtoMajor)
		next.ServeHTTP(ww, r)
		s.Logger.Info("http",
			"method", r.Method,
			"path", r.URL.Path,
			"status", ww.Status(),
			"bytes", ww.BytesWritten(),
			"dur_ms", time.Since(start).Milliseconds(),
		)
	})
}

type ctxKey string

const ctxStudentID ctxKey = "student_id"

func (s *Server) requireStudent(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id, err := studentFromRequest(r, s.Cookies)
		if err != nil {
			writeError(w, http.StatusUnauthorized, "not signed in")
			return
		}
		ctx := r.Context()
		ctx = withStudent(ctx, id)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// ----- Handlers -----

type studentCreateReq struct {
	DisplayName string `json:"display_name"`
}
type studentResp struct {
	ID          int64  `json:"id"`
	DisplayName string `json:"display_name"`
}

func (s *Server) postStudent(w http.ResponseWriter, r *http.Request) {
	var req studentCreateReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "bad json")
		return
	}
	name := strings.TrimSpace(req.DisplayName)
	if name == "" {
		writeError(w, http.StatusBadRequest, "display_name required")
		return
	}
	if len(name) > 80 {
		name = name[:80]
	}
	now := time.Now().UnixMilli()
	res, err := s.Store.DB.ExecContext(r.Context(),
		`INSERT INTO students(display_name, joined_at) VALUES (?, ?)`, name, now)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	id, _ := res.LastInsertId()
	setStudentCookie(w, s.Cookies, id)
	writeJSON(w, http.StatusCreated, studentResp{ID: id, DisplayName: name})
}

func (s *Server) getTests(w http.ResponseWriter, r *http.Request) {
	list, err := content.List(r.Context(), s.Store)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"tests": list})
}

func (s *Server) getTest(w http.ResponseWriter, r *http.Request) {
	slug := chi.URLParam(r, "slug")
	t, err := content.Get(r.Context(), s.Store, slug)
	if err != nil {
		writeError(w, http.StatusNotFound, "no such test")
		return
	}
	writeJSON(w, http.StatusOK, content.StripAnswers(t))
}

type sessionCreateReq struct {
	TestSlug string `json:"test_slug"`
}

type sessionEnvelope struct {
	Session     session.Session     `json:"session"`
	Module      content.Module      `json:"module"`
	Test        content.Listing     `json:"test"`
	Responses   []session.Response  `json:"responses"`
	Highlights  []session.Highlight `json:"highlights"`
	ServerNowMS int64               `json:"server_now_ms"`
}

func (s *Server) postSession(w http.ResponseWriter, r *http.Request) {
	studentID, _ := studentID(r.Context())
	var req sessionCreateReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "bad json")
		return
	}
	if req.TestSlug == "" {
		writeError(w, http.StatusBadRequest, "test_slug required")
		return
	}
	sess, t, err := session.Create(r.Context(), s.Store, studentID, req.TestSlug)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	stripped := content.StripAnswers(t)
	mod := stripped.Modules[sess.CurrentModule]
	writeJSON(w, http.StatusCreated, sessionEnvelope{
		Session:     sess,
		Module:      mod,
		Test:        content.Listing{Slug: t.Slug, Title: t.Title, ExamType: t.ExamType, Subject: t.Subject, Modules: len(t.Modules)},
		Responses:   nil,
		Highlights:  nil,
		ServerNowMS: time.Now().UnixMilli(),
	})
}

func (s *Server) getSession(w http.ResponseWriter, r *http.Request) {
	sess, t, err := s.loadOwnedSession(r)
	if err != nil {
		writeError(w, statusFor(err), err.Error())
		return
	}
	stripped := content.StripAnswers(t)
	resps, err := session.Responses(r.Context(), s.Store, sess.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	hs, err := session.Highlights(r.Context(), s.Store, sess.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	mod := content.Module{}
	if sess.CurrentModule < len(stripped.Modules) {
		mod = stripped.Modules[sess.CurrentModule]
	}
	writeJSON(w, http.StatusOK, sessionEnvelope{
		Session:     sess,
		Module:      mod,
		Test:        content.Listing{Slug: t.Slug, Title: t.Title, ExamType: t.ExamType, Subject: t.Subject, Modules: len(t.Modules)},
		Responses:   resps,
		Highlights:  hs,
		ServerNowMS: time.Now().UnixMilli(),
	})
}

type answerReq struct {
	QuestionID       string `json:"question_id"`
	Choice           string `json:"choice"`
	TimeOnQuestionMS int64  `json:"time_on_question_ms"`
}
type answerResp struct {
	ServerNowMS      int64 `json:"server_now_ms"`
	ModuleDeadlineAt int64 `json:"module_deadline_at"`
}

func (s *Server) patchAnswer(w http.ResponseWriter, r *http.Request) {
	sess, _, err := s.loadOwnedSession(r)
	if err != nil {
		writeError(w, statusFor(err), err.Error())
		return
	}
	if sess.State != session.StateInProgress {
		writeError(w, http.StatusConflict, "session not in progress")
		return
	}
	var req answerReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "bad json")
		return
	}
	if err := session.UpsertAnswer(r.Context(), s.Store, sess.ID, req.QuestionID, req.Choice, req.TimeOnQuestionMS); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, answerResp{
		ServerNowMS:      time.Now().UnixMilli(),
		ModuleDeadlineAt: sess.ModuleDeadlineAt,
	})
}

func (s *Server) postAdvance(w http.ResponseWriter, r *http.Request) {
	sess, t, err := s.loadOwnedSession(r)
	if err != nil {
		writeError(w, statusFor(err), err.Error())
		return
	}
	_ = t
	next, err := session.Advance(r.Context(), s.Store, sess.ID)
	if errors.Is(err, session.ErrNoMoreModules) {
		writeJSON(w, http.StatusOK, map[string]any{"done": true})
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	stripped := content.StripAnswers(t)
	mod := stripped.Modules[next.CurrentModule]
	writeJSON(w, http.StatusOK, sessionEnvelope{
		Session:     next,
		Module:      mod,
		Test:        content.Listing{Slug: t.Slug, Title: t.Title, ExamType: t.ExamType, Subject: t.Subject, Modules: len(t.Modules)},
		ServerNowMS: time.Now().UnixMilli(),
	})
}

func (s *Server) postSubmit(w http.ResponseWriter, r *http.Request) {
	sess, _, err := s.loadOwnedSession(r)
	if err != nil {
		writeError(w, statusFor(err), err.Error())
		return
	}
	sum, err := session.Submit(r.Context(), s.Store, sess.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, sum)
}

func (s *Server) getResults(w http.ResponseWriter, r *http.Request) {
	sess, _, err := s.loadOwnedSession(r)
	if err != nil {
		writeError(w, statusFor(err), err.Error())
		return
	}
	if sess.State != session.StateSubmitted {
		writeError(w, http.StatusConflict, "session not submitted")
		return
	}
	sum, err := session.Submit(r.Context(), s.Store, sess.ID) // idempotent re-tally
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, sum)
}

type highlightReq struct {
	QuestionID string `json:"question_id"`
	AnchorJSON string `json:"anchor_json"`
	Color      string `json:"color"`
	Note       string `json:"note"`
}

func (s *Server) postHighlight(w http.ResponseWriter, r *http.Request) {
	sess, _, err := s.loadOwnedSession(r)
	if err != nil {
		writeError(w, statusFor(err), err.Error())
		return
	}
	var req highlightReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "bad json")
		return
	}
	if req.Color == "" {
		req.Color = "yellow"
	}
	id, err := session.AddHighlight(r.Context(), s.Store, sess.ID, req.QuestionID, req.AnchorJSON, req.Color, req.Note)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"id": id})
}

func (s *Server) deleteHighlight(w http.ResponseWriter, r *http.Request) {
	sess, _, err := s.loadOwnedSession(r)
	if err != nil {
		writeError(w, statusFor(err), err.Error())
		return
	}
	hidStr := chi.URLParam(r, "hid")
	hid, err := strconv.ParseInt(hidStr, 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "bad id")
		return
	}
	if err := session.DeleteHighlight(r.Context(), s.Store, sess.ID, hid); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ----- helpers -----

func (s *Server) loadOwnedSession(r *http.Request) (session.Session, content.Test, error) {
	studentID, _ := studentID(r.Context())
	id := chi.URLParam(r, "id")
	sess, err := session.Load(r.Context(), s.Store, id)
	if err != nil {
		return sess, content.Test{}, err
	}
	if sess.StudentID != studentID {
		return sess, content.Test{}, errForbidden
	}
	t, err := content.Get(r.Context(), s.Store, sess.TestSlug)
	if err != nil {
		return sess, content.Test{}, err
	}
	return sess, t, nil
}

var errForbidden = errors.New("forbidden")

func statusFor(err error) int {
	if errors.Is(err, errForbidden) {
		return http.StatusForbidden
	}
	return http.StatusNotFound
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, code int, msg string) {
	writeJSON(w, code, map[string]string{"error": msg})
}
