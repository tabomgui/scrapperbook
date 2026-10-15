-- +goose Up
ALTER TABLE jobs
    ADD COLUMN result_new        integer,
    ADD COLUMN result_changed    integer,
    ADD COLUMN result_seen       integer,
    ADD COLUMN result_invalid    integer,
    ADD COLUMN result_irrelevant integer,
    ADD COLUMN result_alerts     integer;

-- +goose Down
ALTER TABLE jobs
    DROP COLUMN result_new,
    DROP COLUMN result_changed,
    DROP COLUMN result_seen,
    DROP COLUMN result_invalid,
    DROP COLUMN result_irrelevant,
    DROP COLUMN result_alerts;
