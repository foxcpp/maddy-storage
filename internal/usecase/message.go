package usecase

import (
	"context"

	"github.com/foxcpp/maddy-storage/internal/domain/changelog"
	"github.com/foxcpp/maddy-storage/internal/domain/folder"
	"github.com/foxcpp/maddy-storage/internal/domain/message"
	"github.com/oklog/ulid/v2"
)

type Message struct {
	folderRepo folder.Repo
	msgRepo    message.Repo
	changeLog  changelog.Repo
}

func NewMessage(folder folder.Repo, msg message.Repo, changeLog changelog.Repo) Message {
	return Message{
		folderRepo: folder,
		msgRepo:    msg,
		changeLog:  changeLog,
	}
}

func (m Message) CopyByUID(ctx context.Context, accountID ulid.ULID, uids []folder.UIDRange, sourceName, targetName string) (*folder.Folder, error) {
	panic("implement me")
}

func (m Message) MoveByUID(ctx context.Context, accountID ulid.ULID, uids []folder.UIDRange, targetName string) (*folder.Folder, error) {
	panic("implement me")
}
