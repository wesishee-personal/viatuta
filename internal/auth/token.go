package auth

import (
	"errors"
	"fmt"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// TokenTTL is how long an access token stays valid.
//
// JWTs cannot be revoked before they expire — that is the price of not
// consulting the database on every request. A short lifetime bounds the
// damage from a leaked token; anything longer wants a refresh-token flow or
// a server-side session store instead.
const TokenTTL = 24 * time.Hour

// Issuer identifies tokens minted by this service.
const Issuer = "viatuta"

var (
	ErrInvalidToken = errors.New("invalid or expired token")
	ErrNoSecret     = errors.New("auth: no signing secret configured")
)

// Claims is the payload carried by an access token.
//
// Only the user id and email go in. A JWT is signed, not encrypted: anyone
// holding the token can read its contents, so it must carry nothing private.
type Claims struct {
	Email string `json:"email"`
	jwt.RegisteredClaims
}

// UserID returns the subject, which is the account's uuid.
func (c Claims) UserID() string { return c.Subject }

// Signer issues and verifies access tokens.
type Signer struct {
	secret []byte
}

// NewSigner builds a Signer from the configured secret.
func NewSigner(secret string) (*Signer, error) {
	// A short secret makes brute-forcing the signature feasible, which would
	// let anyone mint a token for any account.
	if len(secret) < 16 {
		return nil, fmt.Errorf("%w: must be at least 16 characters", ErrNoSecret)
	}
	return &Signer{secret: []byte(secret)}, nil
}

// Issue mints a signed token for a user.
func (s *Signer) Issue(userID, email string) (string, time.Time, error) {
	now := time.Now()
	expires := now.Add(TokenTTL)

	claims := Claims{
		Email: email,
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   userID,
			Issuer:    Issuer,
			IssuedAt:  jwt.NewNumericDate(now),
			NotBefore: jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(expires),
		},
	}

	signed, err := jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString(s.secret)
	if err != nil {
		return "", time.Time{}, fmt.Errorf("auth: signing token: %w", err)
	}
	return signed, expires, nil
}

// Verify parses and validates a token, returning its claims.
func (s *Signer) Verify(tokenString string) (*Claims, error) {
	token, err := jwt.ParseWithClaims(tokenString, &Claims{},
		func(t *jwt.Token) (any, error) { return s.secret, nil },

		// Pinning the algorithm is the single most important line here.
		// Without it a forged token could declare alg "none", or claim
		// RS256 so the parser treats our HMAC secret as a public key — both
		// classic JWT forgeries that let an attacker mint any identity.
		jwt.WithValidMethods([]string{jwt.SigningMethodHS256.Alg()}),
		jwt.WithIssuer(Issuer),
		jwt.WithExpirationRequired(),
	)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidToken, err)
	}

	claims, ok := token.Claims.(*Claims)
	if !ok || !token.Valid || claims.Subject == "" {
		return nil, ErrInvalidToken
	}
	return claims, nil
}
