// Package session manages the lifecycle of a single student's test attempt:
// create, resume, autosave answers, advance modules, submit, and score.
//
// All timing is server-authoritative: the client renders a countdown from
// module_deadline_at returned by the server, never from its own clock.
package session

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"github.com/qiangli/omniscore/internal/content"
	"github.com/qiangli/omniscore/internal/scoring"
	"github.com/qiangli/omniscore/internal/store"
)

// State is the lifecycle stage of a session row.
type State string

const (
	StateInProgress State = "in_progress"
	StateSubmitted  State = "submitted"
	StateExpired    State = "expired"
)

// Session is the runtime view of a row in student_sessions.
type Session struct {
	ID               string `json:"id"`
	StudentID        int64  `json:"student_id"`
	TestSlug         string `json:"test_slug"`
	CurrentModule    int    `json:"current_module"`
	ModuleDeadlineAt int64  `json:"module_deadline_at"` // unix ms
	State            State  `json:"state"`
	RawScore         *int   `json:"raw_score,omitempty"`
	ScaledScore      *int   `json:"scaled_score,omitempty"`
	CreatedAt        int64  `json:"created_at"`
	UpdatedAt        int64  `json:"updated_at"`
}

// Response captures a student's answer to one question, plus accumulated time on it.
type Response struct {
	QuestionID       string `json:"question_id"`
	Choice           string `json:"choice"`
	TimeOnQuestionMS int64  `json:"time_on_question_ms"`
	LastAnsweredAt   int64  `json:"last_answered_at"`
}

// Highlight is one persisted text annotation against a question's passage.
type Highlight struct {
	ID         int64  `json:"id"`
	QuestionID string `json:"question_id"`
	AnchorJSON string `json:"anchor_json"`
	Color      string `json:"color"`
	Note       string `json:"note,omitempty"`
	CreatedAt  int64  `json:"created_at"`
}

// Create starts a new session for a student against a published test.
// It loads the test, sets current_module = 0, and computes the first deadline
// based on the module's time_limit_s.
func Create(ctx context.Context, s *store.Store, studentID int64, testSlug string) (Session, content.Test, error) {
	t, err := content.Get(ctx, s, testSlug)
	if err != nil {
		return Session{}, content.Test{}, fmt.Errorf("load test %q: %w", testSlug, err)
	}
	if len(t.Modules) == 0 {
		return Session{}, content.Test{}, errors.New("test has no modules")
	}
	id, err := newID()
	if err != nil {
		return Session{}, content.Test{}, err
	}
	now := time.Now().UnixMilli()
	deadline := now + int64(t.Modules[0].TimeLimitS)*1000
	sess := Session{
		ID:               id,
		StudentID:        studentID,
		TestSlug:         testSlug,
		CurrentModule:    0,
		ModuleDeadlineAt: deadline,
		State:            StateInProgress,
		CreatedAt:        now,
		UpdatedAt:        now,
	}
	if _, err := s.DB.ExecContext(ctx, `
		INSERT INTO student_sessions
		  (id, student_id, test_slug, current_module, module_started_at, module_deadline_at, state, created_at, updated_at)
		VALUES (?, ?, ?, 0, ?, ?, ?, ?, ?)`,
		sess.ID, studentID, testSlug, now, deadline, sess.State, now, now,
	); err != nil {
		return Session{}, content.Test{}, err
	}
	return sess, t, nil
}

// Load fetches a Session by id (no auth check — caller enforces).
func Load(ctx context.Context, s *store.Store, id string) (Session, error) {
	row := s.DB.QueryRowContext(ctx, `
		SELECT id, student_id, test_slug, current_module, module_deadline_at, state, raw_score, scaled_score, created_at, updated_at
		FROM student_sessions WHERE id = ?`, id)
	var sess Session
	var rawScore, scaledScore sql.NullInt64
	if err := row.Scan(&sess.ID, &sess.StudentID, &sess.TestSlug, &sess.CurrentModule, &sess.ModuleDeadlineAt, &sess.State, &rawScore, &scaledScore, &sess.CreatedAt, &sess.UpdatedAt); err != nil {
		return Session{}, err
	}
	if rawScore.Valid {
		v := int(rawScore.Int64)
		sess.RawScore = &v
	}
	if scaledScore.Valid {
		v := int(scaledScore.Int64)
		sess.ScaledScore = &v
	}
	return sess, nil
}

