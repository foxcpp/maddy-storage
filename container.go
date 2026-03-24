package maddy_storage

import (
	"context"
	"time"

	accountsqlite "github.com/foxcpp/maddy-storage/internal/domain/account/repository/sqlite"
	accountusecase "github.com/foxcpp/maddy-storage/internal/domain/account/usecase"
	"github.com/foxcpp/maddy-storage/internal/domain/blob"
	storefs "github.com/foxcpp/maddy-storage/internal/domain/blob/store/fs"
	changelogsqlite "github.com/foxcpp/maddy-storage/internal/domain/changelog/repository/sqlite"
	"github.com/foxcpp/maddy-storage/internal/domain/folder"
	"github.com/foxcpp/maddy-storage/internal/domain/folder/recent"
	recentsqlcommon "github.com/foxcpp/maddy-storage/internal/domain/folder/recent/sqlcommon"
	foldersql "github.com/foxcpp/maddy-storage/internal/domain/folder/repository/sqlcommon"
	foldersqlite "github.com/foxcpp/maddy-storage/internal/domain/folder/repository/sqlite"
	folderusecase "github.com/foxcpp/maddy-storage/internal/domain/folder/usecase"
	messagesqlite "github.com/foxcpp/maddy-storage/internal/domain/message/repository/sqlite"
	"github.com/foxcpp/maddy-storage/internal/domain/message/searcher"
	metaonlysqlite "github.com/foxcpp/maddy-storage/internal/domain/message/searcher/metaonly/sqlcommon"
	"github.com/foxcpp/maddy-storage/internal/domain/message/searcher/scan"
	messageusecase "github.com/foxcpp/maddy-storage/internal/domain/message/usecase"
	"github.com/foxcpp/maddy-storage/internal/repository/sqlite"
	"github.com/foxcpp/maddy-storage/pkg/delivery"
	"github.com/foxcpp/maddy-storage/pkg/imap2"
	"go.uber.org/zap"
)

type SQLiteConfig struct {
	SQLite     sqlite.Cfg
	ScanSearch scan.Cfg
	MetaSearch metaonlysqlite.Cfg
}

var DefaultsSQLite = SQLiteConfig{
	SQLite: sqlite.Cfg{
		SlowLogThreshold: time.Second,
	},
	ScanSearch: scan.Cfg{
		BatchSize: 100,
	},
	MetaSearch: metaonlysqlite.Cfg{
		MaxResults: 100,
	},
}

var ErrBlobNotFound = blob.ErrNotFound

func NewFS(path string) blob.Store {
	return storefs.New(path)
}

type Container struct {
	Searcher searcher.Searcher
	Accounts accountusecase.Account
	Folders  folderusecase.Folder
	Message  messageusecase.Usecase
	Watcher  folder.Watcher
	Recents  recent.Tracker
}

type StubAuth struct{}

func (s StubAuth) Login(ctx context.Context, username, password string) (string, error) {
	return "", accountusecase.ErrInvalidCredentials
}

func NewUsingSQLite(
	cfg SQLiteConfig,
	path string,
	auth accountusecase.Auth,
	blobStore blob.Store,
	tempStore blob.Store,
) (*Container, error) {
	c := &Container{}

	db, err := sqlite.New(path, cfg.SQLite)
	if err != nil {
		return nil, err
	}

	accountsRepo := accountsqlite.New(db)
	folderRepo := foldersql.New(db)
	imapFolderRepo := foldersqlite.New(db)
	messageRepo := messagesqlite.New(db)
	changelogRepo := changelogsqlite.New(db)

	c.Recents = recentsqlcommon.New(db)
	c.Watcher = foldersql.NewWatcher(1*time.Second, db)

	c.Searcher = scan.New(
		cfg.ScanSearch,
		metaonlysqlite.New(db, cfg.MetaSearch),
		messageRepo,
		blobStore,
	)
	c.Accounts = accountusecase.NewAccount(
		accountusecase.Cfg{},
		accountsRepo,
		folderRepo,
		imapFolderRepo,
		auth,
		changelogRepo,
	)
	c.Folders = folderusecase.New(
		folderRepo,
		imapFolderRepo,
		changelogRepo,
		c.Searcher,
	)
	c.Message = messageusecase.New(
		messageusecase.Config{},
		folderRepo,
		imapFolderRepo,
		messageRepo,
		c.Searcher,
		blobStore,
		tempStore,
		changelogRepo,
	)

	return c, nil
}

func (c Container) IMAP(logger *zap.Logger, cfg imap2.Config) *imap2.Backend {
	return imap2.New(
		cfg, logger,
		c.Accounts, c.Folders, c.Message,
		c.Recents, c.Watcher,
	)
}

func (c Container) Delivery(cfg delivery.Config, logger *zap.Logger) *delivery.Container {
	return delivery.NewContainer(
		cfg, logger,
		c.Accounts, c.Folders,
		c.Message,
	)
}
