package imap2

import (
	"context"
	"errors"
	"fmt"
	"runtime/trace"

	"github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapserver"
	mess "github.com/foxcpp/go-imap-mess/v2"
	"github.com/foxcpp/maddy-storage/internal/domain/folder"
	"github.com/foxcpp/maddy-storage/internal/usecase"
	"github.com/oklog/ulid/v2"
	"go.uber.org/zap"
)

type session struct {
	b   *Backend
	c   *imapserver.Conn
	sid ulid.ULID

	accountID        ulid.ULID
	selectedFolderID ulid.ULID
	updateHandler    *mess.MailboxHandle[ulid.ULID]
	readOnly         bool

	log           *zap.Logger
	ctx           context.Context
	sessionCancel context.CancelCauseFunc
	sessionTask   *trace.Task
}

func (s *session) Unauthenticate() error {
	if s.selectedFolderID != (ulid.ULID{}) {
		if err := s.Unselect(); err != nil {
			return err
		}
	}
	s.accountID = ulid.ULID{}
	s.log.Debug("unauthenticated")
	return nil
}

func (s *session) Namespace() (*imap.NamespaceData, error) {
	return &imap.NamespaceData{
		Personal: []imap.NamespaceDescriptor{
			{
				Prefix: "",
				Delim:  rune(folder.PathSeparator[0]),
			},
		},
	}, nil
}

func (s *session) Close() error {
	s.sessionTask.End()
	s.sessionCancel(fmt.Errorf("connection closed"))
	s.log.Info("session close")
	if s.updateHandler != nil {
		return s.updateHandler.Close()
	}
	return nil
}

func (s *session) Login(username, password string) error {
	ctx, task := trace.NewTask(s.ctx, "maddy-storage/imap2.Login")
	defer task.End()

	authzULID, err := s.b.accounts.AuthPlain(ctx, username, password)
	if err != nil {
		if errors.Is(err, usecase.ErrInvalidCredentials) {
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
