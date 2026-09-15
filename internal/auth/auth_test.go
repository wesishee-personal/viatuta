package auth

import (
	"errors"
	"strings"
	"testing"
	"time"
)

func TestHashAndCheckPassword(t *testing.T) {
	const pw = "correct horse battery staple"

	hash, err := HashPassword(pw)
	if err != nil {
		t.Fatalf("hashing: %v", err)
	}
	// The stored value must not contain the password.
	if strings.Contains(hash, pw) {
		t.Fatal("hash contains the plaintext password")
	}
	if err := CheckPassword(hash, pw); err != nil {
		t.Errorf("correct password rejected: %v", err)
	}
	if err := CheckPassword(hash, pw+"x"); !errors.Is(err, ErrInvalidCredentials) {
		t.Errorf("wrong password accepted or wrong error: %v", err)
	}
}

// TestHashesAreSalted checks that identical passwords produce different
// hashes. Without a salt, an attacker could crack every account sharing a
// password at once, and spot which users share one just by reading the table.
func TestHashesAreSalted(t *testing.T) {
	a, err := HashPassword("the same password")
	if err != nil {
		t.Fatal(err)
	}
	b, err := HashPassword("the same password")
	if err != nil {
		t.Fatal(err)
	}
	if a == b {
		t.Error("identical passwords produced identical hashes; the salt is missing")
	}
	// Both must still verify.
	if CheckPassword(a, "the same password") != nil || CheckPassword(b, "the same password") != nil {
		t.Error("salted hashes failed to verify")
	}
}

func TestValidatePassword(t *testing.T) {
	cases := []struct {
		name string
		pw   string
		ok   bool
	}{
		{"long passphrase", "correct horse battery staple", true},
		{"exactly minimum", strings.Repeat("a", MinPasswordLen), true},
		{"one short", strings.Repeat("a", MinPasswordLen-1), false},
		{"empty", "", false},
		// bcrypt ignores everything past 72 bytes, so an over-long password
		// would be silently weaker than the user believes.
		{"absurdly long", strings.Repeat("a", MaxPasswordLen+1), false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidatePassword(tc.pw)
			if (err == nil) != tc.ok {
				t.Errorf("ValidatePassword = %v, want ok=%v", err, tc.ok)
			}
		})
	}
}

func TestNormaliseEmail(t *testing.T) {
	for _, in := range []string{"Wes@Example.COM", "  wes@example.com  ", "wes@example.com"} {
		if got := NormaliseEmail(in); got != "wes@example.com" {
			t.Errorf("NormaliseEmail(%q) = %q", in, got)
		}
	}
}

func TestValidateEmail(t *testing.T) {
	good := []string{"a@b.co", "wes+bike@example.com", "user.name@sub.domain.org"}
	bad := []string{"", "no-at-sign", "@nolocal.com", "trailing@", "two@@at.com", "has space@x.com"}

	for _, e := range good {
		if err := ValidateEmail(e); err != nil {
			t.Errorf("ValidateEmail(%q) rejected: %v", e, err)
		}
	}
	for _, e := range bad {
		if err := ValidateEmail(e); err == nil {
			t.Errorf("ValidateEmail(%q) should have been rejected", e)
		}
	}
}

func testSigner(t *testing.T) *Signer {
	t.Helper()
	s, err := NewSigner("a-test-secret-of-sufficient-length")
	if err != nil {
		t.Fatalf("NewSigner: %v", err)
	}
	return s
}

func TestIssueAndVerify(t *testing.T) {
	s := testSigner(t)

	token, expires, err := s.Issue("user-123", "wes@example.com")
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	if !expires.After(time.Now()) {
		t.Error("token expires in the past")
	}

	claims, err := s.Verify(token)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if claims.UserID() != "user-123" {
		t.Errorf("UserID = %q, want user-123", claims.UserID())
	}
	if claims.Email != "wes@example.com" {
		t.Errorf("Email = %q", claims.Email)
	}
}

func TestShortSecretIsRejected(t *testing.T) {
	// A guessable secret means anyone can mint a token for any account.
	if _, err := NewSigner("short"); err == nil {
		t.Error("a short signing secret should be rejected")
	}
}

func TestTokenFromAnotherSecretIsRejected(t *testing.T) {
	a := testSigner(t)
	b, err := NewSigner("a-completely-different-secret-value")
	if err != nil {
		t.Fatal(err)
	}

	token, _, err := a.Issue("user-123", "wes@example.com")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := b.Verify(token); err == nil {
		t.Error("a token signed with another key was accepted")
	}
}

func TestTamperedTokenIsRejected(t *testing.T) {
	s := testSigner(t)
	token, _, err := s.Issue("user-123", "wes@example.com")
	if err != nil {
		t.Fatal(err)
	}

	// Flip a character in the payload segment; the signature must no longer
	// match.
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		t.Fatalf("expected three JWT segments, got %d", len(parts))
	}
	body := []byte(parts[1])
	body[0] ^= 0x01
	tampered := parts[0] + "." + string(body) + "." + parts[2]

	if _, err := s.Verify(tampered); err == nil {
		t.Error("a tampered token was accepted")
	}
}

// TestAlgNoneIsRejected covers the best-known JWT forgery: a token declaring
// that it is unsigned. Any parser that honours the header's algorithm field
// without pinning it will accept an attacker-authored identity.
func TestAlgNoneIsRejected(t *testing.T) {
	s := testSigner(t)

	// {"alg":"none","typ":"JWT"} . {"sub":"attacker"} . <empty signature>
	forged := "eyJhbGciOiJub25lIiwidHlwIjoiSldUIn0." +
		"eyJzdWIiOiJhdHRhY2tlciIsImlzcyI6InZpYXR1dGEifQ."

	if _, err := s.Verify(forged); err == nil {
		t.Error("an unsigned 'alg: none' token was accepted")
	}
}

func TestGarbageTokenIsRejected(t *testing.T) {
	s := testSigner(t)
	for _, bad := range []string{"", "not-a-token", "a.b.c", "....."} {
		if _, err := s.Verify(bad); !errors.Is(err, ErrInvalidToken) {
			t.Errorf("Verify(%q) = %v, want ErrInvalidToken", bad, err)
		}
	}
}
