-- +goose Up
CREATE TABLE events_b (id BIGSERIAL PRIMARY KEY, kind TEXT NOT NULL);
-- +goose Down
DROP TABLE events_b;
