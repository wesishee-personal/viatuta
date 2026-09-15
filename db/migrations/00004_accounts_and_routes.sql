-- +goose Up

CREATE TABLE user_account (
    id            uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    email         text NOT NULL,
    password_hash text NOT NULL,
    display_name  text,
    created_at    timestamptz NOT NULL DEFAULT now(),
    updated_at    timestamptz NOT NULL DEFAULT now()
);

-- Email comparison must be case-insensitive: Wes@example.com and
-- wes@example.com are the same account. Indexing lower(email) enforces that
-- at the database level, which is the only place it cannot be forgotten.
CREATE UNIQUE INDEX user_account_email_idx ON user_account (lower(email));

-- Now that user_account exists, connect hazard reports to their authors.
ALTER TABLE hazard_report
    ADD CONSTRAINT hazard_report_user_fk
    FOREIGN KEY (user_id) REFERENCES user_account(id) ON DELETE SET NULL;

CREATE TABLE saved_route (
    id           uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id      uuid NOT NULL REFERENCES user_account(id) ON DELETE CASCADE,
    name         text NOT NULL,

    -- The points the rider asked for, in order.
    waypoints    geometry(MultiPoint, 4326) NOT NULL,
    -- The path the router actually produced.
    geom         geometry(LineString, 4326) NOT NULL,

    distance_m   double precision NOT NULL,
    duration_s   integer,
    safety_score real,

    -- The exact profile and weights used. Stored so a saved route can be
    -- explained or recomputed later, even after the defaults change.
    profile      jsonb NOT NULL DEFAULT '{}'::jsonb,

    created_at   timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX saved_route_user_idx ON saved_route (user_id, created_at DESC);
CREATE INDEX saved_route_geom_idx ON saved_route USING GIST (geom);

-- +goose Down
DROP TABLE IF EXISTS saved_route;
ALTER TABLE hazard_report DROP CONSTRAINT IF EXISTS hazard_report_user_fk;
DROP TABLE IF EXISTS user_account;
