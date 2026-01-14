-- +goose Up
-- +goose StatementBegin
CREATE TABLE accounts (
      id BLOB NOT NULL PRIMARY KEY,
      name TEXT NOT NULL UNIQUE,
      created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
      updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
      namespace BLOB NOT NULL DEFAULT x'7b7d', -- {}

      UNIQUE(name)
) WITHOUT ROWID;

CREATE TABLE folders (
     id BLOB NOT NULL PRIMARY KEY,
     parent_id BLOB DEFAULT NULL
         REFERENCES folders(id)
             ON UPDATE CASCADE ON DELETE RESTRICT,
     account_id BLOB NOT NULL
         REFERENCES accounts(id)
             ON UPDATE CASCADE ON DELETE CASCADE,

     name TEXT NOT NULL DEFAULT 'INBOX',
     path TEXT NOT NULL DEFAULT 'INBOX',

     role TEXT DEFAULT NULL,
     subscribed INTEGER NOT NULL DEFAULT 1,
     sort_order INTEGER NOT NULL DEFAULT 1,

     meta BLOB NOT NULL DEFAULT x'7b7d', -- {}
     created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
     updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,

     UNIQUE(account_id, path),
     UNIQUE(parent_id, name),
     UNIQUE(account_id, role),
     CHECK(path LIKE '%/' || name OR path = name)
) WITHOUT ROWID;

CREATE TABLE imap_folders(
    folder_id BLOB NOT NULL PRIMARY KEY
        REFERENCES folders(id)
            ON UPDATE CASCADE ON DELETE CASCADE,
    uid_validity INTEGER NOT NULL DEFAULT (abs(random())),
    uid_next INTEGER NOT NULL DEFAULT 1

    CHECK(uid_next > 0),
    CHECK(uid_validity > 0)
) WITHOUT ROWID;

CREATE TABLE messages (
      id BLOB NOT NULL PRIMARY KEY,
      date TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
      received_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
      total_size INTEGER NOT NULL DEFAULT 0,
      modseq INTEGER NOT NULL,
      created_at_modseq INTEGER NOT NULL,
      created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
      updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
      meta BLOB NOT NULL DEFAULT x'7b7d', -- {}
      content BLOB NOT NULL DEFAULT x'7b7d'
) WITHOUT ROWID;

CREATE TABLE folder_entries (
    folder_id BLOB NOT NULL
        REFERENCES folders(id)
            ON UPDATE CASCADE ON DELETE CASCADE,
    message_id BLOB DEFAULT NULL
        REFERENCES messages(id)
            ON UPDATE CASCADE ON DELETE SET NULL,

    uid INTEGER NOT NULL,
    modseq INTEGER NOT NULL,
    created_at_modseq INTEGER NOT NULL,

    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    deleted_at TIMESTAMP DEFAULT NULL,

    UNIQUE(folder_id, uid),
    CHECK(uid > 0)
);

CREATE TABLE modseq (
    account_id BLOB NOT NULL PRIMARY KEY
        REFERENCES accounts(id)
            ON UPDATE CASCADE ON DELETE CASCADE,
    modseq INTEGER NOT NULL DEFAULT 1
) WITHOUT ROWID;

CREATE TABLE recent_uids (
    folder_id BLOB NOT NULL
        REFERENCES folders(id)
            ON UPDATE CASCADE ON DELETE CASCADE,
    uid INTEGER NOT NULL,
    modseq INTEGER NOT NULL,

    PRIMARY KEY(folder_id, uid)
) WITHOUT ROWID;

CREATE TABLE message_flags (
    message_id BLOB NOT NULL
        REFERENCES messages(id)
            ON UPDATE CASCADE ON DELETE CASCADE,
    flag TEXT NOT NULL DEFAULT '',

    PRIMARY KEY(message_id, flag)
) STRICT, WITHOUT ROWID;

CREATE TABLE message_parts (
    id BLOB NOT NULL PRIMARY KEY,
    message_id BLOB NOT NULL
        REFERENCES messages(id)
            ON UPDATE CASCADE ON DELETE CASCADE,
    order_ INTEGER NOT NULL DEFAULT 0,
    path TEXT NOT NULL DEFAULT '1',
    content BLOB NOT NULL DEFAULT x'7b7d', -- {}
    inline BLOB DEFAULT NULL,
    external_blob_id TEXT DEFAULT NULL,

    UNIQUE(message_id, path)
) WITHOUT ROWID;

CREATE TABLE messages_external_id_counters (
    external_id TEXT NOT NULL PRIMARY KEY,
    copies INTEGER NOT NULL DEFAULT 1,

    CHECK(copies >= 0)
) STRICT, WITHOUT ROWID;
CREATE UNIQUE INDEX messages_external_id_counters_pending_delete
    ON messages_external_id_counters(external_id)
    WHERE copies = 0;

-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER messages_external_id_counters_inc
    BEFORE INSERT ON message_parts
    FOR EACH ROW WHEN NEW.external_blob_id IS NOT NULL
BEGIN
    INSERT INTO messages_external_id_counters(external_id)
    VALUES (NEW.external_blob_id)
        ON CONFLICT (external_id) DO
    UPDATE SET copies = copies + 1;
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER messages_external_id_counters_dec
    BEFORE DELETE ON message_parts
    FOR EACH ROW WHEN OLD.external_blob_id IS NOT NULL
BEGIN
    UPDATE messages_external_id_counters
    SET copies = copies - 1
    WHERE external_id = OLD.external_blob_id;
END;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

DROP TABLE messages_external_id_counters;

DROP TABLE message_parts;
DROP TABLE message_flags;
DROP TABLE folder_entries;
DROP TABLE recent_uids;
DROP TABLE messages;

DROP TABLE folders;
DROP TABLE accounts;

-- +goose StatementEnd
