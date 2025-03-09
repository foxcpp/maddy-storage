package imap2

import (
	"regexp"
	"runtime/trace"
	"strings"

	"github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapserver"
	"github.com/foxcpp/maddy-storage/internal/domain/folder"
	folderusecase "github.com/foxcpp/maddy-storage/internal/domain/folder/usecase"
	"github.com/foxcpp/maddy-storage/internal/pkg/contextlog"
)

var (
	patternNone     = regexp.MustCompile(``)
	patternAllTree  = regexp.MustCompile(`.*`)
	patternAllLevel = regexp.MustCompile(`[^/]*`) // slash is path separator (folder.PathSeparator)
)

func patternAsRegex(pattern string) *regexp.Regexp {
	switch pattern {
	case "":
		return patternNone
	case "%":
		return patternAllLevel
	case "*":
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
	ctx = contextlog.WithLogger(ctx, s.log)

	if len(patterns) == 0 {
		// Special request to return path separator and root.
		// We don't bother checking roots or anything.
		return w.WriteList(&imap.ListData{
			Attrs:   []imap.MailboxAttr{imap.MailboxAttrNoSelect},
			Delim:   rune(folder.PathSeparator[0]),
			Mailbox: "",
		})
	}

	ref = strings.TrimRight(ref, folder.PathSeparator)

	options.ReturnSpecialUse = true
	options.ReturnChildren = true

	regexpPatterns := make([]*regexp.Regexp, len(patterns))
	for i, p := range patterns {
		if strings.HasPrefix(strings.ToLower(p), "inbox") {
			// Replace any spelling of case-insensitive INBOX folder with
			// canonical one.
			p = folder.FolderINBOX + p[len(folder.FolderINBOX):]
		}
		if ref != "" {
			regexpPatterns[i] = patternAsRegex(ref + folder.PathSeparator + p)
		} else {
			regexpPatterns[i] = patternAsRegex(p)
		}
	}
	opts := &folderusecase.ListOpts{
		Filter: folder.Filter{
			PathRegex: regexpPatterns,
		},
		CheckChildren: true,
		SortAsTree:    true,
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
	if options.ReturnStatus != nil {
		status := options.ReturnStatus
		opts.ReturnIMAPMeta = status.UIDNext || status.UIDValidity
		opts.CountDeleted = status.NumDeleted || status.DeletedStorage
		opts.CountMsgs = status.NumMessages
		opts.ReturnMaxModSeq = status.HighestModSeq
		opts.CountSize = status.Size
		opts.CountUnseen = status.NumUnseen
		opts.ReturnNamespace = status.AppendLimit
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
			Mailbox: f.Folder.Path,
		}

		if options.ReturnSubscribed && f.Folder.Subscribed {
			data.Attrs = append(data.Attrs, imap.MailboxAttrSubscribed)
		}
		if options.ReturnSpecialUse && f.Folder.Role != folder.RoleNone {
			data.Attrs = append(data.Attrs, folderRoleAsSpecial(f.Folder.Role))
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
				data.Status.NumMessages = &f.Msgs
			}
			if options.ReturnStatus.UIDValidity {
				data.Status.UIDValidity = f.IMAP.UIDValidity
			}
			if options.ReturnStatus.UIDNext {
				data.Status.UIDNext = imap.UID(f.IMAP.UIDNext)
			}
			if options.ReturnStatus.NumUnseen {
				data.Status.NumUnseen = &f.UnseenMsgs
			}
			if options.ReturnStatus.NumDeleted {
				data.Status.NumDeleted = &f.DeletedMsgs
			}
			if options.ReturnStatus.Size {
				data.Status.Size = &f.Size
			}
			if options.ReturnStatus.AppendLimit {
				data.Status.AppendLimit = &f.Namespace.AppendLimit
			}
			if options.ReturnStatus.DeletedStorage {
				data.Status.DeletedStorage = &f.DeletedSize
			}
		}
		if options.SelectRecursiveMatch && len(f.MatchingDescendant) > 0 {
			data.ChildInfo = &imap.ListDataChildInfo{}
			for _, f := range f.MatchingDescendant {
				if options.SelectSubscribed && f.Subscribed {
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
