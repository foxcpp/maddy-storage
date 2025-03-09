package imap2

import (
	"context"
	"errors"
	"fmt"
	"runtime/trace"

	"github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapserver"
	accountusecase "github.com/foxcpp/maddy-storage/internal/domain/account/usecase"
	"github.com/foxcpp/maddy-storage/internal/domain/folder"
	"github.com/foxcpp/maddy-storage/internal/domain/folder/recent"
	"github.com/foxcpp/maddy-storage/internal/pkg/contextlog"
	"github.com/oklog/ulid/v2"
	"go.uber.org/zap"
)

type selectedMbox struct {
	FolderID          ulid.ULID
	At, DeletesAt     folder.ModSeq
	Msgs              uint32
	MaxUID            uint32
	Recents           recent.Set
	ReadOnly          bool
	CondStoreActive   bool
	SavedSearchResult imap.UIDSet

	SkipExpunges        map[uint32]struct{}
	SkipFlagUpdateUntil map[uint32]folder.ModSeq
}

func (m *selectedMbox) isOpen() bool {
	return m.FolderID != ulid.ULID{}
}

var errSeqOutOfRange = &imap.Error{
	Type: imap.StatusResponseTypeNo,
	Code: imap.ResponseCodeCannot,
	Text: "Sequence number out ouf range",
}

func (m *selectedMbox) idsAsRange(set imap.NumSet) (folder.Range, error) {
	res := folder.Range{
		At:        m.At,
		DeletesAt: m.DeletesAt,
	}
	if imap.IsSearchRes(set) {
		set = m.SavedSearchResult
	}
	switch set := set.(type) {
	case imap.SeqSet:
		res.SeqNum = true
		for _, seq := range set {
			if seq.Start == 0 {
				seq.Start = m.Msgs
			}
			if seq.Stop == 0 {
				seq.Stop = m.Msgs
			}
			if seq.Start > m.Msgs || seq.Stop > m.Msgs {
				return folder.Range{}, errSeqOutOfRange
			}

			if seq.Start == seq.Stop {
				res.Values = append(res.Values, seq.Start)
				continue
			}

			if seq.Stop < seq.Start {
				seq.Start, seq.Stop = seq.Stop, seq.Start
			}
			res.Intervals = append(res.Intervals, folder.NumInterval{Since: seq.Start, Until: seq.Stop})
		}
	case imap.UIDSet:
		for _, seq := range set {
			if seq.Start == 0 {
				seq.Start = imap.UID(m.MaxUID)
			}
			if seq.Stop == 0 {
				seq.Stop = imap.UID(m.MaxUID)
			}

			if seq.Start == seq.Stop {
				res.Values = append(res.Values, uint32(seq.Start))
				continue
			}

			if seq.Stop < seq.Start {
				seq.Start, seq.Stop = seq.Stop, seq.Start
			}
			res.Intervals = append(res.Intervals, folder.NumInterval{
				Since: uint32(seq.Start),
				Until: uint32(seq.Stop),
			})
		}
	default:
		panic("unexpected NumSet type")
	}

	return res, nil
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

type session struct {
	b   *Backend
	c   *imapserver.Conn
	sid ulid.ULID

	accountID   ulid.ULID
	mbox        selectedMbox
	enabledCaps imap.CapSet // populated after first select

	log           *zap.Logger
	ctx           context.Context
	sessionCancel context.CancelCauseFunc
	sessionTask   *trace.Task
}

func (s *session) Unauthenticate() error {
	ctx, task := trace.NewTask(s.ctx, "maddy-storage/imap2.Unauthenticate")
	defer task.End()

	if s.mbox.isOpen() {
		if err := s.unselect(ctx); err != nil {
			return err
		}
	}
	s.accountID = ulid.ULID{}
	s.log.Debug("unauthenticated")
	return nil
}

func (s *session) Namespace() (*imap.NamespaceData, error) {
	_, task := trace.NewTask(s.ctx, "maddy-storage/imap2.Namespace")
	defer task.End()

	return &imap.NamespaceData{
		Personal: []imap.NamespaceDescriptor{
			{
				Prefix: "",
				Delim:  rune(folder.PathSeparator[0]),
			},
		},
	}, nil
}

func (s *session) Login(username, password string) error {
	ctx, task := trace.NewTask(s.ctx, "maddy-storage/imap2.Login")
	defer task.End()

	authzULID, err := s.b.accounts.AuthPlain(ctx, username, password)
	if err != nil {
		if errors.Is(err, accountusecase.ErrInvalidCredentials) {
			s.log.Info("invalid credentials", zap.String("username", username))
			return imapserver.ErrAuthFailed
		}
		s.log.Error("authentication error", zap.Error(err))
		return &imap.Error{
			Type: imap.StatusResponseTypeNo,
			Code: imap.ResponseCodeUnavailable,
			Text: "internal server error, sid: " + s.sid.String(),
		}
	}
	s.log.Info("authenticated", zap.String("sasl_username", username), zap.Stringer("account_id", authzULID))
	s.accountID = authzULID
	return nil
}

func (s *session) Close() error {
	s.sessionTask.End()
	s.sessionCancel(fmt.Errorf("connection closed"))
	s.log.Info("session close")
	if s.mbox.isOpen() {
		if err := s.unselect(s.ctx); err != nil {
			s.log.Error("unselect failed", zap.Error(err))
		}
	}
	return nil
}
