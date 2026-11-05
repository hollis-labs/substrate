-- +goose Up
CREATE TABLE notes (id BIGSERIAL PRIMARY KEY, body TEXT NOT NULL);
-- +goose Down
DROP TABLE notes;
