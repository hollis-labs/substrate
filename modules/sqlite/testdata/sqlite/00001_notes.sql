-- +goose Up
CREATE TABLE notes (id INTEGER PRIMARY KEY, body TEXT NOT NULL);
-- +goose Down
DROP TABLE notes;
