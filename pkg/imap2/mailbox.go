package imap2

import (
	"regexp"
	"runtime/trace"
	"strings"

	"github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapserver"
	"github.com/foxcpp/maddy-storage/internal/domain/folder"
	"github.com/foxcpp/maddy-storage/internal/pkg/storeerrors"
	"github.com/foxcpp/maddy-storage/internal/usecase"
	"github.com/oklog/ulid/v2"
)

func (s *session) Create(mailbox string, options *imap.CreateOptions) error {
	ctx, task := trace.NewTask(s.ctx, "maddy-storage/imap2.Create")
	defer task.End()

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

	_, err := s.b.folders.Create(ctx, s.accountID, mailbox, role)
	return s.asIMAPError(err)
}

func (s *session) Delete(mailbox string) error {
	ctx, task := trace.NewTask(s.ctx, "maddy-storage/imap2.Delete")
	defer task.End()

	deleted, err := s.b.folders.Delete(ctx, s.accountID, false, mailbox)
	if err != nil {
		return s.asIMAPError(err)
	}

	for _, d := range deleted {
		s.b.updateManager.MailboxDestroyed(d.ID)
	}

	return nil
}

func (s *session) Rename(mailbox, newName string) error {
	ctx, task := trace.NewTask(s.ctx, "maddy-storage/imap2.Rename")
	defer task.End()

	if strings.EqualFold(mailbox, "INBOX") {
		// TODO: Implement "move everything from INBOX" behavior.
		return &imap.Error{
			Type: imap.StatusResponseTypeNo,
			Code: imap.ResponseCodeServerBug,
			Text: "INBOX cannot be renamed",
		}
	}

	_, err := s.b.folders.Rename(ctx, s.accountID, mailbox, newName)
	return s.asIMAPError(err)
}

func (s *session) Subscribe(mailbox string) error {
	ctx, task := trace.NewTask(s.ctx, "maddy-storage/imap2.Subscribe")
	defer task.End()

	err := s.b.folders.Subscribe(ctx, s.accountID, mailbox)
	return s.asIMAPError(err)
}

func (s *session) Unsubscribe(mailbox string) error {
	ctx, task := trace.NewTask(s.ctx, "maddy-storage/imap2.Unsubscribe")
	defer task.End()

	err := s.b.folders.Unsubscribe(ctx, s.accountID, mailbox)
	return s.asIMAPError(err)
}

var (
	patternNone     = regexp.MustCompile(``)
	patternAllTree  = regexp.MustCompile(`.*`)
	patternAllLevel = regexp.MustCompile(`[^/]*`) // slash is path separator (folder.PathSeparator)
)

func patternAsRegex(pattern string) *regexp.Regexp {
	if pattern == "" {
		return patternNone
	}
	if pattern == "%" {
		return patternAllLevel
	}
	if pattern == "*" {
		return patternAllTree
	}

	reStr := strings.Builder{}
	reStr.Grow(len(pattern) + 2)
	reStr.WriteByte('^')
	for _, chr := range []byte(pattern) {
		switch {
		case chr == '%':
			reStr.WriteString(patternAllLevel.String())
		case chr == '*':
			reStr.WriteString(patternAllTree.String())
		case specialInRegexp(chr):
			reStr.WriteByte('\\')
			fallthrough
		default:
			reStr.WriteByte(chr)
		}
	}
	reStr.WriteByte('$')

	return regexp.MustCompile(reStr.String())
}

