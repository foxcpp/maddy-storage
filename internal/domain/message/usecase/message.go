package messageusecase

import (
	"github.com/foxcpp/maddy-storage/internal/domain/blob"
	"github.com/foxcpp/maddy-storage/internal/domain/changelog"
	"github.com/foxcpp/maddy-storage/internal/domain/folder"
	"github.com/foxcpp/maddy-storage/internal/domain/message"
)

const (
	inlineMaxPartSize   = 8192            // 8 KiB
	memoryBufferMaxSize = 1 * 1024 * 1024 // 1 MiB
)

type Config struct {
	InlineMaxPartSize   int
	MemoryBufferMaxSize int64
}

type Usecase struct {
	cfg        Config
	folderRepo folder.Repo
	msgRepo    message.Repo
	blobStore  blob.Store
	tempStore  blob.Store
	changeLog  changelog.Repo
}

func New(
	cfg Config,
	folder folder.Repo,
	msg message.Repo,
	blobStore blob.Store,
	tempStore blob.Store,
	changeLog changelog.Repo,
) Usecase {
	uc := Usecase{
		cfg:        cfg,
		folderRepo: folder,
		msgRepo:    msg,
		blobStore:  blobStore,
		tempStore:  tempStore,
		changeLog:  changeLog,
	}
	if uc.cfg.InlineMaxPartSize == 0 {
		uc.cfg.InlineMaxPartSize = inlineMaxPartSize
	}
	if uc.cfg.MemoryBufferMaxSize == 0 {
		uc.cfg.MemoryBufferMaxSize = memoryBufferMaxSize
	}
	return uc
}

type CreateData struct {
	Folder *folder.Folder
	Entry  *folder.Entry
	Msg    *message.Msg
}
