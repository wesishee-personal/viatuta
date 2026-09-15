-- +goose Up

-- A node is a junction, or an endpoint of a way. Nodes that merely describe
-- the shape of a road (the curve of a bend) are NOT stored here — they are
-- folded into the edge geometry, which keeps the graph small.
-- `id` is assigned by the ingest, not by a sequence. The ingest numbers
-- nodes densely from 0, which lets the Phase 3 router use the id directly as
-- an array index instead of carrying a hash map from id to slot.
CREATE TABLE routing_node (
    id           bigint PRIMARY KEY,
    osm_node_id  bigint NOT NULL,
    geom         geometry(Point, 4326) NOT NULL,

    -- Metres above sea level, filled in by the elevation ingest (Phase 5).
    elevation_m  real,

    -- How traffic is controlled here. Drives the turn-cost model: crossing a
    -- busy road is far safer at a signal than at an uncontrolled junction.
    control      text NOT NULL DEFAULT 'none'
                 CHECK (control IN ('none','signal','stop','yield','crossing','roundabout')),

    created_at   timestamptz NOT NULL DEFAULT now()
);

CREATE UNIQUE INDEX routing_node_osm_id_idx ON routing_node (osm_node_id);
CREATE INDEX routing_node_geom_idx ON routing_node USING GIST (geom);

-- An edge is a DIRECTED road segment between two nodes. A two-way street
-- produces two rows, one per direction.
--
-- Why directed rather than a `oneway` flag? Because safety is not symmetric:
-- a contraflow bike lane, a one-way pair, and the grade of a hill all differ
-- by direction. Storing direction explicitly keeps the router honest.
CREATE TABLE routing_edge (
    -- Dense, ingest-assigned, for the same reason as routing_node.id.
    id          bigint PRIMARY KEY,
    osm_way_id  bigint NOT NULL,

    from_node   bigint NOT NULL REFERENCES routing_node(id) ON DELETE CASCADE,
    to_node     bigint NOT NULL REFERENCES routing_node(id) ON DELETE CASCADE,

    geom        geometry(LineString, 4326) NOT NULL,
    length_m    double precision NOT NULL CHECK (length_m > 0),

    -- ---- Raw OSM-derived attributes ---------------------------------
    highway     text NOT NULL,          -- residential, primary, cycleway, ...
    name        text,

    -- The cycling facility present, best to worst. This is the single
    -- strongest safety signal available from OSM tags.
    infra_class text NOT NULL DEFAULT 'none'
                CHECK (infra_class IN (
                    'protected_track',  -- physically separated from traffic
                    'buffered_lane',    -- painted lane + painted buffer
                    'painted_lane',     -- paint only
                    'shared_lane',      -- sharrow; paint with no space
                    'none',             -- mixed traffic
                    'path',             -- off-street shared-use path
                    'pedestrian'        -- footway where cycling is permitted
                )),

    maxspeed_mph smallint CHECK (maxspeed_mph IS NULL OR maxspeed_mph BETWEEN 0 AND 100),
    lanes        smallint CHECK (lanes IS NULL OR lanes BETWEEN 0 AND 12),
    surface      text,                  -- asphalt, concrete, gravel, dirt, ...
    lit          boolean,               -- NULL = unknown, which is not the same as false

    -- ---- Derived safety attributes ----------------------------------
    -- Signed percent grade IN THE DIRECTION OF TRAVEL: +5.0 means a 5% climb.
    grade_pct    real,

    -- Level of Traffic Stress, 1 (suitable for children) to 4 (fearless
    -- riders only). The Mekuria/Furth framework; see docs/safety-model.md.
    lts          smallint CHECK (lts IS NULL OR lts BETWEEN 1 AND 4),

    -- Normalised 0..1 crash pressure from matched historical collisions.
    crash_score  real NOT NULL DEFAULT 0 CHECK (crash_score >= 0),

    updated_at   timestamptz NOT NULL DEFAULT now()
);

-- The router loads edges ordered by from_node to build its adjacency index,
-- so this index serves the single hottest query in graph building.
CREATE INDEX routing_edge_from_node_idx ON routing_edge (from_node);
CREATE INDEX routing_edge_to_node_idx   ON routing_edge (to_node);
CREATE INDEX routing_edge_geom_idx      ON routing_edge USING GIST (geom);
CREATE INDEX routing_edge_osm_way_idx   ON routing_edge (osm_way_id);

-- +goose Down
DROP TABLE IF EXISTS routing_edge;
DROP TABLE IF EXISTS routing_node;
