package auth

import (
	"crypto/rand"
	"crypto/subtle"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// Device authorization grants (RFC 8628): an OAuth-style flow where a device
// (e.g. the MCP server) requests a code, the human approves it in a browser
// after logging in, and the device polls until an access token is issued.
// The password never leaves the browser-to-server hop; the device only ever
// receives a revocable API token.

const (
	deviceGrantTTL = 10 * time.Minute
	// userCodeAlphabet excludes visually ambiguous characters (0/O, 1/I/L).
	userCodeAlphabet = "ABCDEFGHJKMNPQRSTUVWXYZ23456789"
)

var (
	ErrDevicePending  = errors.New("authorization pending")
	ErrDeviceDenied   = errors.New("user denied the request")
	ErrDeviceUsed     = errors.New("device code already used")
	ErrDeviceUnknown  = errors.New("unknown or expired device code")
	ErrNoPendingGrant = errors.New("no pending grant for this user code")
)

type DeviceGrant struct {
	DeviceCode string // plaintext, returned to the device exactly once
	UserCode   string
	ExpiresIn  int
}

// newUserCode generates a code like "K7M3-QP9X".
func newUserCode() (string, error) {
	buf := make([]byte, 8)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	out := make([]byte, 8)
	for i, b := range buf {
		out[i] = userCodeAlphabet[int(b)%len(userCodeAlphabet)]
	}
	return fmt.Sprintf("%s-%s", string(out[:4]), string(out[4:])), nil
}

// CreateDeviceGrant starts a new device authorization attempt.
func (a *Auth) CreateDeviceGrant() (*DeviceGrant, error) {
	if a.db == nil {
		return nil, errors.New("no database connection")
	}
	a.PurgeExpiredDeviceGrants()

	secret, err := randomURLSafe(32)
	if err != nil {
		return nil, err
	}
	deviceCode := tokenPrefix + "dev_" + secret

	var userCode string
	for attempt := 0; attempt < 5; attempt++ {
		candidate, err := newUserCode()
		if err != nil {
			return nil, err
		}
		var exists bool
		err = a.db.QueryRow(
			`SELECT EXISTS(SELECT 1 FROM device_grants
			 WHERE user_code = $1 AND status = 'pending' AND expires_at > CURRENT_TIMESTAMP)`,
			candidate,
		).Scan(&exists)
		if err != nil {
			return nil, err
		}
		if !exists {
			userCode = candidate
			break
		}
	}
	if userCode == "" {
		return nil, errors.New("could not allocate a unique user code")
	}

	if _, err := a.db.Exec(
		`INSERT INTO device_grants (device_code_hash, user_code, status, expires_at)
		 VALUES ($1, $2, 'pending', $3)`,
		HashToken(deviceCode), userCode, time.Now().Add(deviceGrantTTL),
	); err != nil {
		return nil, err
	}
	return &DeviceGrant{DeviceCode: deviceCode, UserCode: userCode, ExpiresIn: int(deviceGrantTTL.Seconds())}, nil
}

// AuthorizeDeviceGrant approves or denies the pending grant for userCode.
// Called from the browser after the admin has authenticated with a password.
func (a *Auth) AuthorizeDeviceGrant(userCode string, approve bool) error {
	if a.db == nil {
		return errors.New("no database connection")
	}
	status := "denied"
	if approve {
		status = "approved"
	}
	res, err := a.db.Exec(
		`UPDATE device_grants SET status = $1
		 WHERE user_code = $2 AND status = 'pending' AND expires_at > CURRENT_TIMESTAMP`,
		status, userCode,
	)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNoPendingGrant
	}
	return nil
}

// ConsumeDeviceGrant is called by the device while polling. Depending on grant
// state it returns ErrDevicePending / ErrDeviceDenied / ErrDeviceUnknown, or
// issues a fresh API token exactly once.
func (a *Auth) ConsumeDeviceGrant(deviceCode, userAgent, ip string) (string, error) {
	if a.db == nil {
		return "", errors.New("no database connection")
	}
	codeHash := HashToken(deviceCode)

	var status string
	err := a.db.QueryRow(
		`SELECT status FROM device_grants WHERE device_code_hash = $1 AND expires_at > CURRENT_TIMESTAMP`,
		codeHash,
	).Scan(&status)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrDeviceUnknown
	}
	if err != nil {
		return "", err
	}

	switch status {
	case "pending":
		return "", ErrDevicePending
	case "denied":
		return "", ErrDeviceDenied
	case "approved":
		plain, err := a.issueTokenForGrant(codeHash, userAgent, ip)
		if err != nil {
			return "", err
		}
		return plain, nil
	default: // consumed
		return "", ErrDeviceUsed
	}
}

// issueTokenForGrant atomically converts an approved grant into an API token.
// The conditional UPDATE guarantees a single winner among concurrent polls.
func (a *Auth) issueTokenForGrant(codeHash, userAgent, ip string) (string, error) {
	userAgent = TruncateUserAgent(userAgent)
	secret, err := randomURLSafe(32)
	if err != nil {
		return "", err
	}
	plain := tokenPrefix + secret

	tx, err := a.db.Begin()
	if err != nil {
		return "", err
	}
	defer tx.Rollback()

	var tokenID int
	if err := tx.QueryRow(
		`INSERT INTO api_tokens (name, token_hash, kind, user_agent, ip) VALUES ($1, $2, 'device', $3, $4) RETURNING id`,
		"device-flow", HashToken(plain), userAgent, ip,
	).Scan(&tokenID); err != nil {
		return "", err
	}
	res, err := tx.Exec(
		`UPDATE device_grants SET status = 'consumed', token_id = $1
		 WHERE device_code_hash = $2 AND status = 'approved'`,
		tokenID, codeHash,
	)
	if err != nil {
		return "", err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return "", ErrDeviceUsed
	}
	if err := tx.Commit(); err != nil {
		return "", err
	}
	return plain, nil
}

// MatchDeviceCode is a constant-time comparison helper for device codes.
func MatchDeviceCode(a, b string) bool {
	return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}
