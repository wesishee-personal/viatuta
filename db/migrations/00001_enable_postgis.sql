-- +goose Up
-- PostGIS adds the `geometry` column type, spatial (GIST) indexes, and the
-- ST_* function family. Everything else in this schema depends on it.
CREATE EXTENSION IF NOT EXISTS postgis;

-- +goose Down
-- Intentionally NOT dropping postgis: other databases or schemas may use it,
-- and dropping an extension cascades to every dependent object.
SELECT 1;
