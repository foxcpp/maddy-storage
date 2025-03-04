package main

import (
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/emersion/go-imap/v2/imapserver"
	"github.com/foxcpp/maddy-storage/internal/domain/account"
	accountsql "github.com/foxcpp/maddy-storage/internal/domain/account/repository/sqlite"
	accountusecase "github.com/foxcpp/maddy-storage/internal/domain/account/usecase"
	"github.com/foxcpp/maddy-storage/internal/domain/blob"
	storefs "github.com/foxcpp/maddy-storage/internal/domain/blob/store/fs"
	"github.com/foxcpp/maddy-storage/internal/domain/changelog"
	changelogsqlite "github.com/foxcpp/maddy-storage/internal/domain/changelog/repository/sqlite"
	"github.com/foxcpp/maddy-storage/internal/domain/folder"
	"github.com/foxcpp/maddy-storage/internal/domain/folder/recent"
	recentsql "github.com/foxcpp/maddy-storage/internal/domain/folder/recent/sqlcommon"
	foldersql "github.com/foxcpp/maddy-storage/internal/domain/folder/repository/sqlcommon"
	foldersqlite "github.com/foxcpp/maddy-storage/internal/domain/folder/repository/sqlite"
	folderusecase "github.com/foxcpp/maddy-storage/internal/domain/folder/usecase"
	"github.com/foxcpp/maddy-storage/internal/domain/message"
	messagesqlite "github.com/foxcpp/maddy-storage/internal/domain/message/repository/sqlite"
	"github.com/foxcpp/maddy-storage/internal/domain/message/searcher"
	searchersql "github.com/foxcpp/maddy-storage/internal/domain/message/searcher/metaonly/sqlcommon"
	"github.com/foxcpp/maddy-storage/internal/domain/message/searcher/scan"
	messageusecase "github.com/foxcpp/maddy-storage/internal/domain/message/usecase"
	"github.com/foxcpp/maddy-storage/internal/repository/sqlite"
	"github.com/foxcpp/maddy-storage/pkg/imap2"
	"go.uber.org/zap"
)

func main() {
	addr := flag.String("listen", "127.0.0.1:143", "addr:port to listen on")
	sqliteDB := flag.String("sqlite", "", "path to sqlite DB to operate on")
	blobFS := flag.String("blobfs", "", "path to store message blobs in")
	flag.Parse()

	logger, err := zap.NewDevelopment()
	if err != nil {
		fmt.Println(err)
		os.Exit(2)
	}

	var (
		accountsRepo  account.Repo
		folderRepo    folder.Repo
		imapRepo      folder.IMAPRepo
		messageRepo   message.Repo
		changelogRepo changelog.Repo
		blobStore     blob.Store
		tempBlobStore blob.Store
		searcher      searcher.Searcher
		recents       recent.Tracker
		watcher       folder.Watcher
	)
	tempBlobStore = storefs.New(os.TempDir())
	if *sqliteDB != "" {
		db, err := sqlite.New(*sqliteDB, sqlite.Cfg{})
		if err != nil {
			logger.Fatal("failed to init db", zap.Error(err))
		}

		accountsRepo = accountsql.New(db)
		folderRepo = foldersql.New(db)
		imapRepo = foldersqlite.New(db)
		messageRepo = messagesqlite.New(db)
		changelogRepo = changelogsqlite.New(db)

		searcher = scan.New(scan.Cfg{
			BatchSize: 10,
		}, searchersql.New(db, searchersql.Cfg{
			MaxResults: 1000,
		}), messageRepo, blobStore)

		recents = recentsql.New(db)
		watcher = foldersql.NewWatcher(1*time.Second, db)
	}
	if *blobFS != "" {
		blobStore = storefs.New(*blobFS)
	}

	cfg := imap2.Config{
		ConnLogLevel: zap.DebugLevel,
		IODump:       false,
		TLS:          nil,
		InsecureAuth: true,
	}

	backend := imap2.New(
		cfg, logger,
		accountusecase.NewAccount(
			accountusecase.Cfg{},
			accountsRepo,
			folderRepo,
			imapRepo,
			accountusecase.StubAuth{},
			changelogRepo,
		),
		folderusecase.New(
			folderRepo,
			imapRepo,
			changelogRepo,
			searcher,
		),
		messageusecase.New(
			messageusecase.Config{},
			folderRepo,
			imapRepo,
			messageRepo,
			searcher,
			blobStore,
			tempBlobStore,
			changelogRepo,
		),
		recents, watcher,
	)
	srv := imapserver.New(backend.Options())
	defer srv.Close()

	logger.Info("listening for incoming connections", zap.String("addr", *addr))
	if err := srv.ListenAndServe(*addr); err != nil {
		logger.Fatal("failed to listen", zap.Error(err))
	}
}
