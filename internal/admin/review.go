package admin

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/qiangli/omniscore/internal/content"
	"github.com/qiangli/omniscore/internal/store"
)

// Review state values. Mirror the CHECK constraint on question_review.status.
const (
	StatusPending  = "pending"
	StatusApproved = "approved"
	StatusFlagged  = "flagged"
)

// ReviewRow is the row shape returned to the admin UI.
type ReviewRow struct {
	QuestionID string `json:"question_id"`
	Status     string `json:"status"`
	Note       string `json:"note,omitempty"`
	ReviewedBy string `json:"reviewed_by,omitempty"`
	ReviewedAt int64  `json:"reviewed_at,omitempty"`
}

// SetReviewRequest is the body of PUT /api/admin/tests/{slug}/questions/{qid}/review.
type SetReviewRequest struct {
	Status string `json:"status"`
	Note   string `json:"note,omitempty"`
}

// BulkReviewRequest is the body of POST /api/admin/tests/{slug}/review/bulk.
// An empty QuestionIDs list means "every question in this test".
type BulkReviewRequest struct {
	Status      string   `json:"status"`
	QuestionIDs []string `json:"question_ids,omitempty"`
}

// Reviewer wires the review-state handlers against the store.
type Reviewer struct {
	Store *store.Store
}

// SetReview handles PUT /api/admin/tests/{slug}/questions/{qid}/review.
func (rv *Reviewer) SetReview(w http.ResponseWriter, r *http.Request) {
	slug := chi.URLParam(r, "slug")
	qid := chi.URLParam(r, "qid")
	var req SetReviewRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "bad json")
		return
	}
	if !validStatus(req.Status) {
		writeError(w, http.StatusBadRequest, "status must be one of pending|approved|flagged")
		return
	}
	if err := upsertReview(r.Context(), rv.Store, slug, qid, req.Status, req.Note); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"slug": slug, "question_id": qid, "status": req.Status})
}

// BulkSetReview handles POST /api/admin/tests/{slug}/review/bulk.
// Empty QuestionIDs => every question in the test.
func (rv *Reviewer) BulkSetReview(w http.ResponseWriter, r *http.Request) {
	slug := chi.URLParam(r, "slug")
	var req BulkReviewRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "bad json")
		return
	}
	if !validStatus(req.Status) {
		writeError(w, http.StatusBadRequest, "status must be one of pending|approved|flagged")
		return
	}
	ids := req.QuestionIDs
	if len(ids) == 0 {
		t, err := content.Get(r.Context(), rv.Store, slug)
		if err != nil {
			writeError(w, http.StatusNotFound, "no such test")
			return
		}
		for _, m := range t.Modules {
			for _, q := range m.Questions {
				ids = append(ids, q.ID)
			}
		}
	}
	for _, qid := range ids {
		if err := upsertReview(r.Context(), rv.Store, slug, qid, req.Status, ""); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"slug": slug, "updated": len(ids), "status": req.Status})
}

func validStatus(s string) bool {
	switch s {
	case StatusPending, StatusApproved, StatusFlagged:
		return true
	}
	return false
}

func upsertReview(ctx context.Context, s *store.Store, slug, qid, status, note string) error {
	now := time.Now().UnixMilli()
	_, err := s.DB.ExecContext(ctx,
		`INSERT INTO question_review(test_slug, question_id, status, note, reviewed_at) VALUES (?, ?, ?, ?, ?)
		 ON CONFLICT(test_slug, question_id) DO UPDATE SET status = excluded.status, note = excluded.note, reviewed_at = excluded.reviewed_at`,
		slug, qid, status, nullableString(note), now)
	return err
}

// nullableString converts "" to nil so the column ends up NULL rather than
// the empty string. Keeps SQL NULL semantics for "no note".
func nullableString(s string) any {
	if s == "" {
		return nil
	}
	return s
}

// ListTests returns one row per published test with approved/flagged/pending
// counts joined from question_review.
type TestListing struct {
	Slug     string `json:"slug"`
	Title    string `json:"title"`
	ExamType string `json:"exam_type"`
	Approved int    `json:"approved"`
	Flagged  int    `json:"flagged"`
	Pending  int    `json:"pending"`
	Total    int    `json:"total"`
}

// ListTests handles GET /api/admin/tests.
func (rv *Reviewer) ListTests(w http.ResponseWriter, r *http.Request) {
	tests, err := content.List(r.Context(), rv.Store)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	out := make([]TestListing, 0, len(tests))
	for _, t := range tests {
		full, err := content.Get(r.Context(), rv.Store, t.Slug)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		row := TestListing{Slug: t.Slug, Title: t.Title, ExamType: t.ExamType}
		for _, m := range full.Modules {
			row.Total += len(m.Questions)
		}
		statuses, err := loadReviewStatuses(r.Context(), rv.Store, t.Slug)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		for _, s := range statuses {
			switch s {
			case StatusApproved:
				row.Approved++
			case StatusFlagged:
				row.Flagged++
			}
		}
		row.Pending = row.Total - row.Approved - row.Flagged
		out = append(out, row)
	}
	writeJSON(w, http.StatusOK, map[string]any{"tests": out})
}

func loadReviewStatuses(ctx context.Context, s *store.Store, slug string) ([]string, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT status FROM question_review WHERE test_slug = ?`, slug)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var st string
		if err := rows.Scan(&st); err != nil {
			return nil, err
		}
		out = append(out, st)
	}
	return out, rows.Err()
}
