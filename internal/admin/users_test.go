package admin

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	_ "modernc.org/sqlite"

	"github.com/qiangli/omniscore/internal/store"
)

func openTestStore(t *testing.T) *store.Store {
	t.Helper()
	s, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "admin.db"))
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func setURLParam(r *http.Request, key, value string) *http.Request {
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add(key, value)
	return r.WithContext(context.WithValue(r.Context(), chi.RouteCtxKey, rctx))
}

func TestUserCRUDLifecycle(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()

	// Create with caller-omitted id; server generates one.
	req := httptest.NewRequest("POST", "/admin/users",
		strings.NewReader(`{"name":"Alice","role":"teacher"}`))
	w := httptest.NewRecorder()
	createUserHandler(s).ServeHTTP(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("create: want 201, got %d body=%s", w.Code, w.Body.String())
	}
	var created userRow
	if err := json.Unmarshal(w.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	if created.ID == "" || created.Role != "teacher" || created.Name != "Alice" {
		t.Fatalf("created row malformed: %+v", created)
	}
	if created.CreatedAt == 0 {
		t.Fatalf("created_at not set: %+v", created)
	}

	// Update.
	req = httptest.NewRequest("PATCH", "/admin/users/"+created.ID,
		strings.NewReader(`{"name":"Alice Cooper","role":"admin"}`))
	req = setURLParam(req, "id", created.ID)
	w = httptest.NewRecorder()
	updateUserHandler(s).ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("update: want 200, got %d body=%s", w.Code, w.Body.String())
	}
	var updated userRow
	if err := json.Unmarshal(w.Body.Bytes(), &updated); err != nil {
		t.Fatal(err)
	}
	if updated.Name != "Alice Cooper" || updated.Role != "admin" {
		t.Fatalf("updated row: %+v", updated)
	}

	// List shows the active user.
	req = httptest.NewRequest("GET", "/admin/users", nil)
	w = httptest.NewRecorder()
	listUsersHandler(s).ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("list: want 200, got %d", w.Code)
	}
	if !strings.Contains(w.Body.String(), `"Alice Cooper"`) {
		t.Fatalf("list missing updated user: %s", w.Body.String())
	}

	// Soft delete.
	req = httptest.NewRequest("DELETE", "/admin/users/"+created.ID, nil)
	req = setURLParam(req, "id", created.ID)
	w = httptest.NewRecorder()
	deleteUserHandler(s).ServeHTTP(w, req)
	if w.Code != http.StatusNoContent {
		t.Fatalf("delete: want 204, got %d body=%s", w.Code, w.Body.String())
	}

	// Row survives — soft delete keeps FK referents valid.
	var deletedAt int64
	if err := s.DB.QueryRowContext(ctx,
		`SELECT deleted_at FROM users WHERE id = ?`, created.ID).Scan(&deletedAt); err != nil {
		t.Fatalf("row should still exist after soft delete: %v", err)
	}
	if deletedAt == 0 {
		t.Fatalf("deleted_at should be set")
	}

	// List now hides the deleted user.
	req = httptest.NewRequest("GET", "/admin/users", nil)
	w = httptest.NewRecorder()
	listUsersHandler(s).ServeHTTP(w, req)
	if strings.Contains(w.Body.String(), created.ID) {
		t.Fatalf("list should hide soft-deleted user: %s", w.Body.String())
	}

	// Outbox: 1 upsert (create) + 1 upsert (update) + 1 delete = 3 rows.
	var upserts, deletes int
	if err := s.DB.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM sync_outbox WHERE entity_type='user' AND entity_id=? AND op='upsert'`,
		created.ID).Scan(&upserts); err != nil {
		t.Fatal(err)
	}
	if err := s.DB.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM sync_outbox WHERE entity_type='user' AND entity_id=? AND op='delete'`,
		created.ID).Scan(&deletes); err != nil {
		t.Fatal(err)
	}
	if upserts != 2 || deletes != 1 {
		t.Fatalf("outbox: want 2 upserts + 1 delete, got %d + %d", upserts, deletes)
	}
}

