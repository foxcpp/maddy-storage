-- +goose Up
-- +goose StatementBegin
CREATE TABLE accounts (
    id bytea NOT NULL PRIMARY KEY,
    name TEXT NOT NULL UNIQUE,
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,

    UNIQUE(name)
);

CREATE TABLE folders (
    id bytea NOT NULL PRIMARY KEY,
    parent_id bytea DEFAULT NULL
        REFERENCES folders(id)
            ON UPDATE CASCADE ON DELETE NO ACTION
            DEFERRABLE INITIALLY DEFERRED,
    account_id bytea NOT NULL
        REFERENCES accounts(id)
            ON UPDATE CASCADE ON DELETE CASCADE,

    name TEXT NOT NULL DEFAULT 'INBOX',
    path TEXT NOT NULL DEFAULT 'INBOX',

    role TEXT DEFAULT NULL,
    subscribed INTEGER NOT NULL DEFAULT 1,
    sort_order INTEGER NOT NULL DEFAULT 1,

    uid_validity INTEGER NOT NULL DEFAULT (abs(random())),
    uid_next INTEGER NOT NULL DEFAULT 1,

    meta JSONB NOT NULL DEFAULT '{}',
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,

    UNIQUE(account_id, path),
    UNIQUE(parent_id, name),
    UNIQUE(account_id, role),
    CHECK(uid_validity > 0),
    CHECK(uid_next > 0),
    CHECK(path LIKE '%' || name)
);

CREATE TABLE messages (
    id bytea NOT NULL PRIMARY KEY,
    date TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    meta JSONB NOT NULL DEFAULT '{}',
    content JSONB NOT NULL DEFAULT '{}'
);

CREATE TABLE folder_entries (
    folder_id bytea NOT NULL
        REFERENCES folders(id)
            ON UPDATE CASCADE ON DELETE CASCADE,
    message_id bytea NOT NULL
        REFERENCES messages(id)
            ON UPDATE CASCADE ON DELETE CASCADE,
    uid INTEGER NOT NULL DEFAULT 1,

    UNIQUE(folder_id, uid),
    CHECK(uid > 0)
);

CREATE TABLE message_flags (
message_id bytea NOT NULL
    REFERENCES messages(id)
        ON UPDATE CASCADE ON DELETE CASCADE,
    flag TEXT NOT NULL DEFAULT '',

    PRIMARY KEY(message_id, flag)
);

CREATE TABLE message_parts (
    id bytea NOT NULL PRIMARY KEY,
    message_id bytea NOT NULL
        REFERENCES messages(id)
            ON UPDATE CASCADE ON DELETE CASCADE,
    path TEXT NOT NULL DEFAULT '1',
    content JSONB NOT NULL DEFAULT '{}', -- {}
    inline bytea DEFAULT NULL,
    external_blob_id TEXT DEFAULT NULL,

    UNIQUE(message_id, path)
);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

DROP TABLE message_parts;
DROP TABLE message_flags;
DROP TABLE folder_entries;
DROP TABLE messages;

DROP TABLE folders;
DROP TABLE accounts;

-- +goose StatementEnd