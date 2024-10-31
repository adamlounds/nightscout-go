-- +goose Up
-- +goose StatementBegin
create table events (
    id int generated always as identity primary key,
    type text NOT NULL,
    mgdl int NOT NULL,
    trend text,
    device_id int,
    created_at timestamptz not null default now()
);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE events;
-- +goose StatementEnd
