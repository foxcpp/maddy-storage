# AGENTS.md

## Project Overview

SQL-backed IMAP mail storage for [go-imap v2](https://github.com/emersion/go-imap) and Maddy Mail Server. Go 1.23+, SQLite (via GORM), with goose migrations.

## Architecture

Clean/hexagonal architecture with strict layering — **never** import up:

```
cmd/              → Entrypoints (imapd server, imapctl CLI)
pkg/imap2/        → IMAP protocol adapter (go-imap v2 imapserver handlers)
pkg/cli/          → CLI adapter (urfave/cli commands)
internal/domain/  → Business logic (entities, repos, usecases)
internal/repository/ → Shared DB infrastructure (GORM, migrations)
```

### Domain Modules (`internal/domain/`)

Each domain has the same structure: **entity** (`foo.go`), **repo interface** (`repo.go`), **repository/** implementations, **usecase/** orchestration.

- **account** — User accounts (ULID IDs). Creating an account auto-creates INBOX.
- **folder** — Mailbox folders with tree hierarchy via `ParentID`/`Path`. Contains `Entry` (message↔folder link with IMAP UID). Has `IMAPRepo` for UID/ModSeq counters and `Watcher` for polling changes.
- **message** — Messages with recursive MIME `Part` tree. Parts stored inline (≤8KB) or in external `blob.Store`.
- **changelog** — Append-only event log for account/folder/message changes.
- **blob** — `Store` interface for binary part storage (`fs` and `memory` implementations).
- **metadata** — Simple `map[string]string` key-value bag used across entities.

### Key Design Decisions

- **ULIDs everywhere** (`github.com/oklog/ulid/v2`) for all entity IDs — time-sortable, no DB sequences.
- **ModSeq** (`folder.ModSeq` = `uint64`) for IMAP CONDSTORE — tracks change ordering per-account.
- **Folder entries** (`folder.Entry`) are the join between folders and messages, carrying IMAP UID and ModSeq. Messages can exist in multiple folders.
- **Error types** in `internal/pkg/storeerrors/` — use `NotExistsError`, `AlreadyExistsError`, `LogicError`, `ValidationError`. The IMAP layer (`pkg/imap2/error.go`) maps these to IMAP response codes via `errors.As`.
- **Repository interfaces** live in the domain package (e.g. `folder.Repo`), implementations in `repository/` subdirs. `sqlcommon` packages hold shared SQL logic; `sqlite` packages hold SQLite-specific bits.
- **Transactions**: Repos expose `Tx(ctx, readOnly, func(Repo) error)` — always use the `Repo` passed into the callback.

## Build & Test

```sh
# Build
go build ./cmd/imapd
go build ./cmd/imapctl

# Unit tests
go test ./...

# Integration IMAP tests (uses in-memory SQLite by default)
go test ./tests/imap2/...

# imaptest scripted tests (requires external imaptest binary)
go test -tags imaptest ./tests/imap2/ -test.imaptest=/path/to/imaptest
```

CGo vs non-CGo SQLite: both `mattn/go-sqlite3` (CGo) and `modernc.org/sqlite` (pure Go) are supported via build tags — see `open_cgo.go`/`open_non_cgo.go` and `errors_cgo.go`/`errors_non_cgo.go`.

## Conventions

- **Constructor pattern**: `NewFoo(args) (*Foo, error)` validates and returns entity with generated ULID and timestamps.
- **Usecase structs** aggregate repo interfaces + config and are the only layer that orchestrates cross-domain logic (e.g. `messageusecase.Usecase` uses `folder.Repo`, `folder.IMAPRepo`, `message.Repo`, `blob.Store`, `changelog.Repo`).
- **IMAP adapter** (`pkg/imap2/`): `Backend` holds usecases; `session` holds per-connection state (`selectedMbox`). Error translation is centralized in `asIMAPError()`.
- **Test servers** (`tests/imap2/utils/server.go`): `TestServer(t)` wires all repos with in-memory SQLite + memory blob store for integration tests.
- **Searcher** has two strategies: `metaonly` (SQL-based metadata search) and `scan` (reads blob content). `scan.New()` composes both.
- **Migrations**: SQL files in `internal/repository/sqlite/migrations/`, auto-applied via goose on DB open.
- **Folder paths** use `/` separator (`folder.PathSeparator`). INBOX is always uppercased.

