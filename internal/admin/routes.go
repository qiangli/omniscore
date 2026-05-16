package admin

import (
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/qiangli/omniscore/internal/store"
)

// Mount installs the /api/admin/* routes on r. contentRoot is the same path
// the server was booted with (-content) so the editor can find test.json
// files and the loader can hot-reload after each write.
func Mount(r chi.Router, s *store.Store, contentRoot string, auth *Auth) {
	io := NewJSONIO(contentRoot, s)
	editor := &Editor{IO: io, Store: s}
	reviewer := &Reviewer{Store: s}

	r.Post("/admin/login", auth.Login)
	r.Post("/admin/logout", auth.Logout)

	r.Group(func(r chi.Router) {
		r.Use(auth.RequireAdmin)
		r.Get("/admin/whoami", auth.Whoami)
		r.Get("/admin/tests", reviewer.ListTests)
		r.Get("/admin/tests/{slug}", editor.GetTest)
		r.Patch("/admin/tests/{slug}/questions/{qid}", editor.PatchQuestion)
		r.Put("/admin/tests/{slug}/questions/{qid}/review", reviewer.SetReview)
		r.Post("/admin/tests/{slug}/review/bulk", reviewer.BulkSetReview)
		r.Get("/admin/sync/status", syncStatusHandler(s))
		r.Get("/admin/users", listUsersHandler(s))
		r.Post("/admin/users", createUserHandler(s))
		r.Patch("/admin/users/{id}", updateUserHandler(s))
		r.Delete("/admin/users/{id}", deleteUserHandler(s))
		r.Get("/admin/tasks", listTasksHandler(s))
	})
}

func syncStatusHandler(s *store.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var pending, failed int
		if err := s.DB.QueryRowContext(r.Context(),
			`SELECT COUNT(*) FROM sync_outbox WHERE delivered_at IS NULL`).Scan(&pending); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		if err := s.DB.QueryRowContext(r.Context(),
			`SELECT COUNT(*) FROM sync_outbox WHERE delivered_at IS NULL AND attempts > 0`).Scan(&failed); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"pending": pending, "failed": failed})
	}
}

type userRow struct {
	ID        string `json:"id"`
	Role      string `json:"role"`
	Name      string `json:"name"`
	CreatedAt int64  `json:"created_at"`
}

func listUsersHandler(s *store.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		rows, err := s.DB.QueryContext(r.Context(),
			`SELECT id, role, name, created_at FROM users WHERE deleted_at IS NULL ORDER BY created_at DESC`)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		defer rows.Close()
		out := []userRow{}
		for rows.Next() {
			var u userRow
			if err := rows.Scan(&u.ID, &u.Role, &u.Name, &u.CreatedAt); err != nil {
				writeError(w, http.StatusInternalServerError, err.Error())
				return
			}
			out = append(out, u)
		}
		writeJSON(w, http.StatusOK, map[string]any{"users": out})
	}
}

type taskRow struct {
	ID           string `json:"id"`
	UserID       string `json:"user_id"`
	TestID       string `json:"test_id"`
	FromDatetime int64  `json:"from_datetime"`
	ToDatetime   int64  `json:"to_datetime"`
	CreatedAt    int64  `json:"created_at"`
}

func listTasksHandler(s *store.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		rows, err := s.DB.QueryContext(r.Context(),
			`SELECT id, user_id, test_id, from_datetime, to_datetime, created_at
			   FROM tasks ORDER BY from_datetime DESC`)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		defer rows.Close()
		out := []taskRow{}
		for rows.Next() {
			var t taskRow
			if err := rows.Scan(&t.ID, &t.UserID, &t.TestID, &t.FromDatetime, &t.ToDatetime, &t.CreatedAt); err != nil {
				writeError(w, http.StatusInternalServerError, err.Error())
				return
			}
			out = append(out, t)
		}
		writeJSON(w, http.StatusOK, map[string]any{"tasks": out})
	}
}
