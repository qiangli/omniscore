package server

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"net/http"
	"os"
	"strconv"
	"strings"
)

const cookieName = "omniscore_sid"

type cookieSigner struct {
	key []byte
}

// loadOrCreateKey reads a base64-encoded HMAC key from path; if absent it
// generates a fresh 32-byte key and writes it. Keys persist across restarts
// so existing student cookies remain valid.
func loadOrCreateKey(path string) ([]byte, error) {
	if raw, err := os.ReadFile(path); err == nil {
		k, err := base64.RawURLEncoding.DecodeString(strings.TrimSpace(string(raw)))
		if err != nil {
			return nil, err
		}
		if len(k) >= 16 {
			return k, nil
		}
	}
	k := make([]byte, 32)
	if _, err := rand.Read(k); err != nil {
		return nil, err
	}
	if err := os.WriteFile(path, []byte(base64.RawURLEncoding.EncodeToString(k)), 0o600); err != nil {
		return nil, err
	}
	return k, nil
}

func (cs *cookieSigner) sign(studentID int64) string {
	payload := strconv.FormatInt(studentID, 10)
	h := hmac.New(sha256.New, cs.key)
	h.Write([]byte(payload))
	sig := base64.RawURLEncoding.EncodeToString(h.Sum(nil))
	return payload + "." + sig
}

func (cs *cookieSigner) verify(value string) (int64, error) {
	parts := strings.SplitN(value, ".", 2)
	if len(parts) != 2 {
		return 0, errors.New("malformed cookie")
	}
	h := hmac.New(sha256.New, cs.key)
	h.Write([]byte(parts[0]))
	expected := base64.RawURLEncoding.EncodeToString(h.Sum(nil))
	if !hmac.Equal([]byte(parts[1]), []byte(expected)) {
		return 0, errors.New("bad signature")
	}
	return strconv.ParseInt(parts[0], 10, 64)
}

func setStudentCookie(w http.ResponseWriter, cs *cookieSigner, studentID int64) {
	http.SetCookie(w, &http.Cookie{
		Name:     cookieName,
		Value:    cs.sign(studentID),
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   60 * 60 * 24 * 30, // 30 days
	})
}

func studentFromRequest(r *http.Request, cs *cookieSigner) (int64, error) {
	c, err := r.Cookie(cookieName)
	if err != nil {
		return 0, err
	}
	return cs.verify(c.Value)
}
