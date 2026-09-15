package auth

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ErrEmailTaken is returned when an address is already registered.
var ErrEmailTaken = errors.New("that email is already registered")

// User is an account, without the password hash.
//
// The hash is deliberately absent from this struct rather than merely omitted
// from JSON: a field that does not exist cannot be leaked by a future handler
// that serialises the whole object.
type User struct {
	ID          string    `json:"id"`
	Email       string    `json:"email"`
	DisplayName string    `json:"display_name,omitempty"`
	CreatedAt   time.Time `json:"created_at"`
}

// Register creates an account.
func Register(ctx context.Context, pool *pgxpool.Pool, email, password, displayName string) (User, error) {
	email = NormaliseEmail(email)
	if err := ValidateEmail(email); err != nil {
		return User{}, err
	}

	hash, err := HashPassword(password)
	if err != nil {
		return User{}, err
	}

	var u User
	err = pool.QueryRow(ctx, `
		INSERT INTO user_account (email, password_hash, display_name)
		VALUES ($1, $2, NULLIF($3, ''))
		RETURNING id, email, coalesce(display_name, ''), created_at`,
		email, hash, displayName).
		Scan(&u.ID, &u.Email, &u.DisplayName, &u.CreatedAt)
	if err != nil {
		// 23505 is unique_violation. Translating it here keeps the race
		// window closed: checking for an existing address first and then
		// inserting would let two simultaneous registrations both pass the
		// check. The database constraint is the only real arbiter.
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			return User{}, ErrEmailTaken
		}
		return User{}, fmt.Errorf("auth: registering user: %w", err)
	}
	return u, nil
}

// Authenticate verifies credentials and returns the user.
func Authenticate(ctx context.Context, pool *pgxpool.Pool, email, password string) (User, error) {
	email = NormaliseEmail(email)

	var u User
	var hash string
	err := pool.QueryRow(ctx, `
		SELECT id, email, coalesce(display_name, ''), created_at, password_hash
		FROM user_account WHERE lower(email) = $1`, email).
		Scan(&u.ID, &u.Email, &u.DisplayName, &u.CreatedAt, &hash)

	if errors.Is(err, pgx.ErrNoRows) {
		// Hash a dummy password anyway so that a request for an unknown
		// address takes the same time as one for a known address. Returning
		// early here would make account enumeration possible by stopwatch.
		_, _ = HashPassword("timing-equalisation-placeholder")
		return User{}, ErrInvalidCredentials
	}
	if err != nil {
		return User{}, fmt.Errorf("auth: looking up user: %w", err)
	}

	if err := CheckPassword(hash, password); err != nil {
		return User{}, ErrInvalidCredentials
	}
	return u, nil
}

// ByID loads a user by their uuid.
func ByID(ctx context.Context, pool *pgxpool.Pool, id string) (User, error) {
	var u User
	err := pool.QueryRow(ctx, `
		SELECT id, email, coalesce(display_name, ''), created_at
		FROM user_account WHERE id = $1`, id).
		Scan(&u.ID, &u.Email, &u.DisplayName, &u.CreatedAt)
	if err != nil {
		return User{}, fmt.Errorf("auth: loading user: %w", err)
	}
	return u, nil
}
