package utils

import (
	"context"
	"flag"
	"math/rand"
	"net"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/emersion/go-imap/v2/imapclient"
	"github.com/emersion/go-imap/v2/imapserver"
	"github.com/foxcpp/maddy-storage/internal/domain/account"
	accountsqlite "github.com/foxcpp/maddy-storage/internal/domain/account/repository/sqlite"
	accountusecase "github.com/foxcpp/maddy-storage/internal/domain/account/usecase"
	storememory "github.com/foxcpp/maddy-storage/internal/domain/blob/store/memory"
	"github.com/foxcpp/maddy-storage/internal/domain/changelog"
	changelogsqlite "github.com/foxcpp/maddy-storage/internal/domain/changelog/repository/sqlite"
	"github.com/foxcpp/maddy-storage/internal/domain/folder"
	"github.com/foxcpp/maddy-storage/internal/domain/folder/recent"
	recentsqlcommon "github.com/foxcpp/maddy-storage/internal/domain/folder/recent/sqlcommon"
	foldersql "github.com/foxcpp/maddy-storage/internal/domain/folder/repository/sqlcommon"
	foldersqlite "github.com/foxcpp/maddy-storage/internal/domain/folder/repository/sqlite"
	folderusecase "github.com/foxcpp/maddy-storage/internal/domain/folder/usecase"
	"github.com/foxcpp/maddy-storage/internal/domain/message"
	messagesqlite "github.com/foxcpp/maddy-storage/internal/domain/message/repository/sqlite"
	"github.com/foxcpp/maddy-storage/internal/domain/message/searcher"
	metaonlysqlite "github.com/foxcpp/maddy-storage/internal/domain/message/searcher/metaonly/sqlcommon"
	"github.com/foxcpp/maddy-storage/internal/domain/message/searcher/scan"
	messageusecase "github.com/foxcpp/maddy-storage/internal/domain/message/usecase"
	"github.com/foxcpp/maddy-storage/internal/repository/sqlite"
	"github.com/foxcpp/maddy-storage/pkg/imap2"
	"go.uber.org/zap/zaptest"
)

var (
	flagDBImpl     = flag.String("test.db", "sqlite", "Database/repository implementation to use")
	flagSqliteFile = flag.Bool("test.sqlite-file", false, "When SQLite is used, use file instead of in-memory DB")
	flagIMAPLog    = flag.Bool("test.imap-log", false, "Write IMAP commands to test log")
)

type testAuth struct {
	Creds map[string]string
}

func (t *testAuth) Login(ctx context.Context, username, password string) (string, error) {
	pass, ok := t.Creds[username]
	if !ok {
		return "", imapserver.ErrAuthFailed
	}
	if pass != password {
		return "", imapserver.ErrAuthFailed
	}
	return username, nil
}

type Server struct {
	T *testing.T

	Auth     *testAuth
	Accounts accountusecase.Account
	Folders  folderusecase.Folder
	Message  messageusecase.Usecase
	Searcher searcher.Searcher
	Backend  *imap2.Backend

	server   *imapserver.Server
	listener net.Listener
	connect  func() net.Conn
}

func (s *Server) Run() {
	if s.listener != nil {
		panic("Server.Run cannot be called twice")
	}
	s.listener, s.connect = TestListener()

	go func() {
		if err := s.server.Serve(s.listener); err != nil {
			s.T.Error("Serve failed:", err)
		}
	}()
}

func (s *Server) RunTCP() (port int) {
	if s.listener != nil {
		panic("Server.Run cannot be called twice")
	}

	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		s.T.Fatal(err)
	}
	port = l.Addr().(*net.TCPAddr).Port

	s.listener = l
	s.connect = func() net.Conn {
		conn, err := net.Dial("tcp", l.Addr().String())
		if err != nil {
			s.T.Fatal(err)
		}
		return conn
	}

	s.T.Log("test server listening on 127.0.0.1:" + strconv.Itoa(port))

	go func() {
		if err := s.server.Serve(s.listener); err != nil {
			s.T.Error("Serve failed:", err)
		}
	}()

	return port
}

