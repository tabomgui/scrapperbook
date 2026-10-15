-- +goose Up
ALTER TABLE listings ADD COLUMN hidden boolean NOT NULL DEFAULT false;

-- +goose Down
ALTER TABLE listings DROP COLUMN hidden;
