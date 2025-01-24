package messageusecase

import (
	"github.com/foxcpp/maddy-storage/internal/domain/blob"
	"github.com/foxcpp/maddy-storage/internal/domain/changelog"
	"github.com/foxcpp/maddy-storage/internal/domain/folder"
	"github.com/foxcpp/maddy-storage/internal/domain/message"
	"github.com/foxcpp/maddy-storage/internal/domain/message/searcher"
)

const (
	defaultInlineMaxPartSize   = 8192            // 8 KiB
	defaultMemoryBufferMaxSize = 1 * 1024 * 1024 // 1 MiB
	defaultMaxPartNesting      = 10
)

type Config struct {
	InlineMaxPartSize   int
	MemoryBufferMaxSize int64
	MaxPartNesting      int
}

type Usecase struct {
	cfg        Config
	folderRepo folder.Repo
	imapRepo   folder.IMAPRepo
	msgRepo    message.Repo
	searcher   searcher.Searcher
	blobStore  blob.Store
	tempStore  blob.Store
	changeLog  changelog.Repo
}

func New(
	cfg Config,
	folder folder.Repo,
	imap folder.IMAPRepo,
	msg message.Repo,
	searcher searcher.Searcher,
	blobStore blob.Store,
	tempStore blob.Store,
	changeLog changelog.Repo,
) Usecase {
	uc := Usecase{
		cfg:        cfg,
		folderRepo: folder,
		imapRepo:   imap,
		msgRepo:    msg,
		searcher:   searcher,
		blobStore:  blobStore,
		tempStore:  tempStore,
		changeLog:  changeLog,
	}
	if uc.cfg.InlineMaxPartSize == 0 {
		uc.cfg.InlineMaxPartSize = defaultInlineMaxPartSize
	}
	if uc.cfg.MemoryBufferMaxSize == 0 {
		uc.cfg.MemoryBufferMaxSize = defaultMemoryBufferMaxSize
	}
	if uc.cfg.MaxPartNesting == 0 {
		uc.cfg.MaxPartNesting = defaultMaxPartNesting
	}
	return uc
}

type CreateData struct {
	Folder *folder.Folder
	IMAP   *folder.IMAPFolder
	Entry  *folder.Entry
	Msg    *message.Msg
}
