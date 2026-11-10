package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/emersion/go-imap/v2/imapserver"
	maddy_storage "github.com/foxcpp/maddy-storage"
	"github.com/foxcpp/maddy-storage/internal/domain/blob"
	storefs "github.com/foxcpp/maddy-storage/internal/domain/blob/store/fs"
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

	var container *maddy_storage.Container

	tempBlobStore := storefs.New(os.TempDir())
	var blobStore blob.Store
	if *blobFS != "" {
		blobStore = storefs.New(*blobFS)
	} else {
		fmt.Fprint(os.Stderr, "No blob storage provided")
		os.Exit(2)
	}

	if *sqliteDB != "" {
		container, err = maddy_storage.NewUsingSQLite(
			maddy_storage.DefaultsSQLite,
			*sqliteDB,
			maddy_storage.StubAuth{},
			blobStore,
			tempBlobStore)
		if err != nil {
			logger.Fatal("failed to init db", zap.Error(err))
		}
	} else {
		fmt.Fprint(os.Stderr, "No DB storage provided")
		os.Exit(2)
	}

	backend := container.IMAP(logger, imap2.Config{
		ConnLogLevel: zap.DebugLevel,
		IODump:       false,
		TLS:          nil,
		InsecureAuth: true,
	})

	srv := imapserver.New(backend.Options())
	defer srv.Close()

	logger.Info("listening for incoming connections", zap.String("addr", *addr))
	if err := srv.ListenAndServe(*addr); err != nil {
		logger.Fatal("failed to listen", zap.Error(err))
	}
}
