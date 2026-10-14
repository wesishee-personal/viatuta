-- +goose Up

-- The compass heading (degrees clockwise from north) the reporting rider was
-- travelling, as a best guess at which direction the hazard affects. NULL
-- means it affects both directions, which is what a report made from the
-- planning screen records: nobody is travelling anywhere.
--
-- Stored for every kind, but only kinds marked directional in
-- internal/hazard/kinds.go let it steer routing, so changing which kinds are
-- directional needs no data migration.
ALTER TABLE hazard_report
    ADD COLUMN heading_deg smallint CHECK (heading_deg >= 0 AND heading_deg < 360);

-- +goose Down
ALTER TABLE hazard_report DROP COLUMN heading_deg;