// UpsertAnswer records or updates a student's choice for one question.
func UpsertAnswer(ctx context.Context, s *store.Store, sessionID, questionID, choice string, timeOnMS int64) error {
	now := time.Now().UnixMilli()
	_, err := s.DB.ExecContext(ctx, `
		INSERT INTO responses(session_id, question_id, choice, first_answered_at, last_answered_at, time_on_question_ms)
		VALUES (?, ?, ?, ?, ?, ?)
		ON CONFLICT(session_id, question_id) DO UPDATE SET
		  choice = excluded.choice,
		  last_answered_at = excluded.last_answered_at,
		  time_on_question_ms = MAX(responses.time_on_question_ms, excluded.time_on_question_ms)`,
		sessionID, questionID, choice, now, now, timeOnMS,
	)
	if err != nil {
		return err
	}
	_, err = s.DB.ExecContext(ctx, `UPDATE student_sessions SET updated_at = ? WHERE id = ?`, now, sessionID)
	return err
}

// Responses returns every recorded answer for a session.
func Responses(ctx context.Context, s *store.Store, sessionID string) ([]Response, error) {
	rows, err := s.DB.QueryContext(ctx, `
		SELECT question_id, COALESCE(choice, ''), time_on_question_ms, last_answered_at
		FROM responses WHERE session_id = ?`, sessionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Response
	for rows.Next() {
		var r Response
		if err := rows.Scan(&r.QuestionID, &r.Choice, &r.TimeOnQuestionMS, &r.LastAnsweredAt); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// Advance moves the session to the next module (resetting the deadline) if one
// exists. If no further module remains it returns ErrNoMoreModules and the
// caller should call Submit.
var ErrNoMoreModules = errors.New("no more modules")

func Advance(ctx context.Context, s *store.Store, sessionID string) (Session, error) {
	sess, err := Load(ctx, s, sessionID)
	if err != nil {
		return Session{}, err
	}
	if sess.State != StateInProgress {
		return sess, fmt.Errorf("session not in progress (state=%s)", sess.State)
	}
	t, err := content.Get(ctx, s, sess.TestSlug)
	if err != nil {
		return Session{}, err
	}
	next := sess.CurrentModule + 1
	if next >= len(t.Modules) {
		return sess, ErrNoMoreModules
	}
	now := time.Now().UnixMilli()
	deadline := now + int64(t.Modules[next].TimeLimitS)*1000
	_, err = s.DB.ExecContext(ctx, `
		UPDATE student_sessions
		SET current_module = ?, module_started_at = ?, module_deadline_at = ?, updated_at = ?
		WHERE id = ?`,
		next, now, deadline, now, sessionID,
	)
	if err != nil {
		return Session{}, err
	}
	sess.CurrentModule = next
	sess.ModuleDeadlineAt = deadline
	sess.UpdatedAt = now
	return sess, nil
}

// Result is the public per-question outcome returned by /results.
type Result struct {
	QuestionID       string `json:"question_id"`
	Section          string `json:"section"`
	ModuleID         string `json:"module_id"`
	Chosen           string `json:"chosen"`
	Correct          string `json:"correct"`
	IsCorrect        bool   `json:"is_correct"`
	TimeOnQuestionMS int64  `json:"time_on_question_ms"`
	RationaleMD      string `json:"rationale_md,omitempty"`
}

// Summary is the aggregate result of a completed session.
type Summary struct {
	SessionID       string         `json:"session_id"`
	TestSlug        string         `json:"test_slug"`
	ExamType        string         `json:"exam_type,omitempty"`
	Subject         string         `json:"subject,omitempty"`
	State           State          `json:"state"`
	RawTotal        int            `json:"raw_total"`
	ScaledTotal     int            `json:"scaled_total"`
	BySection       map[string]int `json:"by_section_raw"`
	BySectionScaled map[string]int `json:"by_section_scaled"`
	Questions       []Result       `json:"questions"`
}

// Submit tallies the session and stores raw + scaled totals.
func Submit(ctx context.Context, s *store.Store, sessionID string) (Summary, error) {
	sess, err := Load(ctx, s, sessionID)
	if err != nil {
		return Summary{}, err
	}
	t, err := content.Get(ctx, s, sess.TestSlug)
	if err != nil {
		return Summary{}, err
	}
	answers, err := Responses(ctx, s, sessionID)
	if err != nil {
		return Summary{}, err
	}
	answered := make(map[string]Response, len(answers))
	for _, a := range answers {
		answered[a.QuestionID] = a
	}

	sum := Summary{
		SessionID:       sessionID,
		TestSlug:        sess.TestSlug,
		ExamType:        t.ExamType,
		Subject:         t.Subject,
		BySection:       map[string]int{},
		BySectionScaled: map[string]int{},
	}
	for _, m := range t.Modules {
		for _, q := range m.Questions {
			r := Result{
				QuestionID:  q.ID,
				Section:     m.Section,
				ModuleID:    m.ID,
				Correct:     q.AnswerLabel,
				RationaleMD: q.RationaleMD,
			}
			if ans, ok := answered[q.ID]; ok {
				r.Chosen = ans.Choice
				r.TimeOnQuestionMS = ans.TimeOnQuestionMS
				if ans.Choice == q.AnswerLabel {
					r.IsCorrect = true
					sum.RawTotal++
					sum.BySection[m.Section]++
				}
			}
			sum.Questions = append(sum.Questions, r)
		}
	}
	// Scale per section, then sum.
	for sec, raw := range sum.BySection {
		scaled, ok, err := scoring.Scale(ctx, s, sess.TestSlug, scoring.Section(sec), raw)
		if err != nil {
			return Summary{}, err
		}
		if !ok {
			continue
		}
		sum.BySectionScaled[sec] = scaled
		sum.ScaledTotal += scaled
	}
	// Sections with raw=0 may not be reflected in BySection map; include them too.
	for _, m := range t.Modules {
		if _, seen := sum.BySection[m.Section]; !seen {
			sum.BySection[m.Section] = 0
			scaled, ok, err := scoring.Scale(ctx, s, sess.TestSlug, scoring.Section(m.Section), 0)
			if err == nil && ok {
				sum.BySectionScaled[m.Section] = scaled
				sum.ScaledTotal += scaled
			}
		}
	}

	now := time.Now().UnixMilli()
	if _, err := s.DB.ExecContext(ctx, `
		UPDATE student_sessions SET state=?, raw_score=?, scaled_score=?, updated_at=? WHERE id=?`,
		StateSubmitted, sum.RawTotal, sum.ScaledTotal, now, sessionID,
	); err != nil {
		return Summary{}, err
	}
	sum.State = StateSubmitted
	return sum, nil
}

// Highlights returns every persisted highlight for a session.
func Highlights(ctx context.Context, s *store.Store, sessionID string) ([]Highlight, error) {
	rows, err := s.DB.QueryContext(ctx, `
		SELECT id, question_id, anchor_json, color, COALESCE(note, ''), created_at
		FROM highlights WHERE session_id = ? ORDER BY id`, sessionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Highlight
	for rows.Next() {
		var h Highlight
		if err := rows.Scan(&h.ID, &h.QuestionID, &h.AnchorJSON, &h.Color, &h.Note, &h.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, h)
	}
	return out, rows.Err()
}

// AddHighlight inserts a new highlight and returns its assigned ID.
func AddHighlight(ctx context.Context, s *store.Store, sessionID, questionID, anchorJSON, color, note string) (int64, error) {
	res, err := s.DB.ExecContext(ctx, `
		INSERT INTO highlights(session_id, question_id, anchor_json, color, note, created_at)
		VALUES (?, ?, ?, ?, ?, ?)`,
		sessionID, questionID, anchorJSON, color, nullable(note), time.Now().UnixMilli(),
	)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// DeleteHighlight removes a highlight by id, scoped to the session.
func DeleteHighlight(ctx context.Context, s *store.Store, sessionID string, id int64) error {
	_, err := s.DB.ExecContext(ctx, `DELETE FROM highlights WHERE session_id = ? AND id = ?`, sessionID, id)
	return err
}

func nullable(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func newID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(b[:]), nil
}
