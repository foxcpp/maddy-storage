GO := go
TMPDIR ?= /tmp
BINDIR := bin

.PHONY: imapctl
imapctl:
	@mkdir -p $(BINDIR)
	$(GO) build -o bin/imapctl ./cmd/imapctl

.PHONY: imapd
imapd:
	@mkdir -p $(BINDIR)
	$(GO) build -o bin/imapd ./cmd/imapd

.PHONY: generate-jetdb
generate-jetdb: generate-jetdb-sqlite

.PHONE: generate-jetdb-sqlite
generate-jetdb-sqlite: imapctl
	$(BINDIR)/imapctl --sqlite $(TMPDIR)/maddy-storage-jetdb-gen.db db migrations-up
	$(GO) run github.com/go-jet/jet/v2/cmd/jet -source=sqlite -dsn="$(TMPDIR)/maddy-storage-jetdb-gen.db" -path=internal/repository/sqlite/jetdb
	@rm $(TMPDIR)/maddy-storage-jetdb-gen.db

.PHONY: unittest
unittest:
	$(GO) test ./...

.PHONY: imaptest
imaptest:
	$(GO) test -tags imaptest ./pkg/imap2 

.PHONY: lint
lint:
	golangci-lint run ./...

