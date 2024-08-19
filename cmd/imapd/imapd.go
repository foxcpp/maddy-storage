package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/emersion/go-imap/v2/imapserver"
	"github.com/foxcpp/maddy-storage/internal/domain/account"
	accountsqlite "github.com/foxcpp/maddy-storage/internal/domain/account/repository/sqlite"
	"github.com/foxcpp/maddy-storage/internal/domain/changelog"
	"github.com/foxcpp/maddy-storage/internal/domain/changelog/repository/sqlite"
	"github.com/foxcpp/maddy-storage/internal/domain/folder"
	foldersqlite "github.com/foxcpp/maddy-storage/internal/domain/folder/repository/sqlite"
	"github.com/foxcpp/maddy-storage/internal/domain/message"
	messagesqlite "github.com/foxcpp/maddy-storage/internal/domain/message/repository/sqlite"
	"github.com/foxcpp/maddy-storage/internal/repository/sqlite"
	"github.com/foxcpp/maddy-storage/internal/usecase"
	"github.com/foxcpp/maddy-storage/pkg/imap2"
	"go.uber.org/zap"
)

func main() {
	addr := flag.String("listen", "127.0.0.1:143", "addr:port to listen on")
	sqliteDB := flag.String("sqlite", "", "path to sqlite DB to operate on")
	flag.Parse()

	logger, err := zap.NewDevelopment()
	if err != nil {
		fmt.Println(err)
		os.Exit(2)
	}

	var (
		accountsRepo  account.Repo
		folderRepo    folder.Repo
		messageRepo   message.Repo
		changelogRepo changelog.Repo
	)
	if *sqliteDB != "" {
		db, err := sqlite.New(*sqliteDB, sqlite.Cfg{})
		if err != nil {
			logger.Fatal("failed to init db", zap.Error(err))
		}

		accountsRepo = accountsqlite.New(db)
		folderRepo = foldersqlite.New(db)
		messageRepo = messagesqlite.New(db)
		changelogRepo = changelogsqlite.New(db)
	}

	cfg := imap2.Config{
		ConnLogLevel: zap.DebugLevel,
		IODump:       false,
		TLS:          nil,
		InsecureAuth: true,
	}

	backend := imap2.New(
		cfg, logger,
		usecase.NewAccount(accountsRepo, usecase.StubAuth{}, changelogRepo),
		usecase.NewFolder(folderRepo, changelogRepo),
		usecase.NewMessage(folderRepo, messageRepo, changelogRepo),
	)
	srv := imapserver.New(backend.Options())
	defer srv.Close()

	logger.Info("listening for incoming connections", zap.String("addr", *addr))
	if err := srv.ListenAndServe(*addr); err != nil {
		logger.Fatal("failed to listen", zap.Error(err))
	}

}
