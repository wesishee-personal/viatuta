-- +goose Up

-- One response per rider per hazard: either "I saw it too" (confirm) or "it
-- is gone" (resolve). The primary key is what stops a single account from
-- inflating a hazard by confirming it repeatedly.
--
-- hazard_report.confirmations keeps counting OTHER riders' confirmations; the
-- original reporter is identified by hazard_report.user_id and does not get a
-- vote row for their own report.
CREATE TABLE hazard_vote (
    hazard_id  bigint      NOT NULL REFERENCES hazard_report(id) ON DELETE CASCADE,
    user_id    uuid        NOT NULL REFERENCES user_account(id)  ON DELETE CASCADE,
    vote       text        NOT NULL CHECK (vote IN ('confirm', 'resolve')),
    created_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (hazard_id, user_id)
);

-- When a hazard was marked "no longer there", as distinct from simply
-- expiring. Resolving also sets expires_at to that moment so every existing
-- query that filters on expires_at keeps working unchanged.
ALTER TABLE hazard_report ADD COLUMN resolved_at timestamptz;

-- +goose Down
ALTER TABLE hazard_report DROP COLUMN resolved_at;
DROP TABLE hazard_vote;
