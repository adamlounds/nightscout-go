-- +goose Up
-- +goose StatementBegin
create table entry (
    id int generated always as identity primary key,
    oid text NOT NULL UNIQUE,
    type text NOT NULL,
    sgv_mgdl int NOT NULL,
    trend text,
    device_id int,
    entry_time timestamptz not null,
    created_time timestamptz not null default now()
);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE entry;
-- +goose StatementEnd
