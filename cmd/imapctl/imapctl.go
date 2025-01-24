package main

import (
	"errors"
	"fmt"
	"os"
	"runtime/debug"

	"github.com/foxcpp/maddy-storage/internal/domain/account"
	accountsqlite "github.com/foxcpp/maddy-storage/internal/domain/account/repository/sqlite"
	"github.com/foxcpp/maddy-storage/internal/domain/account/usecase"
	"github.com/foxcpp/maddy-storage/internal/domain/blob"
	storefs "github.com/foxcpp/maddy-storage/internal/domain/blob/store/fs"
	"github.com/foxcpp/maddy-storage/internal/domain/changelog"
	"github.com/foxcpp/maddy-storage/internal/domain/changelog/repository/sqlite"
	"github.com/foxcpp/maddy-storage/internal/domain/folder"
	foldersqlite "github.com/foxcpp/maddy-storage/internal/domain/folder/repository/sqlcommon"
	"github.com/foxcpp/maddy-storage/internal/domain/folder/usecase"
	"github.com/foxcpp/maddy-storage/internal/domain/message"
	messagesqlite "github.com/foxcpp/maddy-storage/internal/domain/message/repository/sqlite"
	"github.com/foxcpp/maddy-storage/internal/domain/message/searcher"
	metaonlysqlite "github.com/foxcpp/maddy-storage/internal/domain/message/searcher/metaonly/sqlcommon"
	"github.com/foxcpp/maddy-storage/internal/domain/message/searcher/scan"
	"github.com/foxcpp/maddy-storage/internal/domain/message/usecase"
	"github.com/foxcpp/maddy-storage/internal/pkg/storeerrors"
	"github.com/foxcpp/maddy-storage/internal/repository/sqlite"
	storagecli "github.com/foxcpp/maddy-storage/pkg/cli"
	"github.com/urfave/cli/v2"
	"go.uber.org/zap"
)

func storageInit(c *cli.Context) (storagecli.App, error) {
	var (
		accountsRepo  account.Repo
		folderRepo    folder.Repo
		imapRepo      folder.IMAPRepo
		messageRepo   message.Repo
		changelogRepo changelog.Repo
		blobStore     blob.Store
		tempBlobStore blob.Store
		searcher      searcher.Searcher
	)
	tempBlobStore = storefs.New(os.TempDir())
	if c.IsSet("debug") {
		dev, err := zap.NewDevelopment()
		if err != nil {
			return storagecli.App{}, err
		}
		zap.ReplaceGlobals(dev)
	}
	if c.IsSet("blobfs") {
		blobStore = storefs.New(c.Path("blobfs"))
	}
	if c.IsSet("sqlite") {
		db, err := sqlite.New(c.Path("sqlite"), sqlite.Cfg{})
		if err != nil {
			return storagecli.App{}, cli.Exit("Unable to open SQLite DB: "+err.Error(), 2)
		}

		accountsRepo = accountsqlite.New(db)
		folderRepo = foldersqlite.New(db)
		messageRepo = messagesqlite.New(db)
		changelogRepo = changelogsqlite.New(db)

		searcher = scan.New(scan.Cfg{
			BatchSize: 10,
		}, metaonlysqlite.New(db, metaonlysqlite.Cfg{
			MaxResults: 1000,
		}), messageRepo, blobStore)
	} else {
		return storagecli.App{}, cli.Exit("Missing DB path", 2)
	}

	return storagecli.App{
		Accounts: accountusecase.NewAccount(
			accountusecase.Cfg{},
			accountsRepo,
			folderRepo,
			imapRepo,
			accountusecase.StubAuth{},
			changelogRepo,
		),
		Folders: folderusecase.New(
			folderRepo,
			imapRepo,
			changelogRepo,
			searcher,
		),
		Message: messageusecase.New(
			messageusecase.Config{},
			folderRepo,
			imapRepo,
			messageRepo,
			searcher,
			blobStore,
			tempBlobStore,
			changelogRepo,
		),
	}, nil
}

func main() {
	app := cli.NewApp()
	app.Name = "maddy-storage CLI management utility"
	app.ExitErrHandler = func(cCtx *cli.Context, err error) {
		cli.HandleExitCoder(err)
		if err != nil {
			var internal storeerrors.InternalError
			if errors.As(err, &internal) {
				fmt.Println("Internal error:", err)
			} else {
				fmt.Println(err)
			}
			os.Exit(1)
		}
	}
	app.Flags = []cli.Flag{
		&cli.BoolFlag{
			Name:  "debug",
			Usage: "Enable debug logging",
		},
		&cli.StringFlag{
			Name:      "sqlite",
			TakesFile: true,
			Usage:     "Path to the sqlite3 database file",
		},
		&cli.StringFlag{
			Name:  "blobfs",
			Usage: "Path to the directory to use to store messages",
		},
	}
	app.Authors = []*cli.Author{
		{
			Name:  "Maddy Mail Server maintainers & contributors",
			Email: "~foxcpp/maddy@lists.sr.ht",
		},
	}

	info, ok := debug.ReadBuildInfo()
	if ok {
		app.Version = info.Main.Version
	}

	app.Commands = storagecli.BuildCommands(storageInit)

	app.Run(os.Args)
}