func folderRoleAsSpecial(r folder.Role) imap.MailboxAttr {
	return imap.MailboxAttr(`\` + string(r))
}

func (s *session) List(w *imapserver.ListWriter, ref string, patterns []string, options *imap.ListOptions) error {
	ctx, task := trace.NewTask(s.ctx, "maddy-storage/imap2.List")
	defer task.End()

	regexpPatterns := make([]*regexp.Regexp, len(patterns))
	for i, p := range patterns {
		if ref != "" {
			regexpPatterns[i] = patternAsRegex(ref + folder.PathSeparator + p)
		} else {
			regexpPatterns[i] = patternAsRegex(p)
		}
	}
	opts := &usecase.ListOpts{
		Filter: folder.Filter{
			PathRegex: regexpPatterns,
		},
		CheckChildren: true,
	}
	if options.SelectRecursiveMatch {
		if options.SelectSubscribed {
			opts.DescendantFilter.Subscribed = &options.SelectSubscribed
		}
		if options.SelectSpecialUse {
			opts.DescendantFilter.HasRole = &options.SelectSpecialUse
		}
	} else {
		if options.SelectSubscribed {
			opts.Filter.Subscribed = &options.SelectSubscribed
		}
		if options.SelectSpecialUse {
			opts.Filter.HasRole = &options.SelectSpecialUse
		}
	}

	options.ReturnSubscribed = options.ReturnSubscribed || options.SelectSubscribed
	options.ReturnSpecialUse = options.ReturnSpecialUse || options.SelectSpecialUse

	folders, err := s.b.folders.List(ctx, s.accountID, opts, folder.OrderByName)
	if err != nil {
		return s.asIMAPError(err)
	}

	for _, f := range folders {
		data := &imap.ListData{
			Delim:   rune(folder.PathSeparator[0]),
			Mailbox: f.Folder.Name_,
		}

		if options.ReturnSubscribed && f.Folder.Subscribed_ {
			data.Attrs = append(data.Attrs, imap.MailboxAttrSubscribed)
		}
		if options.ReturnSpecialUse && f.Folder.Role_ != folder.RoleNone {
			data.Attrs = append(data.Attrs, folderRoleAsSpecial(f.Folder.Role_))
		}
		if options.ReturnChildren {
			if f.HasChildren {
				data.Attrs = append(data.Attrs, imap.MailboxAttrHasChildren)
			} else {
				data.Attrs = append(data.Attrs, imap.MailboxAttrHasNoChildren)
			}
		}
		if options.ReturnStatus != nil {
			data.Status = &imap.StatusData{
				Mailbox: data.Mailbox,
			}
			if options.ReturnStatus.NumMessages {
				msgs := uint32(f.Msgs)
				data.Status.NumMessages = &msgs
			}
			if options.ReturnStatus.UIDValidity {
				data.Status.UIDValidity = f.Folder.UIDValidity_
			}
			if options.ReturnStatus.UIDNext {
				data.Status.UIDNext = imap.UID(f.Folder.UIDNext_)
			}
			if options.ReturnStatus.NumUnseen {
				msgs := uint32(f.UnseenMsgs)
				data.Status.NumUnseen = &msgs
			}
			if options.ReturnStatus.NumDeleted {
				msgs := uint32(f.DeletedMsgs)
				data.Status.NumDeleted = &msgs
			}
			if options.ReturnStatus.Size {
				data.Status.Size = &f.Size
			}
			if options.ReturnStatus.AppendLimit {
				panic("not implemented") // TODO: implement me
			}
			if options.ReturnStatus.DeletedStorage {
				panic("not implemented") // TODO: implement me
			}
		}
		if options.SelectRecursiveMatch && len(f.MatchingDescendant) > 0 {
			data.ChildInfo = &imap.ListDataChildInfo{}
			for _, f := range f.MatchingDescendant {
				if options.SelectSubscribed && f.Subscribed_ {
					data.ChildInfo.Subscribed = true
				}
			}
			if !data.ChildInfo.Subscribed /* all are false * */ {
				data.ChildInfo = nil
				continue // skip - no recursive match
			}
		}

		if err := w.WriteList(data); err != nil {
			return err
		}
	}

	return nil
}

func (s *session) Unselect() error {
	_, task := trace.NewTask(s.ctx, "maddy-storage/imap2.Unselect")
	defer task.End()

	s.selectedFolderID = ulid.ULID{}
	if s.updateHandler != nil {
		s.updateHandler.Close()
		s.updateHandler = nil
	}
	s.readOnly = false

	return nil
}
