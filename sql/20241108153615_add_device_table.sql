-- +goose Up
-- +goose StatementBegin
CREATE TABLE device
(
    id           INT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    name         TEXT        NOT NULL UNIQUE,
    created_time timestamptz NOT NULL DEFAULT NOW()
);
INSERT INTO device VALUES (DEFAULT, 'unknown', NOW());

ALTER TABLE entry
    ALTER COLUMN device_id SET DEFAULT 1;

UPDATE entry SET device_id = 1;

ALTER TABLE entry
    ALTER COLUMN device_id SET NOT NULL,
    ADD CONSTRAINT fk_entry_device FOREIGN KEY (device_id) REFERENCES device (id);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE entry
    DROP CONSTRAINT fk_entry_device,
    ALTER COLUMN device_id DROP NOT NULL,
    ALTER COLUMN device_id DROP DEFAULT;

DROP TABLE device;
-- +goose StatementEnd
