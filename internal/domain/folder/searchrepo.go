package folder

import (
	"context"
	"time"

	"github.com/oklog/ulid/v2"
)

type SearchCond struct {
	FolderIDs []ulid.ULID

	DateSince    time.Time
	DateUntil    time.Time
	CreatedSince time.Time
	CreatedUntil time.Time
	UpdatedSince time.Time
	UpdatedUntil time.Time

	// TODO: Body and header search - probably via FTS.

	SizeSince int64
	SizeUntil int64

	Flag   []string
	NoFlag []string

	Not *SearchCond
	Or  [][]*SearchCond
}

type Searcher interface {
	Search(ctx context.Context, accountID ulid.ULID, cond *SearchCond) ([]Entry, error)
}