func (s *Server) Close() {
	if err := s.server.Close(); err != nil {
		s.T.Fatal(err)
	}
}

func (s *Server) Account() (username, password string) {
	username = "acct" + strconv.Itoa(len(s.Auth.Creds))
	password = "password"

	s.Auth.Creds[username] = password

	acct, err := s.Accounts.Create(context.Background(), username)
	if err != nil {
		s.T.Fatal(err)
	}
	s.T.Log("account", acct.Name, "id", acct.ID)

	return username, password
}

func (s *Server) Conn() *Conn {
	if s.connect == nil {
		panic("Server.Conn called before Server.Run")
	}

	return TestConn(s.T, s.connect())
}

func (s *Server) Client(handler *UnilateralRecorder) *imapclient.Client {
	c := s.Conn()

	cl := imapclient.New(c.Conn, &imapclient.Options{
		DebugWriter:           TestLogWriter(s.T, strconv.Itoa(int(rand.Int31()))),
		UnilateralDataHandler: handler.Handler(),
	})
	if err := cl.WaitGreeting(); err != nil {
		c.Close()
		s.T.Fatal(err)
	}

	return cl
}

func TestServer(t *testing.T) *Server {
	tempBlobStore := storememory.New()
	blobStore := storememory.New()
	logger := zaptest.NewLogger(t)

	var (
		accountsRepo   account.Repo
		folderRepo     folder.Repo
		imapFolderRepo folder.IMAPRepo
		messageRepo    message.Repo
		changelogRepo  changelog.Repo
		recents        recent.Tracker
		watcher        folder.Watcher
		searcher       searcher.Searcher
	)

	t.Log("db implementation:", *flagDBImpl)
	if *flagDBImpl == "sqlite" {
		t.Log("sqlite3 driver:", sqlite.Implementation())

		var (
			db  sqlite.DB
			err error
		)
		if *flagSqliteFile {
			dir := t.TempDir()
			t.Log("temporary directory:", dir)
			db, err = sqlite.New(filepath.Join(dir, "test.db"), sqlite.Cfg{})
		} else {
			db, err = sqlite.NewMemory(sqlite.Cfg{})
		}
		if err != nil {
			t.Fatal(err)
		}

		accountsRepo = accountsqlite.New(db)
		folderRepo = foldersql.New(db)
		imapFolderRepo = foldersqlite.New(db)
		messageRepo = messagesqlite.New(db)
		changelogRepo = changelogsqlite.New(db)

		recents = recentsqlcommon.New(db)
		watcher = foldersql.NewWatcher(1*time.Second, db)

		searcher = scan.New(scan.Cfg{
			BatchSize: 100,
		}, metaonlysqlite.New(db, metaonlysqlite.Cfg{
			MaxResults: 1000,
		}), messageRepo, blobStore)
	} else {
		t.Fatal("Unknown DB implementation:", *flagDBImpl)
	}

	s := &Server{}
	s.T = t

	s.Auth = &testAuth{
		Creds: map[string]string{},
	}

	s.Searcher = searcher

	s.Accounts = accountusecase.NewAccount(
		accountusecase.Cfg{},
		accountsRepo,
		folderRepo,
		imapFolderRepo,
		s.Auth,
		changelogRepo,
	)
	s.Folders = folderusecase.New(
		folderRepo,
		imapFolderRepo,
		changelogRepo,
		s.Searcher,
	)
	s.Message = messageusecase.New(
		messageusecase.Config{},
		folderRepo,
		imapFolderRepo,
		messageRepo,
		s.Searcher,
		blobStore,
		tempBlobStore,
		changelogRepo,
	)

	s.Backend = imap2.New(
		imap2.Config{
			InsecureAuth: true,
			IODump:       *flagIMAPLog,
		}, logger, s.Accounts, s.Folders, s.Message,
		recents, watcher,
	)
	s.server = imapserver.New(s.Backend.Options())

	return s
}
