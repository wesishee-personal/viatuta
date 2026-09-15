// Package store owns the database connection and the hand-written SQL that
// sqlc compiles into typed Go (see internal/store/queries and the generated
// package in internal/store/gen).
package store

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Pool wraps a pgx connection pool.
//
// A "pool" keeps a set of open connections and hands them out as needed.
// Opening a Postgres connection costs a network round trip plus
// authentication, so doing it per request would be wasteful — the pool makes
// that cost a startup cost instead.
type Pool = pgxpool.Pool

// Open connects to Postgres and verifies the connection works.
//
// It deliberately pings before returning: a bad DATABASE_URL should fail
// loudly at startup, not on the first user request at 3am.
func Open(ctx context.Context, databaseURL string) (*Pool, error) {
	cfg, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		return nil, fmt.Errorf("store: parsing database url: %w", err)
	}

	// Sized for a single API process. The graph lives in memory, so the
	// database only serves account and saved-route traffic; a large pool
	// would just hold idle Postgres backends open.
	cfg.MaxConns = 10
	cfg.MinConns = 1
	cfg.MaxConnLifetime = time.Hour
	cfg.MaxConnIdleTime = 30 * time.Minute

	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("store: creating pool: %w", err)
	}

	pingCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := pool.Ping(pingCtx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("store: pinging database: %w", err)
	}

	return pool, nil
}

// CheckPostGIS verifies the PostGIS extension is installed.
//
// Without it, every spatial column and function we rely on is missing. The
// failure would otherwise surface as a confusing "type geometry does not
// exist" error partway through the first migration.
func CheckPostGIS(ctx context.Context, pool *Pool) error {
	var version string
	err := pool.QueryRow(ctx, `SELECT extversion FROM pg_extension WHERE extname = 'postgis'`).Scan(&version)
	if err != nil {
		return fmt.Errorf("store: postgis extension not found (run: CREATE EXTENSION postgis): %w", err)
	}
	return nil
}
