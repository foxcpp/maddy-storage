package imap2

import (
	"errors"

	"github.com/emersion/go-imap/v2"
	"github.com/foxcpp/maddy-storage/internal/domain/folder"
	"github.com/foxcpp/maddy-storage/internal/pkg/storeerrors"
	"github.com/oklog/ulid/v2"
	"go.uber.org/zap"
)

func (s *session) asIMAPError(err error) error {
	if err == nil {
		return nil
	}

	var valid storeerrors.ValidationError
	if errors.As(err, &valid) {
		if s.accountID != (ulid.ULID{}) { // log only for authenticated sessions to prevent log spam
			s.log.Info("client error", zap.Error(err))
		}
		text := valid.Text
		if text == "" {
			text = valid.Error()
		}
		return &imap.Error{
			Type: imap.StatusResponseTypeBad,
			Code: imap.ResponseCodeClientBug,
			Text: text,
		}
	}

	var notFound storeerrors.NotExistsError
	if errors.As(err, &notFound) {
		return &imap.Error{
			Type: imap.StatusResponseTypeNo,
			Code: imap.ResponseCodeNonExistent,
			Text: notFound.Text,
		}
	}

	var alreadyExists storeerrors.AlreadyExistsError
	if errors.As(err, &alreadyExists) {
		return &imap.Error{
			Type: imap.StatusResponseTypeNo,
			Code: imap.ResponseCodeAlreadyExists,
			Text: alreadyExists.Text,
		}
	}

	var logic storeerrors.LogicError
	if errors.As(err, &logic) {
		code := imap.ResponseCodeCannot
		if errors.Is(err, folder.ErrHasChildren) {
			code = imap.ResponseCodeHasChildren
		}
		return &imap.Error{
			Type: imap.StatusResponseTypeNo,
			Code: code,
			Text: logic.Text,
		}
	}

	s.log.Error("internal server error", zap.Error(err))
	return &imap.Error{
		Type: imap.StatusResponseTypeNo,
		Code: imap.ResponseCodeServerBug,
		Text: "Internal server error, sid: " + s.sid.String(),
	}
}
