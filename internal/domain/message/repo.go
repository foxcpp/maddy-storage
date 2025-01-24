package message

import (
	"context"

	"github.com/foxcpp/maddy-storage/internal/domain/folder"
	"github.com/foxcpp/maddy-storage/internal/pkg/storeerrors"
	"github.com/oklog/ulid/v2"
)

var ErrNotFound = storeerrors.NotExistsError{Text: "message: no such message"}

type MsgFlags struct {
	ID             ulid.ULID
	Flags          []string
	ModSeq         folder.ModSeq
	PreviousModSeq folder.ModSeq
}

type Repo interface {
	GetByID(ctx context.Context, id ulid.ULID) (*Msg, error)
	GetByIDs(ctx context.Context, modSeqGt folder.ModSeq, ids ...ulid.ULID) ([]Msg, error)
	Create(ctx context.Context, m ...Msg) error
	DeleteByID(ctx context.Context, id ...ulid.ULID) error

	AddFlags(ctx context.Context, ids []ulid.ULID, flags []string, modSeqLe, newModSeq folder.ModSeq) (map[ulid.ULID]MsgFlags, error)
	ReplaceFlags(ctx context.Context, ids []ulid.ULID, flags []string, modSeqLe, newModSeq folder.ModSeq) (map[ulid.ULID]MsgFlags, error)
	DeleteFlags(ctx context.Context, ids []ulid.ULID, flags []string, modSeqLe, newModSeq folder.ModSeq) (map[ulid.ULID]MsgFlags, error)

	TouchMsgs(ctx context.Context, ids []ulid.ULID, newModSeq folder.ModSeq) error

	DeletableExternalIDs(ctx context.Context, ids []string) ([]string, error)
	DanglingMsgIDs(ctx context.Context, limit int) ([]ulid.ULID, error)
}
