package auth

import (
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/windowsfreak/leben/internal/config"
)

func TestHashTokenIsDeterministicAndHex(t *testing.T) {
	h1 := HashToken("lbn_abc")
	h2 := HashToken("lbn_abc")
	h3 := HashToken("lbn_abd")
	if h1 != h2 {
		t.Fatal("hashing the same token twice must be deterministic")
	}
	if h1 == h3 {
		t.Fatal("different tokens must hash differently")
	}
	if len(h1) != 64 {
		t.Fatalf("expected 64-char sha256 hex, got %d chars", len(h1))
	}
	for _, c := range h1 {
		if !strings.ContainsRune("0123456789abcdef", c) {
			t.Fatalf("non-hex character %q in hash", c)
		}
	}
}

func TestNewUserCodeFormat(t *testing.T) {
	known := regexp.MustCompile(`^[A-HJ-KM-NP-Z2-9]{4}-[A-HJ-KM-NP-Z2-9]{4}$`)
	seen := map[string]bool{}
	for i := 0; i < 100; i++ {
		code, err := newUserCode()
		if err != nil {
			t.Fatalf("newUserCode error: %v", err)
		}
		if !known.MatchString(code) {
			t.Fatalf("unexpected user code format: %q", code)
		}
		if seen[code] {
			t.Fatalf("duplicate user code generated: %q", code)
		}
		seen[code] = true
	}
}

func TestRateLimiterBurstAndRefill(t *testing.T) {
	rl := NewRateLimiter()
	key := "test:1.2.3.4"

	for i := 0; i < 5; i++ {
		if !rl.Allow(key, 60, 5) {
			t.Fatalf("request %d within burst should be allowed", i+1)
		}
	}
	if rl.Allow(key, 60, 5) {
		t.Fatal("request beyond burst should be denied")
	}

	// Separate keys are independent.
	if !rl.Allow("test:5.6.7.8", 60, 5) {
		t.Fatal("a different key should have its own budget")
	}

	// Token refills over time: force an old last-seen timestamp.
	rl.mu.Lock()
	rl.buckets[key].last = rl.buckets[key].last.Add(-10 * time.Second)
	rl.mu.Unlock()
	if !rl.Allow(key, 60, 5) {
		t.Fatal("after enough refill time one request should be allowed again")
	}
}

func TestVerifyPasswordAndTokenWithNilDB(t *testing.T) {
	// Example config hash corresponds to password 'admin123'.
	cfg := &config.Config{
		Admin: config.AdminConfig{
			PasswordHash: "$2y$10$.8UcGLDKeZDoCLVZjNSac.7vwrMVwj1QLZLhQOLDSH5Ywo14QRhp2",
		},
	}
	a := New(cfg, nil)

	// VerifyPassword: bcrypt check only — the password gate for browser login
	// and device-flow approval.
	if !a.VerifyPassword("admin123") {
		t.Fatal("correct password should verify")
	}
	if a.VerifyPassword("wrong") {
		t.Fatal("wrong password must not verify")
	}

	// VerifyToken: passwords must NOT work as bearer credentials anymore, and
	// without a database no API/session token can validate.
	for _, cred := range []string{"admin123", "lbn_doesnotexist", "lbn_dev_whatever"} {
		if a.VerifyToken(cred) {
			t.Fatalf("credential %q must not verify as bearer token", cred)
		}
	}
	if a.VerifyToken("") {
		t.Fatal("empty token must not verify")
	}

	// The test seam makes a credential valid without a DB (used by router tests).
	a.SetTestToken("test_token_123")
	if !a.VerifyToken("test_token_123") {
		t.Fatal("installed test token should verify")
	}
	if a.VerifyToken("test_token_124") {
		t.Fatal("similar token must not verify")
	}
}

func TestAllowRateZeroLimitAllowsEverything(t *testing.T) {
	rl := NewRateLimiter()
	for i := 0; i < 100; i++ {
		if !rl.Allow("disabled:key", 0, 0) {
			t.Fatal("limits <= 0 mean 'no limit'")
		}
	}
}

func TestTruncateUserAgent(t *testing.T) {
	short := "Mozilla/5.0"
	if TruncateUserAgent(short) != short {
		t.Fatalf("short string should not be truncated")
	}

	long := strings.Repeat("a", 300)
	truncated := TruncateUserAgent(long)
	if len([]rune(truncated)) != 255 {
		t.Fatalf("expected 255 runes, got %d", len([]rune(truncated)))
	}

	// Multibyte unicode characters
	unicodeLong := strings.Repeat("🚀", 300)
	unicodeTruncated := TruncateUserAgent(unicodeLong)
	if len([]rune(unicodeTruncated)) != 255 {
		t.Fatalf("expected 255 runes, got %d", len([]rune(unicodeTruncated)))
	}
}
