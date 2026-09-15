-- +goose Up

-- Historical collisions from TxDOT CRIS, republished on data.austintexas.gov.
-- Raw records are kept verbatim so the derived crash_score can be recomputed
-- with a different decay or severity weighting without re-downloading.
CREATE TABLE crash (
    id               bigserial PRIMARY KEY,
    source           text NOT NULL DEFAULT 'austin_cris',
    source_id        text NOT NULL,
    geom             geometry(Point, 4326) NOT NULL,
    occurred_at      timestamptz NOT NULL,

    -- KABCO scale, highest severity of any person in the crash:
    -- 4 = fatal, 3 = suspected serious, 2 = minor, 1 = possible, 0 = none.
    severity         smallint NOT NULL CHECK (severity BETWEEN 0 AND 4),

    cyclist_involved boolean NOT NULL DEFAULT false,
    raw              jsonb NOT NULL,
    ingested_at      timestamptz NOT NULL DEFAULT now()
);

-- Re-running the ingest must not duplicate records.
CREATE UNIQUE INDEX crash_source_id_idx ON crash (source, source_id);
CREATE INDEX crash_geom_idx ON crash USING GIST (geom);
CREATE INDEX crash_cyclist_idx ON crash (occurred_at) WHERE cyclist_involved;

-- Which crashes were attributed to which edge. The aggregate lives on
-- routing_edge.crash_score; this table exists so the API can explain *why*
-- a segment scored badly, and so a bad match can be debugged.
CREATE TABLE crash_edge (
    crash_id   bigint NOT NULL REFERENCES crash(id) ON DELETE CASCADE,
    edge_id    bigint NOT NULL REFERENCES routing_edge(id) ON DELETE CASCADE,
    distance_m double precision NOT NULL,
    PRIMARY KEY (crash_id, edge_id)
);

CREATE INDEX crash_edge_edge_idx ON crash_edge (edge_id);

-- Rider-submitted hazards: broken glass, a blocked lane, an aggressive
-- junction. These decay, because a hazard reported two years ago tells you
-- very little about today.
CREATE TABLE hazard_report (
    id            bigserial PRIMARY KEY,
    user_id       uuid,   -- FK added in 00004, after user_account exists
    geom          geometry(Point, 4326) NOT NULL,
    kind          text NOT NULL
                  CHECK (kind IN ('debris','pothole','blocked_lane','poor_visibility',
                                  'aggressive_traffic','construction','dangerous_junction','other')),
    notes         text,
    confirmations integer NOT NULL DEFAULT 0,
    created_at    timestamptz NOT NULL DEFAULT now(),
    expires_at    timestamptz NOT NULL DEFAULT (now() + interval '90 days')
);

CREATE INDEX hazard_report_geom_idx ON hazard_report USING GIST (geom);
CREATE INDEX hazard_report_active_idx ON hazard_report (expires_at);

-- +goose Down
DROP TABLE IF EXISTS hazard_report;
DROP TABLE IF EXISTS crash_edge;
DROP TABLE IF EXISTS crash;
