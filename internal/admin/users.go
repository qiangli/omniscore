package admin

import (
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/qiangli/omniscore/internal/store"
	omsync "github.com/qiangli/omniscore/internal/sync"
)

const (
	RoleAdmin   = "admin"
	RoleTeacher = "teacher"
	RoleStudent = "student"
)

type createUserRequest struct {
	ID   string `json:"id,omitempty"`
	Name string `json:"name"`
	Role string `json:"role"`
}

type updateUserRequest struct {
	Name *string `json:"name,omitempty"`
	Role *string `json:"role,omitempty"`
}

func validRole(s string) bool {
	switch s {
	case RoleAdmin, RoleTeacher, RoleStudent:
		return true
	}
	return false
}

// generateUserID returns a 12-char URL-safe random id (72 bits of entropy).
// Used when the admin omits id at create time.
func generateUserID() (string, error) {
	var b [9]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b[:]), nil
}

func createUserHandler(s *store.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req createUserRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, "bad json")
			return
		}
		if strings.TrimSpace(req.Name) == "" {
			writeError(w, http.StatusBadRequest, "name required")
			return
		}
		if !validRole(req.Role) {
			writeError(w, http.StatusBadRequest, "role must be admin|teacher|student")
			return
		}
		id := strings.TrimSpace(req.ID)
		if id == "" {
			gen, err := generateUserID()
			if err != nil {
				writeError(w, http.StatusInternalServerError, err.Error())
				return
			}
			id = gen
		}
		u := userRow{ID: id, Role: req.Role, Name: req.Name, CreatedAt: time.Now().UnixMilli()}

		tx, err := s.DB.BeginTx(r.Context(), nil)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		defer tx.Rollback()
		if _, err := tx.ExecContext(r.Context(),
			`INSERT INTO users(id, role, name, created_at) VALUES (?, ?, ?, ?)`,
			u.ID, u.Role, u.Name, u.CreatedAt); err != nil {
			if isUniqueViolation(err) {
				writeError(w, http.StatusConflict, "user id already exists")
				return
			}
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		payload, _ := json.Marshal(u)
		if err := omsync.Enqueue(r.Context(), tx, omsync.EntityUser, u.ID, omsync.OpUpsert, payload); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		if err := tx.Commit(); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, http.StatusCreated, u)
	}
}

func updateUserHandler(s *store.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := chi.URLParam(r, "id")
		var req updateUserRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, "bad json")
			return
		}
		if req.Name == nil && req.Role == nil {
			writeError(w, http.StatusBadRequest, "no fields to update")
			return
		}
		if req.Name != nil && strings.TrimSpace(*req.Name) == "" {
			writeError(w, http.StatusBadRequest, "name must not be empty")
			return
		}
		if req.Role != nil && !validRole(*req.Role) {
			writeError(w, http.StatusBadRequest, "role must be admin|teacher|student")
			return
		}
		tx, err := s.DB.BeginTx(r.Context(), nil)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		defer tx.Rollback()
		var current userRow
		err = tx.QueryRowContext(r.Context(),
			`SELECT id, role, name, created_at FROM users WHERE id = ? AND deleted_at IS NULL`, id).
			Scan(&current.ID, &current.Role, &current.Name, &current.CreatedAt)
		if errors.Is(err, sql.ErrNoRows) {
			writeError(w, http.StatusNotFound, "no such user")
			return
		}
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		if req.Name != nil {
			current.Name = *req.Name
		}
		if req.Role != nil {
			current.Role = *req.Role
		}
		if _, err := tx.ExecContext(r.Context(),
			`UPDATE users SET role = ?, name = ? WHERE id = ?`,
			current.Role, current.Name, current.ID); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		payload, _ := json.Marshal(current)
		if err := omsync.Enqueue(r.Context(), tx, omsync.EntityUser, current.ID, omsync.OpUpsert, payload); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		if err := tx.Commit(); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, current)
	}
}

func deleteUserHandler(s *store.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := chi.URLParam(r, "id")
		tx, err := s.DB.BeginTx(r.Context(), nil)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		defer tx.Rollback()
		var existing string
		var deletedAt sql.NullInt64
		err = tx.QueryRowContext(r.Context(),
			`SELECT id, deleted_at FROM users WHERE id = ?`, id).
			Scan(&existing, &deletedAt)
		if errors.Is(err, sql.ErrNoRows) {
			writeError(w, http.StatusNotFound, "no such user")
			return
		}
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		// Idempotent: already deleted, no state change, no outbox row.
		if deletedAt.Valid {
			if err := tx.Commit(); err != nil {
				writeError(w, http.StatusInternalServerError, err.Error())
				return
			}
			w.WriteHeader(http.StatusNoContent)
			return
		}
		if _, err := tx.ExecContext(r.Context(),
			`UPDATE users SET deleted_at = ? WHERE id = ?`, time.Now().UnixMilli(), id); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		if err := omsync.Enqueue(r.Context(), tx, omsync.EntityUser, id, omsync.OpDelete, []byte(`{}`)); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		if err := tx.Commit(); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

// isUniqueViolation pattern-matches SQLite's UNIQUE constraint error. The
// modernc.org/sqlite driver does not expose a typed error for it, so a
// substring check is the pragmatic option.
func isUniqueViolation(err error) bool {
	return err != nil && strings.Contains(strings.ToUpper(err.Error()), "UNIQUE")
}
