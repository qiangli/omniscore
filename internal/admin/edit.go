package admin

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/qiangli/omniscore/internal/content"
	"github.com/qiangli/omniscore/internal/store"
)

// EditQuestionRequest carries any subset of the fields an admin can change
// on a single question. Pointer-valued so nil means "leave alone" and
// empty-string is a real value (used to clear a caption etc.).
//
// question_id itself is never editable — that would orphan responses /
// question_review rows. Same for choice labels: only TextMD and the
// per-figure caption (Alt) are editable on choices.
type EditQuestionRequest struct {
	PassageMD            *string             `json:"passage_md,omitempty"`
	StemMD               *string             `json:"stem_md,omitempty"`
	AnswerLabel          *string             `json:"answer_label,omitempty"`
	AnswerValues         *[]string           `json:"answer_values,omitempty"`
	RationaleMD          *string             `json:"rationale_md,omitempty"`
	PassageFigureCaption *string             `json:"passage_figure_caption,omitempty"`
	StemFigureCaption    *string             `json:"stem_figure_caption,omitempty"`
	ChoiceEdits          map[string]ChoiceEdit `json:"choice_edits,omitempty"` // keyed by choice label
}

// ChoiceEdit is the per-choice patch payload. Both fields are optional;
// label is the path parameter, not an editable field.
type ChoiceEdit struct {
	TextMD        *string `json:"text_md,omitempty"`
	FigureCaption *string `json:"figure_caption,omitempty"`
}

// Editor holds the dependencies needed to PATCH a question on disk.
type Editor struct {
	IO    *JSONIO
	Store *store.Store
}

// PatchQuestion handles PATCH /api/admin/tests/{slug}/questions/{qid}.
// Mutates test.json on disk, then hot-reloads via content.LoadFromDisk.
func (e *Editor) PatchQuestion(w http.ResponseWriter, r *http.Request) {
	slug := chi.URLParam(r, "slug")
	qid := chi.URLParam(r, "qid")
	var req EditQuestionRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "bad json")
		return
	}
	found := false
	_, err := e.IO.MutateAndSave(r.Context(), slug, func(t *content.Test) error {
		for i := range t.Modules {
			for j := range t.Modules[i].Questions {
				q := &t.Modules[i].Questions[j]
				if q.ID != qid {
					continue
				}
				found = true
				applyEdit(q, req)
				return nil
			}
		}
		return errors.New("admin: question_id not found in test")
	})
	if errors.Is(err, ErrFlatLayout) {
		writeError(w, http.StatusConflict, err.Error())
		return
	}
	if errors.Is(err, ErrTestNotOnDisk) {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if !found {
		writeError(w, http.StatusNotFound, "question_id not found")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"slug": slug, "question_id": qid})
}

func applyEdit(q *content.Question, req EditQuestionRequest) {
	if req.PassageMD != nil {
		q.PassageMD = *req.PassageMD
	}
	if req.StemMD != nil {
		q.StemMD = *req.StemMD
	}
	if req.AnswerLabel != nil {
		q.AnswerLabel = *req.AnswerLabel
	}
	if req.AnswerValues != nil {
		q.AnswerValues = *req.AnswerValues
	}
	if req.RationaleMD != nil {
		q.RationaleMD = *req.RationaleMD
	}
	if req.PassageFigureCaption != nil && q.PassageFigure != nil {
		q.PassageFigure.Alt = *req.PassageFigureCaption
	}
	if req.StemFigureCaption != nil && q.StemFigure != nil {
		q.StemFigure.Alt = *req.StemFigureCaption
	}
	for label, ce := range req.ChoiceEdits {
		for k := range q.Choices {
			if q.Choices[k].Label != label {
				continue
			}
			if ce.TextMD != nil {
				q.Choices[k].TextMD = *ce.TextMD
			}
			if ce.FigureCaption != nil && q.Choices[k].Figure != nil {
				q.Choices[k].Figure.Alt = *ce.FigureCaption
			}
		}
	}
}

// GetTest returns the full Test with answers + per-question review state
// joined in. Unlike the public /api/tests/{slug} endpoint, this does NOT
// strip answer keys — admins need to see them to confirm correctness.
func (e *Editor) GetTest(w http.ResponseWriter, r *http.Request) {
	slug := chi.URLParam(r, "slug")
	t, err := content.Get(r.Context(), e.Store, slug)
	if err != nil {
		writeError(w, http.StatusNotFound, "no such test")
		return
	}
	reviews, err := loadReviews(r.Context(), e.Store, slug)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"test":    t,
		"reviews": reviews,
	})
}

func loadReviews(ctx context.Context, s *store.Store, slug string) (map[string]ReviewRow, error) {
	rows, err := s.DB.QueryContext(ctx,
		`SELECT question_id, status, COALESCE(note,''), COALESCE(reviewed_by,''), COALESCE(reviewed_at,0)
		   FROM question_review WHERE test_slug = ?`, slug)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]ReviewRow{}
	for rows.Next() {
		var rr ReviewRow
		if err := rows.Scan(&rr.QuestionID, &rr.Status, &rr.Note, &rr.ReviewedBy, &rr.ReviewedAt); err != nil {
			return nil, err
		}
		out[rr.QuestionID] = rr
	}
	return out, rows.Err()
}
