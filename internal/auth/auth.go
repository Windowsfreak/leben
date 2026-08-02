package auth

import (
	"crypto/subtle"
	"net/http"
	"strings"

	"github.com/windowsfreak/leben/internal/config"
	"golang.org/x/crypto/bcrypt"
)

type Auth struct {
	cfg *config.Config
}

func New(cfg *config.Config) *Auth {
	return &Auth{cfg: cfg}
}

// VerifyPassword checks if input password matches configured bcrypt hash or master secret token
func (a *Auth) VerifyPassword(password string) bool {
	if password == "" {
		return false
	}
	// Check against secret token directly
	if a.cfg.Admin.SecretToken != "" && subtle.ConstantTimeCompare([]byte(password), []byte(a.cfg.Admin.SecretToken)) == 1 {
		return true
	}
	// Check against bcrypt hash
	if a.cfg.Admin.PasswordHash != "" {
		hash := strings.Replace(a.cfg.Admin.PasswordHash, "$2y$", "$2a$", 1)
		err := bcrypt.CompareHashAndPassword([]byte(hash), []byte(password))
		return err == nil
	}
	return false
}

// VerifyToken checks Bearer token or custom admin header
func (a *Auth) VerifyToken(token string) bool {
	if token == "" {
		return false
	}
	if a.cfg.Admin.SecretToken != "" && subtle.ConstantTimeCompare([]byte(token), []byte(a.cfg.Admin.SecretToken)) == 1 {
		return true
	}
	// Also accept correct password as token
	return a.VerifyPassword(token)
}

// ExtractToken fetches token from Bearer header, X-Admin-Token header, or Authorization header
func ExtractToken(r *http.Request) string {
	if authHeader := r.Header.Get("Authorization"); authHeader != "" {
		if len(authHeader) > 7 && authHeader[:7] == "Bearer " {
			return authHeader[7:]
		}
		return authHeader
	}
	if token := r.Header.Get("X-Admin-Token"); token != "" {
		return token
	}
	if token := r.Header.Get("X-Admin-Password"); token != "" {
		return token
	}
	return ""
}

// Middleware protects admin handlers
func (a *Auth) Middleware(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		token := ExtractToken(r)
		if !a.VerifyToken(token) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusUnauthorized)
			w.Write([]byte(`{"status":"error","message":"Unauthorized. Admin token required."}`))
			return
		}
		next(w, r)
	}
}

// IsAdmin checks request authorization without writing response
func (a *Auth) IsAdmin(r *http.Request) bool {
	token := ExtractToken(r)
	return a.VerifyToken(token)
}
