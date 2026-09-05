package auth

import (
	"crypto/subtle"
	"net/http"
	"strings"

	"github.com/windowsfreak/leben/internal/config"
	"github.com/windowsfreak/leben/internal/db"
	"golang.org/x/crypto/bcrypt"
)

// SessionCookieName is the HttpOnly cookie carrying the browser session token.
const SessionCookieName = "leben_session"

type Auth struct {
	cfg     *config.Config
	db      *db.DB
	limiter *RateLimiter

	// testToken is a credential wired in by unit tests so handlers can be
	// exercised without a database. Empty in production.
	testToken string
}

func New(cfg *config.Config, database *db.DB) *Auth {
	return &Auth{cfg: cfg, db: database, limiter: NewRateLimiter()}
}

// SetTestToken installs a credential accepted by VerifyToken; tests only.
func (a *Auth) SetTestToken(token string) { a.testToken = token }

// VerifyPassword checks the admin password against the configured bcrypt hash.
// Used only by the browser login and the device-flow approval page.
func (a *Auth) VerifyPassword(password string) bool {
	if password == "" || a.cfg.Admin.PasswordHash == "" {
		return false
	}
	hash := strings.Replace(a.cfg.Admin.PasswordHash, "$2y$", "$2a$", 1)
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(password)) == nil
}

// VerifyToken validates a credential extracted from a request: a Bearer/API
// token (device flow) or a browser session token from the HttpOnly cookie.
// Passwords and static secret tokens are deliberately NOT accepted here.
func (a *Auth) VerifyToken(token string) bool {
	if token == "" {
		return false
	}
	if a.testToken != "" && subtle.ConstantTimeCompare([]byte(token), []byte(a.testToken)) == 1 {
		return true
	}
	return a.VerifyAPIToken(token)
}

// ExtractToken fetches the credential from the Authorization header
// (Bearer), the X-Admin-Token header, or the session cookie.
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
	if cookie, err := r.Cookie(SessionCookieName); err == nil && cookie.Value != "" {
		return cookie.Value
	}
	return ""
}

// Middleware protects admin handlers
func (a *Auth) Middleware(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		token := ExtractToken(r)
		if !a.VerifyToken(token) {
			w.Header().Set("Content-Type", "application/json")
			w.Header().Set("WWW-Authenticate", `Bearer error="invalid_token"`)
			w.WriteHeader(http.StatusUnauthorized)
			w.Write([]byte(`{"status":"error","message":"Unauthorized. Admin token required."}`))
			return
		}
		next(w, r)
	}
}

// IsAdmin checks request authorization without writing response
func (a *Auth) IsAdmin(r *http.Request) bool {
	return a.VerifyToken(ExtractToken(r))
}

// AllowRate reports whether one event of the given scope for key (usually a
// client IP) is allowed. Limits are per minute with a burst allowance.
func (a *Auth) AllowRate(scope, key string, perMinute, burst int) bool {
	return a.limiter.Allow(scope+":"+key, perMinute, burst)
}