func TestCreateUserValidation(t *testing.T) {
	s := openTestStore(t)
	cases := []struct {
		name string
		body string
		want int
	}{
		{"bad json", `not json`, http.StatusBadRequest},
		{"missing name", `{"role":"teacher"}`, http.StatusBadRequest},
		{"whitespace name", `{"name":"   ","role":"teacher"}`, http.StatusBadRequest},
		{"missing role", `{"name":"x"}`, http.StatusBadRequest},
		{"bad role", `{"name":"x","role":"wizard"}`, http.StatusBadRequest},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest("POST", "/admin/users", strings.NewReader(tc.body))
			w := httptest.NewRecorder()
			createUserHandler(s).ServeHTTP(w, req)
			if w.Code != tc.want {
				t.Fatalf("want %d, got %d body=%s", tc.want, w.Code, w.Body.String())
			}
		})
	}
}

func TestCreateUserIDCollision(t *testing.T) {
	s := openTestStore(t)
	body := `{"id":"u1","name":"a","role":"student"}`

	req := httptest.NewRequest("POST", "/admin/users", strings.NewReader(body))
	w := httptest.NewRecorder()
	createUserHandler(s).ServeHTTP(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("first create: %d", w.Code)
	}

	req = httptest.NewRequest("POST", "/admin/users", strings.NewReader(body))
	w = httptest.NewRecorder()
	createUserHandler(s).ServeHTTP(w, req)
	if w.Code != http.StatusConflict {
		t.Fatalf("dup create: want 409, got %d body=%s", w.Code, w.Body.String())
	}
}

func TestUpdateUserNotFound(t *testing.T) {
	s := openTestStore(t)
	req := httptest.NewRequest("PATCH", "/admin/users/nope",
		strings.NewReader(`{"name":"x"}`))
	req = setURLParam(req, "id", "nope")
	w := httptest.NewRecorder()
	updateUserHandler(s).ServeHTTP(w, req)
	if w.Code != http.StatusNotFound {
		t.Fatalf("update missing user: want 404, got %d", w.Code)
	}
}

func TestUpdateRejectsSoftDeletedUser(t *testing.T) {
	s := openTestStore(t)
	// create + delete
	req := httptest.NewRequest("POST", "/admin/users",
		strings.NewReader(`{"id":"u1","name":"a","role":"student"}`))
	w := httptest.NewRecorder()
	createUserHandler(s).ServeHTTP(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("create: %d", w.Code)
	}
	req = httptest.NewRequest("DELETE", "/admin/users/u1", nil)
	req = setURLParam(req, "id", "u1")
	w = httptest.NewRecorder()
	deleteUserHandler(s).ServeHTTP(w, req)
	if w.Code != http.StatusNoContent {
		t.Fatalf("delete: %d", w.Code)
	}
	// PATCH on the tombstone should 404.
	req = httptest.NewRequest("PATCH", "/admin/users/u1",
		strings.NewReader(`{"name":"new"}`))
	req = setURLParam(req, "id", "u1")
	w = httptest.NewRecorder()
	updateUserHandler(s).ServeHTTP(w, req)
	if w.Code != http.StatusNotFound {
		t.Fatalf("patch soft-deleted: want 404, got %d", w.Code)
	}
}

func TestDeleteIsIdempotent(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	req := httptest.NewRequest("POST", "/admin/users",
		strings.NewReader(`{"id":"u1","name":"x","role":"student"}`))
	w := httptest.NewRecorder()
	createUserHandler(s).ServeHTTP(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("create: %d", w.Code)
	}
	for i := 0; i < 2; i++ {
		req = httptest.NewRequest("DELETE", "/admin/users/u1", nil)
		req = setURLParam(req, "id", "u1")
		w = httptest.NewRecorder()
		deleteUserHandler(s).ServeHTTP(w, req)
		if w.Code != http.StatusNoContent {
			t.Fatalf("delete #%d: want 204, got %d", i+1, w.Code)
		}
	}
	// The second delete is a no-op and must not enqueue a second delete row.
	var n int
	_ = s.DB.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM sync_outbox WHERE entity_id = 'u1' AND op = 'delete'`).Scan(&n)
	if n != 1 {
		t.Fatalf("delete outbox rows after two deletes: want 1, got %d", n)
	}
}

func TestDeleteMissingUser(t *testing.T) {
	s := openTestStore(t)
	req := httptest.NewRequest("DELETE", "/admin/users/nope", nil)
	req = setURLParam(req, "id", "nope")
	w := httptest.NewRecorder()
	deleteUserHandler(s).ServeHTTP(w, req)
	if w.Code != http.StatusNotFound {
		t.Fatalf("delete missing user: want 404, got %d", w.Code)
	}
}
