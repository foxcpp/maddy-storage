package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"runtime/debug"

	maddy_storage "github.com/foxcpp/maddy-storage"
	"github.com/foxcpp/maddy-storage/internal/domain/blob"
	storefs "github.com/foxcpp/maddy-storage/internal/domain/blob/store/fs"
	"github.com/foxcpp/maddy-storage/internal/pkg/contextlib"
	"github.com/foxcpp/maddy-storage/internal/pkg/storeerrors"
	storagecli "github.com/foxcpp/maddy-storage/pkg/cli"
	"github.com/urfave/cli/v2"
	"go.uber.org/zap"
)

func storageInit(c *cli.Context) (storagecli.App, error) {
	tempBlobStore := storefs.New(os.TempDir())

	logger := zap.L()
	if c.IsSet("debug") {
		dev, err := zap.NewDevelopment()
		if err != nil {
			return storagecli.App{}, err
		}
		zap.ReplaceGlobals(dev)
		logger = dev
	}

	var blobStore blob.Store
	if c.IsSet("blobfs") {
		blobStore = storefs.New(c.Path("blobfs"))
	} else {
		return storagecli.App{}, cli.Exit("Missing blob path", 2)
	}

	var container *maddy_storage.Container
	if c.IsSet("sqlite") {
		var err error
		container, err = maddy_storage.NewUsingSQLite(
			maddy_storage.DefaultsSQLite,
			c.Path("sqlite"),
			maddy_storage.StubAuth{},
			blobStore,
			tempBlobStore)
		if err != nil {
			logger.Fatal("failed to init db", zap.Error(err))
		}
	} else {
		return storagecli.App{}, cli.Exit("Missing DB path", 2)
	}

	return container.CLI()
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

	logCfg := zap.NewProductionConfig()
	logCfg.Encoding = "console"
	logCfg.Level = zap.NewAtomicLevelAt(zap.InfoLevel)
	log, err := logCfg.Build()
	if err != nil {
		panic(err)
	}
	ctx := contextlib.WithLogger(context.Background(), log)

	if err := app.RunContext(ctx, os.Args); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
}
