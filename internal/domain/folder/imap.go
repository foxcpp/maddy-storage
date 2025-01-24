package folder

import (
	"context"

	"github.com/oklog/ulid/v2"
)

type ModSeq uint64

type IMAPFolder struct {
	ID          ulid.ULID
	UIDNext     uint32
	UIDValidity uint32
}

type IMAPRepo interface {
	CreateIMAPFolders(ctx context.Context, ids ...ulid.ULID) ([]IMAPFolder, error)
	GetIMAPFolder(ctx context.Context, id ulid.ULID) (IMAPFolder, error)
	GetIMAPFolders(ctx context.Context, ids []ulid.ULID) ([]IMAPFolder, error)
	NextUID(ctx context.Context, folderID ulid.ULID, n int) ([]uint32, error)
	LastModSeq(ctx context.Context, accountID ulid.ULID) (ModSeq, error)
	NextModSeq(ctx context.Context, accountID ulid.ULID) (ModSeq, error)
}
