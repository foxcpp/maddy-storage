package message

import (
	"context"

	"github.com/oklog/ulid/v2"
)

type Filter struct {
	FolderID []ulid.ULID
	// TODO
}

type FoundMsg struct {
	FolderID ulid.ULID
	UID      int64
	Message  *Msg
}

type Searcher interface {
	SearchMessages(ctx context.Context, accountID ulid.ULID, filter *Filter) ([]FoundMsg, error)
}
