package imap2

import (
	"math"
	"runtime/trace"
	"strings"

	"github.com/emersion/go-imap/v2"
	"github.com/foxcpp/maddy-storage/internal/domain/folder"
	"github.com/foxcpp/maddy-storage/internal/pkg/contextlog"
	"github.com/foxcpp/maddy-storage/internal/pkg/storeerrors"
)

func (s *session) Create(mailbox string, options *imap.CreateOptions) error {
	ctx, task := trace.NewTask(s.ctx, "maddy-storage/imap2.Create")
	defer task.End()
	ctx = contextlog.WithLogger(ctx, s.log)

	mailbox = strings.TrimRight(mailbox, folder.PathSeparator)

	if strings.EqualFold(mailbox, "INBOX") {
		return &imap.Error{
			Type: imap.StatusResponseTypeNo,
			Code: imap.ResponseCodeAlreadyExists,
			Text: "Cannot create INBOX",
		}
	}

	role := folder.RoleNone
	if len(options.SpecialUse) != 0 {
		if len(options.SpecialUse) != 1 {
			return storeerrors.ValidationError{
				Field: "SpecialUse",
				Text:  "only one SPECIAL-USE attribute is supported",
			}
		}
		if options.SpecialUse[0] == "" {
			return storeerrors.ValidationError{
				Text:  "empty SPECIAL-USE attribute not allowed",
				Field: "SpecialUse",
			}
		}
		if options.SpecialUse[0][0] != '\\' {
			return storeerrors.ValidationError{
				Field: "SpecialUse",
				Text:  "SPECIAL-USE attribute must start with backward slash",
			}
		}

		role = folder.Role(options.SpecialUse[0])
		if !role.Valid() {
			return storeerrors.ValidationError{
				Field: "SpecialUse",
				Text:  "unknown SPECIAL-USE attribute or unusable with create",
			}
		}
	}

	_, err := s.b.folders.Create(ctx, s.accountID, mailbox, role, true)
	return s.asIMAPError(err)
}

func (s *session) Delete(mailbox string) error {
	ctx, task := trace.NewTask(s.ctx, "maddy-storage/imap2.Delete")
	defer task.End()
	ctx = contextlog.WithLogger(ctx, s.log)

	if strings.EqualFold(mailbox, "INBOX") {
		return &imap.Error{
			Type: imap.StatusResponseTypeNo,
			Code: imap.ResponseCodeClientBug,
			Text: "Cannot delete INBOX",
		}
	}

	_, err := s.b.folders.Delete(ctx, s.accountID, false, mailbox)
	if err != nil {
		return s.asIMAPError(err)
	}

	return nil
}

func (s *session) Rename(mailbox, newName string) error {
	ctx, task := trace.NewTask(s.ctx, "maddy-storage/imap2.Rename")
	defer task.End()
	ctx = contextlog.WithLogger(ctx, s.log)

	mailbox = strings.TrimRight(mailbox, folder.PathSeparator)
	newName = strings.TrimRight(newName, folder.PathSeparator)

	if strings.EqualFold(mailbox, "INBOX") {
		created, err := s.b.folders.Create(ctx, s.accountID, mailbox, folder.RoleNone, true)
		if err != nil {
			return s.asIMAPError(err)
		}

		_, err = s.b.messages.Move(ctx, s.accountID, folder.Range{
			Intervals: []folder.NumInterval{
				{
					Since: 0,
					Until: math.MaxUint32,
				},
			},
			At:        s.mbox.At,
			DeletesAt: s.mbox.DeletesAt,
		}, s.mbox.FolderID, created.Path, false)
		if err != nil {
			return s.asIMAPError(err)
		}

		return nil
	}

	_, err := s.b.folders.Rename(ctx, s.accountID, mailbox, newName, true)
	return s.asIMAPError(err)
}

func (s *session) Subscribe(mailbox string) error {
	ctx, task := trace.NewTask(s.ctx, "maddy-storage/imap2.Subscribe")
	defer task.End()
	ctx = contextlog.WithLogger(ctx, s.log)

	err := s.b.folders.Subscribe(ctx, s.accountID, mailbox)
	return s.asIMAPError(err)
}

func (s *session) Unsubscribe(mailbox string) error {
	ctx, task := trace.NewTask(s.ctx, "maddy-storage/imap2.Unsubscribe")
	defer task.End()
	ctx = contextlog.WithLogger(ctx, s.log)

	err := s.b.folders.Unsubscribe(ctx, s.accountID, mailbox)
	return s.asIMAPError(err)
}
