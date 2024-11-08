-- +goose Up
-- +goose StatementBegin
CREATE TABLE entry
(
    id           INT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    oid          TEXT        NOT NULL UNIQUE,
    type         TEXT        NOT NULL,
    sgv_mgdl     INT         NOT NULL,
    trend        TEXT,
    device_id    INT,
    entry_time   timestamptz NOT NULL,
    created_time timestamptz NOT NULL DEFAULT NOW()
);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE entry;
-- +goose StatementEnd
