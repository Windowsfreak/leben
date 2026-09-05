package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"log"
	"time"

	"github.com/windowsfreak/leben/internal/models"
)

const tokenPrefix = "lbn_"

// HashToken returns the hex-encoded SHA-256 of a token. Only hashes are ever
// stored server-side, so a database leak does not expose usable credentials.
func HashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

// randomURLSafe returns n bytes of crypto/rand as a base64url string.
func randomURLSafe(n int) (string, error) {
	buf := make([]byte, n)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("crypto/rand unavailable: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}

// CreateSessionToken issues a browser login session. The plaintext token is
// returned once (it becomes the value of the HttpOnly cookie) and only its
// hash is persisted.
func (a *Auth) CreateSessionToken(userAgent, ip string, ttl time.Duration) (string, *time.Time, error) {
	expires := time.Now().Add(ttl)
	plain, err := a.createToken("session", "browser", userAgent, ip, &expires)
	return plain, &expires, err
}

// LookupTokenID resolves a presented credential to its row id (for marking
// the caller's own session in the token list). Returns ok=false when unknown.
func (a *Auth) LookupTokenID(token string) (int, bool) {
	if a.db == nil || token == "" {
		return 0, false
	}
	var id int
	err := a.db.QueryRow(
		`SELECT id FROM api_tokens WHERE token_hash = $1 AND revoked_at IS NULL`,
		HashToken(token),
	).Scan(&id)
	return id, err == nil
}

// PurgeExpiredSessions removes expired browser sessions; called on login.
func (a *Auth) PurgeExpiredSessions() {
	if a.db == nil {
		return
	}
	if _, err := a.db.Exec(`DELETE FROM api_tokens WHERE kind = 'session' AND expires_at IS NOT NULL AND expires_at < CURRENT_TIMESTAMP`); err != nil {
		log.Printf("Warning: could not purge expired sessions: %v", err)
	}
}

// TruncateUserAgent trims a User-Agent string to at most 255 runes to ensure it
// safely fits in the database column.
func TruncateUserAgent(ua string) string {
	runes := []rune(ua)
	if len(runes) > 255 {
		return string(runes[:255])
	}
	return ua
}

// createToken generates a new credential and persists only its hash.
// The plaintext token is returned once and cannot be recovered later.
func (a *Auth) createToken(kind, name, userAgent, ip string, expiresAt *time.Time) (string, error) {
	if a.db == nil {
		return "", errors.New("no database connection")
	}
	userAgent = TruncateUserAgent(userAgent)
	secret, err := randomURLSafe(32)
	if err != nil {
		return "", err
	}
	plain := tokenPrefix + secret
	_, err = a.db.Exec(
		`INSERT INTO api_tokens (name, token_hash, kind, user_agent, ip, expires_at)
		 VALUES ($1, $2, $3, $4, $5, $6)`,
		name, HashToken(plain), kind, userAgent, ip, expiresAt,
	)
	if err != nil {
		return "", fmt.Errorf("could not store token: %w", err)
	}
	return plain, nil
}

// VerifyAPIToken checks a credential against stored hashes and refreshes
// last_used_at on success.
func (a *Auth) VerifyAPIToken(token string) bool {
	if a.db == nil || token == "" {
		return false
	}
	var id int
	err := a.db.QueryRow(
		`UPDATE api_tokens SET last_used_at = CURRENT_TIMESTAMP
		 WHERE token_hash = $1 AND revoked_at IS NULL
		   AND (expires_at IS NULL OR expires_at > CURRENT_TIMESTAMP)
		 RETURNING id`,
		HashToken(token),
	).Scan(&id)
	return err == nil
}

// ListAPITokens returns all credentials — browser sessions and device tokens
// alike. Hashes are never included.
func (a *Auth) ListAPITokens() ([]models.ApiToken, error) {
	if a.db == nil {
		return nil, errors.New("no database connection")
	}
	rows, err := a.db.Query(
		`SELECT id, kind, name, user_agent, ip, created_at, last_used_at, expires_at, revoked_at
		 FROM api_tokens ORDER BY created_at DESC`,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	tokens := []models.ApiToken{}
	for rows.Next() {
		var t models.ApiToken
		if err := rows.Scan(&t.ID, &t.Kind, &t.Name, &t.UserAgent, &t.IP, &t.CreatedAt, &t.LastUsedAt, &t.ExpiresAt, &t.RevokedAt); err != nil {
			return nil, err
		}
		tokens = append(tokens, t)
	}
	return tokens, rows.Err()
}

// RevokeAPIToken soft-deletes a credential so it stops working immediately.
func (a *Auth) RevokeAPIToken(id int) error {
	if a.db == nil {
		return errors.New("no database connection")
	}
	res, err := a.db.Exec(`UPDATE api_tokens SET revoked_at = CURRENT_TIMESTAMP WHERE id = $1 AND revoked_at IS NULL`, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return sql.ErrNoRows
	}
	return nil
}

// PurgeExpiredDeviceGrants removes stale grant rows; called opportunistically.
func (a *Auth) PurgeExpiredDeviceGrants() {
	if a.db == nil {
		return
	}
	if _, err := a.db.Exec(`DELETE FROM device_grants WHERE expires_at < CURRENT_TIMESTAMP - INTERVAL '1 day'`); err != nil {
		log.Printf("Warning: could not purge expired device grants: %v", err)
	}
}
