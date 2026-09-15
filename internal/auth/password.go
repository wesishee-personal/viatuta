// Package auth handles accounts: password hashing, token issue and
// verification, and the user records behind them.
package auth

import (
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	"golang.org/x/crypto/bcrypt"
)

// BcryptCost controls how expensive hashing is.
//
// Cost 12 is roughly 250 ms per hash on current hardware. That is
// deliberately slow: the entire value of bcrypt is that an attacker who steals
// the database can only test a few thousand guesses per second instead of
// billions. The cost is paid once per login, where a quarter second is
// invisible to a person and ruinous to a cracker.
const BcryptCost = 12

// Password rules. Length is the only requirement, because it is the only one
// that reliably helps: composition rules ("one capital, one symbol") push
// people toward predictable patterns like "Password1!" without adding real
// entropy.
const (
	MinPasswordLen = 10
	MaxPasswordLen = 200 // bcrypt silently truncates past 72 bytes; reject long input outright
)

var (
	// ErrInvalidCredentials is returned for BOTH an unknown email and a
	// wrong password. Distinguishing them would let anyone enumerate which
	// addresses have accounts.
	ErrInvalidCredentials = errors.New("invalid email or password")

	ErrWeakPassword = errors.New("password is too weak")

	// ErrInvalidEmail lets callers distinguish bad input from a database
	// failure without inspecting error strings.
	ErrInvalidEmail = errors.New("invalid email address")
)

// HashPassword returns a bcrypt hash suitable for storage.
func HashPassword(plain string) (string, error) {
	if err := ValidatePassword(plain); err != nil {
		return "", err
	}
	h, err := bcrypt.GenerateFromPassword([]byte(plain), BcryptCost)
	if err != nil {
		return "", fmt.Errorf("auth: hashing password: %w", err)
	}
	return string(h), nil
}

// CheckPassword verifies a password against a stored hash.
//
// bcrypt's comparison is constant-time with respect to the hash, so it does
// not leak how much of a guess was correct through timing.
func CheckPassword(hash, plain string) error {
	if err := bcrypt.CompareHashAndPassword([]byte(hash), []byte(plain)); err != nil {
		return ErrInvalidCredentials
	}
	return nil
}

// ValidatePassword enforces the minimum policy.
func ValidatePassword(p string) error {
	n := utf8.RuneCountInString(p)
	if n < MinPasswordLen {
		return fmt.Errorf("%w: must be at least %d characters", ErrWeakPassword, MinPasswordLen)
	}
	// bcrypt only considers the first 72 BYTES. Accepting a longer password
	// would silently ignore the rest, so a 200-character passphrase would be
	// no stronger than its first 72 bytes while the user believed otherwise.
	if len(p) > MaxPasswordLen {
		return fmt.Errorf("%w: must be at most %d characters", ErrWeakPassword, MaxPasswordLen)
	}
	return nil
}

// NormaliseEmail lowercases and trims an address.
//
// The database enforces uniqueness on lower(email), so normalising here keeps
// application behaviour and the constraint in agreement.
func NormaliseEmail(e string) string {
	return strings.ToLower(strings.TrimSpace(e))
}

// ValidateEmail performs a deliberately minimal check.
//
// Full RFC 5322 validation is famously intricate and rejects addresses that
// genuinely work. The only reliable proof that an address exists is sending
// mail to it, so this rules out the obviously malformed and no more.
func ValidateEmail(e string) error {
	if len(e) < 3 || len(e) > 254 {
		return fmt.Errorf("%w: must be between 3 and 254 characters", ErrInvalidEmail)
	}
	at := strings.IndexByte(e, '@')
	if at <= 0 || at == len(e)-1 {
		return fmt.Errorf("%w: must contain a local part and a domain", ErrInvalidEmail)
	}
	if strings.Count(e, "@") != 1 {
		return fmt.Errorf("%w: must contain exactly one @", ErrInvalidEmail)
	}
	if strings.ContainsAny(e, " \t\n\r") {
		return fmt.Errorf("%w: must not contain whitespace", ErrInvalidEmail)
	}
	return nil
}
