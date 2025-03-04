package imap2

import (
	"context"
	"crypto/tls"
	"runtime/trace"

	"github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapserver"
	accountusecase "github.com/foxcpp/maddy-storage/internal/domain/account/usecase"
	"github.com/foxcpp/maddy-storage/internal/domain/folder"
	"github.com/foxcpp/maddy-storage/internal/domain/folder/recent"
	folderusecase "github.com/foxcpp/maddy-storage/internal/domain/folder/usecase"
	messageusecase "github.com/foxcpp/maddy-storage/internal/domain/message/usecase"
	"github.com/foxcpp/maddy-storage/internal/pkg/contextlog"
	"github.com/oklog/ulid/v2"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

type Config struct {
	ConnLogLevel zapcore.Level
	IODump       bool

	TLS          *tls.Config
	InsecureAuth bool
}

type Backend struct {
	cfg Config
	log *zap.Logger

	accounts accountusecase.Account
	folders  folderusecase.Folder
	messages messageusecase.Usecase
	recents  recent.Tracker
	watcher  folder.Watcher
}

func New(
	cfg Config,
	log *zap.Logger,
	accounts accountusecase.Account,
	folders folderusecase.Folder,
	messages messageusecase.Usecase,
	recents recent.Tracker,
	watcher folder.Watcher,
) *Backend {
	return &Backend{
		cfg: cfg,
		log: log,

		accounts: accounts,
		folders:  folders,
		messages: messages,
		recents:  recents,
		watcher:  watcher,
	}
}

func (b *Backend) newSession(c *imapserver.Conn) (imapserver.Session, *imapserver.GreetingData, error) {
	sid := ulid.Make()

	log := b.log.With(
		zap.Stringer("session_id", sid))
	log.Info("session open",
		zap.Stringer("local_addr", c.NetConn().LocalAddr()),
		zap.Stringer("remote_addr", c.NetConn().RemoteAddr()))

	ctx, sessionCancel := context.WithCancelCause(context.Background())
	ctx = contextlog.WithLogger(ctx, log)
	ctx, task := trace.NewTask(ctx, "maddy-storage/imap2.Session")
	trace.Log(ctx, "session_id", sid.String())

	return &session{
			b:             b,
			c:             c,
			sid:           sid,
			log:           log,
			ctx:           ctx,
			sessionCancel: sessionCancel,
			sessionTask:   task,
		}, &imapserver.GreetingData{
			PreAuth: false,
		}, nil
}

func (b *Backend) Options() *imapserver.Options {
	opts := &imapserver.Options{
		NewSession: b.newSession,
		Caps: imap.CapSet{
			imap.CapIMAP4rev1:    {},
			imap.CapIMAP4rev2:    {},
			imap.CapLiteralPlus:  {},
			imap.CapNamespace:    {},
			imap.CapUIDPlus:      {},
			imap.CapESearch:      {},
			imap.CapSearchRes:    {},
			imap.CapListExtended: {},
			imap.CapListStatus:   {},
			imap.CapMove:         {},
			imap.CapStatusSize:   {},
			imap.CapSASLIR:       {},
			imap.CapEnable:       {},
			imap.CapUnselect:     {},
			imap.CapIdle:         {},

			imap.CapCreateSpecialUse: {},
			imap.CapUnauthenticate:   {},
		},
		Logger: IMAPLogger{
			Zap:   b.log,
			Level: b.cfg.ConnLogLevel,
		},
		TLSConfig:    b.cfg.TLS,
		InsecureAuth: b.cfg.InsecureAuth,
	}

	if b.cfg.IODump {
		opts.DebugWriter = IMAPLogger{
			Zap:   b.log,
			Level: b.cfg.ConnLogLevel,
		}
	}

	return opts
}
