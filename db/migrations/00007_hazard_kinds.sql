-- +goose Up

-- The set of hazard kinds grew (accident, road_closed, flooding, glass,
-- fallen_tree, signal_out, street_event, slippery_surface, loose_animal).
-- The authoritative list lives in internal/hazard/kinds.go; a test checks that
-- this constraint names exactly the same kinds.
ALTER TABLE hazard_report DROP CONSTRAINT hazard_report_kind_check;
ALTER TABLE hazard_report
    ADD CONSTRAINT hazard_report_kind_check
    CHECK (kind IN (
        'accident','road_closed','construction','flooding','poor_visibility',
        'aggressive_traffic','signal_out','street_event','dangerous_junction',
        'pothole','glass','debris','slippery_surface','blocked_lane',
        'fallen_tree','loose_animal','other'));

-- Lifetimes are now set per kind when a report is created, so the flat
-- 90-day default no longer applies to new rows. It stays as a safety net for
-- any insert that does not name an expiry.
COMMENT ON COLUMN hazard_report.expires_at IS
    'Set from the kind''s TTL on insert; extended by confirmations up to the kind''s max life.';

-- +goose Down
UPDATE hazard_report SET kind = 'other'
 WHERE kind NOT IN ('debris','pothole','blocked_lane','poor_visibility',
                    'aggressive_traffic','construction','dangerous_junction','other');
ALTER TABLE hazard_report DROP CONSTRAINT hazard_report_kind_check;
ALTER TABLE hazard_report
    ADD CONSTRAINT hazard_report_kind_check
    CHECK (kind IN ('debris','pothole','blocked_lane','poor_visibility',
                    'aggressive_traffic','construction','dangerous_junction','other'));
COMMENT ON COLUMN hazard_report.expires_at IS NULL;
