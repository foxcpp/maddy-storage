package imap2

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"runtime/trace"
	"strconv"
	"strings"
	"time"

	"github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapserver"
	"github.com/foxcpp/maddy-storage/internal/domain/folder"
	"github.com/foxcpp/maddy-storage/internal/domain/folder/recent"
	"github.com/foxcpp/maddy-storage/internal/domain/message"
	"github.com/foxcpp/maddy-storage/internal/domain/message/searcher"
	messageusecase "github.com/foxcpp/maddy-storage/internal/domain/message/usecase"
	"github.com/foxcpp/maddy-storage/internal/pkg/contextlog"
	"github.com/oklog/ulid/v2"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

func flagsAsStringList(flags []imap.Flag) []string {
	res := make([]string, 0, len(flags))
	for _, flag := range flags {
		res = append(res, string(flag))
	}
	return res
}

func stringListAsFlags(flags []string) []imap.Flag {
	res := make([]imap.Flag, 0, len(flags))
	for _, flag := range flags {
		res = append(res, imap.Flag(flag))
	}
	return res
}

func contentEnvelopeToIMAP(env *message.ContentEnvelope) *imap.Envelope {
	addrSlice := func(addrs []*message.Address) []imap.Address {
		res := make([]imap.Address, 0, len(addrs))
		for _, a := range addrs {
			atSignIndx := strings.LastIndexByte(a.Address, '@')
			mailbox := a.Address // recheck whether this is a meaningful per RFC
			host := ""
			if atSignIndx != -1 {
				mailbox = a.Address[:atSignIndx]
				host = a.Address[atSignIndx+1:]
			}

			res = append(res, imap.Address{
				Name:    a.Name,
				Mailbox: mailbox,
				Host:    host,
			})
		}
		return res
	}

	return &imap.Envelope{
		Date:      env.Date,
		Subject:   env.Subject,
		From:      addrSlice(env.From),
		Sender:    addrSlice(env.Sender),
		ReplyTo:   addrSlice(env.ReplyTo),
		To:        addrSlice(env.To),
		Cc:        addrSlice(env.Cc),
		Bcc:       addrSlice(env.Bcc),
		InReplyTo: env.InReplyTo,
		MessageID: env.MessageID,
	}
}

func partPathFromIMAP(m *message.Msg, imapPath []int, spec imap.PartSpecifier) (message.Path, messageusecase.PartSpecifier) {
	path := message.Path(imapPath)
	part := m.FindPart(path)
	if part == nil {
		if path.LastComponent() != 1 {
			return message.EmptyPath(), messageusecase.PartNone
		}
		parentPart := m.FindPart(path.Parent())
		if parentPart != nil {
			path = parentPart.Path
			part = parentPart
		}
		switch spec {
		case imap.PartSpecifierNone:
			spec = imap.PartSpecifierText
		case imap.PartSpecifierMIME:
			spec = imap.PartSpecifierHeader
		case imap.PartSpecifierHeader, imap.PartSpecifierText:
			return message.EmptyPath(), messageusecase.PartNone
		default:
			panic("unsupported imap part specifier")
		}
	}

	var specifier messageusecase.PartSpecifier
	switch spec {
	case imap.PartSpecifierNone:
		if part.Path.Empty() || part.IsNestedMessage() {
			specifier = messageusecase.PartHeader | messageusecase.PartBody
		} else {
			specifier = messageusecase.PartBody
		}
	case imap.PartSpecifierMIME:
		specifier = messageusecase.PartMIME
	case imap.PartSpecifierHeader:
		specifier = messageusecase.PartHeader
	case imap.PartSpecifierText:
		specifier = messageusecase.PartBody
	}

	return path, specifier
}

func sectionAsPath(m *message.Msg, sect *imap.FetchItemBodySection) (message.Path, messageusecase.WriteOptions) {
	var (
		opts = messageusecase.WriteOptions{}
		path message.Path
	)
	path, opts.Specifier = partPathFromIMAP(m, sect.Part, sect.Specifier)

	if partial := sect.Partial; partial != nil {
		opts.Offset = partial.Offset
		opts.Size = partial.Size
	}
	opts.HeaderFields = sect.HeaderFields
	opts.HeaderFieldsExcept = sect.HeaderFieldsNot

	return path, opts
}

func multiPartToIMAPBodyStruct(partPath message.Path, content *message.ContentPartData, remainingParts []message.Part, extended bool) *imap.BodyStructureMultiPart {
	bodyStruct := &imap.BodyStructureMultiPart{}

	_, contentSubtype, ok := strings.Cut(content.Type, "/")
	if !ok {
		contentSubtype = ""
	}
	bodyStruct.Subtype = contentSubtype

	if extended {
		extStruct := &imap.BodyStructureMultiPartExt{}
		extStruct.Params = content.Params
		if dispos := content.Disposition; dispos != nil {
			extStruct.Disposition = &imap.BodyStructureDisposition{
				Value:  dispos.Value,
				Params: dispos.Params,
			}
		}
		extStruct.Language = content.Language
		extStruct.Location = content.Location
		bodyStruct.Extended = extStruct
	}

	for i, part := range remainingParts {
		if !part.Path.IsChildOf(partPath) {
			continue
		}

		subpart := partToIMAPBodyStruct(part.Path, part.Content, remainingParts[i:], extended, true)
		bodyStruct.Children = append(bodyStruct.Children, subpart)
	}

	return bodyStruct
}

func partToIMAPBodyStruct(partPath message.Path, content *message.ContentPartData, parts []message.Part, extended, isMIMEPart bool) imap.BodyStructure {
	if content.IsMultipart() {
		return multiPartToIMAPBodyStruct(partPath, content, parts, extended)
	}

	bodyStruct := &imap.BodyStructureSinglePart{}

	contentType, contentSubtype, ok := strings.Cut(content.Type, "/")
	if !ok {
		contentSubtype = ""
	}
	bodyStruct.Type = contentType
	bodyStruct.Subtype = contentSubtype
	bodyStruct.ID = content.ID
	bodyStruct.Description = content.Description
	bodyStruct.Encoding = content.Encoding
	bodyStruct.Size = content.Size

	if content.IsText() {
		text := &imap.BodyStructureText{}
		text.NumLines = content.NumLines
		bodyStruct.Text = text
	}

	if content.IsNestedMessage() {
		rfc822 := &imap.BodyStructureMessageRFC822{}
		// If it is a MIME part then content.Nested is populated with necessary info,
		// otherwise there is a single child part immediately after that one
		// that contains the info.
		if isMIMEPart {
			rfc822.Envelope = contentEnvelopeToIMAP(content.Nested.Envelope)
			rfc822.BodyStructure = partToIMAPBodyStruct(partPath, content.Nested, parts, extended, false)
		} else {
			var nestedPart *message.Part
			for _, p := range parts {
				if p.Path.IsChildOf(partPath) {
					nestedPart = &p
					break
				}
			}
			if nestedPart == nil {
				panic("no child part for nested rfc822")
			}

			rfc822.Envelope = contentEnvelopeToIMAP(nestedPart.Content.Envelope)
			rfc822.BodyStructure = partToIMAPBodyStruct(nestedPart.Path, nestedPart.Content, parts, extended, false)
		}
	}

	if extended {
		extStruct := &imap.BodyStructureSinglePartExt{}
		if dispos := content.Disposition; dispos != nil {
			extStruct.Disposition = &imap.BodyStructureDisposition{
				Value:  dispos.Value,
				Params: dispos.Params,
			}
		}
		extStruct.Language = content.Language
		extStruct.Location = content.Location
		bodyStruct.Extended = extStruct
	}

	return bodyStruct
}

var predefinedFlags = []imap.Flag{
	imap.FlagDeleted, imap.FlagFlagged, imap.FlagAnswered,
	imap.FlagDraft, imap.FlagSeen,
}

func (s *session) Select(mailbox string, options *imap.SelectOptions) (*imap.SelectData, error) {
	ctx, task := trace.NewTask(s.ctx, "maddy-storage/imap2.Select")
	defer task.End()

	log := s.log.WithLazy(
		zap.String("imap_command", "SELECT"),
		zap.String("imap_mailbox", mailbox),
		zap.Any("imap_opts", options))
	ctx = contextlog.WithLogger(ctx, log)

	s.enabledCaps = s.c.EnabledCaps()

	if s.mbox.isOpen() {
		if err := s.unselect(ctx); err != nil {
			log.Error("unselect failed", zap.Error(err))
			return nil, s.c.Bye("Unselect failed, connection is in unknown state")
		}
	}

	info, err := s.b.messages.FetchFolderInfo(ctx, s.accountID, mailbox,
		messageusecase.InfoOpts{
			CountMsgs:      true,
			ReturnIMAPMeta: true,
			ReturnMaxUID:   true,
			ReturnModSeq:   true,
			UsedFlags:      true,
		})
	if err != nil {
		return nil, s.asIMAPError(err)
	}
	usedFlagsMap := make(map[string]bool, len(info.UsedFlags))
	for _, val := range info.UsedFlags {
		usedFlagsMap[val] = true
	}
	for _, flag := range predefinedFlags {
		if !usedFlagsMap[string(flag)] {
			info.UsedFlags = append(info.UsedFlags, string(flag))
		}
	}

	var recents recent.Set
	if !s.enabledCaps.Has(imap.CapIMAP4rev2) {
		if options.ReadOnly {
			recents, err = s.b.recents.GetRecents(ctx, info.Folder.ID, info.AcctModSeq)
		} else {
			recents, err = s.b.recents.PopRecents(ctx, info.Folder.ID, info.AcctModSeq)
		}
		if err != nil {
			log.Error("failed to read recents, no flags will be set for this session", zap.Error(err))
		}
	}

	s.mbox = selectedMbox{
		FolderID:          info.Folder.ID,
		At:                info.AcctModSeq,
		DeletesAt:         info.AcctModSeq,
		Msgs:              info.Msgs,
		MaxUID:            info.MaxUID,
		Recents:           recents,
		ReadOnly:          options.ReadOnly,
		CondStoreActive:   options.CondStore,
		SavedSearchResult: imap.UIDSetNum(),

		SkipExpunges:        map[uint32]struct{}{},
		SkipFlagUpdateUntil: map[uint32]folder.ModSeq{},
	}

	log.Info(
		"folder open",
		zap.Stringer("folder_id", s.mbox.FolderID),
		zap.Uint64("modseq", uint64(info.AcctModSeq)),
	)

	// TODO: Use read-only flag for optimizations.

	return &imap.SelectData{
		Flags:          stringListAsFlags(info.UsedFlags),
		PermanentFlags: stringListAsFlags(append(info.UsedFlags, `\*`)),
		NumMessages:    info.Msgs,
		NumRecent:      uint32(recents.Len()),
		UIDNext:        imap.UID(info.IMAP.UIDNext),
		UIDValidity:    info.IMAP.UIDValidity,
		List: &imap.ListData{
			Attrs:   nil,
			Delim:   rune(folder.PathSeparator[0]),
			Mailbox: info.Folder.Path,
		},
	}, nil
}

func (s *session) Status(mailbox string, options *imap.StatusOptions) (*imap.StatusData, error) {
	ctx, task := trace.NewTask(s.ctx, "maddy-storage/imap2.Status")
	defer task.End()

	ctx = contextlog.WithLogger(ctx, s.log.WithLazy(
		zap.String("imap_command", "STATUS"),
		zap.String("imap_mailbox", mailbox),
		zap.Any("imap_opts", options)))

	info, err := s.b.messages.FetchFolderInfo(ctx, s.accountID, mailbox, messageusecase.InfoOpts{
		ReturnNamespace: options.AppendLimit,
		ReturnIMAPMeta:  true,
		CountMsgs:       options.NumMessages,
		CountDeleted:    options.NumDeleted,
		CountUnseen:     options.NumUnseen,
		CountSize:       options.Size,
		UsedFlags:       false,
		ReturnModSeq:    options.HighestModSeq,
	})
	if err != nil {
		return nil, s.asIMAPError(err)
	}

	data := &imap.StatusData{
		Mailbox: info.Folder.Path,
	}

	if options.UIDNext {
		data.UIDNext = imap.UID(info.IMAP.UIDNext)
	}
	if options.UIDValidity {
		data.UIDValidity = info.IMAP.UIDValidity
	}
	if options.AppendLimit {
		data.AppendLimit = &info.Namespace.AppendLimit
	}
	if options.NumMessages {
		data.NumMessages = &info.Msgs
	}
	if options.NumRecent {
		recentCnt, err := s.b.recents.CountRecent(ctx, info.Folder.ID)
		if err != nil {
			return nil, s.asIMAPError(err)
		}
		data.NumRecent = &recentCnt
	}
	if options.NumDeleted {
		data.NumDeleted = &info.DeletedMsgs
	}
	if options.NumUnseen {
		data.NumUnseen = &info.UnseenMsgs
	}
	if options.Size {
		data.Size = &info.Size
	}
	if options.HighestModSeq {
		data.HighestModSeq = uint64(info.MaxModSeq)
	}

	return data, nil
}

func (s *session) Expunge(w *imapserver.ExpungeWriter, uids *imap.UIDSet) error {
	ctx, task := trace.NewTask(s.ctx, "maddy-storage/imap2.Expunge")
	defer task.End()

	if s.mbox.ReadOnly {
		// FIXME: Temporary work-around as go-imap calls Expunge in handleUnselect
		// event for read-only mailboxes.
		return nil
		//return &imap.Error{
		//	Type: imap.StatusResponseTypeNo,
		//	Code: imap.ResponseCodeClientBug,
		//	Text: "Cannot EXPUNGE in a read-only mailbox",
		//}
	}

	log := s.log.WithLazy(
		zap.String("imap_command", "EXPUNGE"),
		zap.Any("imap_uids", uids),
		zap.Stringer("folder_id", s.mbox.FolderID),
	)
	ctx = contextlog.WithLogger(ctx, log)

	var uidsRange folder.Range
	if uids != nil {
		var err error
		uidsRange, err = s.mbox.idsAsRange(uids)
		if err != nil {
			return s.asIMAPError(err)
		}
	}

	uidsRange.At = s.mbox.At
	uidsRange.DeletesAt = s.mbox.DeletesAt
	_, err := s.b.messages.Delete(ctx, s.accountID, s.mbox.FolderID, true, uidsRange, false)
	if err != nil {
		return s.asIMAPError(err)
	}

	// Expunges will be sent by Poll call after Expunge, this might include other messages
	// deleted in other sessions.

	if err := s.b.messages.CleanDeleted(ctx, s.accountID, s.mbox.FolderID); err != nil {
		log.Error("failed to clean dangling messages", zap.Error(err))
	}

	return nil
}

func (s *session) criteriaAsSearcherCond(criteria *imap.SearchCriteria) (cond *searcher.Cond, hasModSeq bool, err error) {
	if criteria == nil {
		return nil, false, nil
	}

	cond = &searcher.Cond{
		SentDateOnly:     true,
		ReceivedDateOnly: true,
	}

	if criteria.SeqNum != nil || criteria.UID != nil {
		cond.NumericIDs = make([]folder.Range, 0, len(criteria.UID)+len(criteria.SeqNum))
		for _, uids := range criteria.UID {
			ids, err := s.mbox.idsAsRange(uids)
			if err != nil {
				return nil, false, err
			}
			cond.NumericIDs = append(cond.NumericIDs, ids)
		}
		for _, seq := range criteria.SeqNum {
			ids, err := s.mbox.idsAsRange(seq)
			if err != nil {
				return nil, false, err
			}
			cond.NumericIDs = append(cond.NumericIDs, ids)
		}
	}
	cond.ReceivedAfter = criteria.Since
	cond.ReceivedBefore = criteria.Before
	cond.SentAfter = criteria.SentSince
	cond.SentBefore = criteria.SentBefore
	if criteria.Header != nil {
		cond.Header = make([]searcher.HeaderField, len(criteria.Header))
		for i, v := range criteria.Header {
			cond.Header[i] = searcher.HeaderField{
				Key:   v.Key,
				Value: v.Value,
			}
		}
	}
	cond.InHeaderBody = criteria.Body
	cond.InBodyOnly = criteria.Text

	if criteria.Flag != nil {
		cond.Flag = make([]string, len(criteria.Flag))
		for i, v := range criteria.Flag {
			cond.Flag[i] = string(v)
		}
	}
	if criteria.NotFlag != nil {
		cond.NoFlag = make([]string, len(criteria.NotFlag))
		for i, v := range criteria.NotFlag {
			cond.NoFlag[i] = string(v)
		}
	}

	cond.SizeGt = uint32(criteria.Larger)
	cond.SizeLt = uint32(criteria.Smaller)

	if cond.Not != nil {
		cond.Not = make([]*searcher.Cond, len(criteria.Not))
		for i, not := range criteria.Not {
			var hasModSeqNot bool
			cond.Not[i], hasModSeqNot, err = s.criteriaAsSearcherCond(&not)
			if err != nil {
				return nil, false, err
			}
			hasModSeq = hasModSeq || hasModSeqNot
		}
	}
	if cond.Or != nil {
		cond.Or = make([][]*searcher.Cond, len(criteria.Or))
		for i, or := range criteria.Or {
			var hasModSeqOr bool

			cond.Or[i][0], hasModSeqOr, err = s.criteriaAsSearcherCond(&or[0])
			if err != nil {
				return nil, false, err
			}
			if hasModSeqOr {
				hasModSeq = true
			}

			cond.Or[i][1], hasModSeqOr, err = s.criteriaAsSearcherCond(&or[1])
			if err != nil {
				return nil, false, err
			}
			if hasModSeqOr {
				hasModSeq = true
			}
		}
	}

	if modSeq := criteria.ModSeq; modSeq != nil {
		cond.ModSeqGt = folder.ModSeq(modSeq.ModSeq)
	}

	return cond, hasModSeq || criteria.ModSeq != nil, nil
}

func (s *session) Search(kind imapserver.NumKind, criteria *imap.SearchCriteria, options *imap.SearchOptions) (*imap.SearchData, error) {
	ctx, task := trace.NewTask(s.ctx, "maddy-storage/imap2.Search")
	defer task.End()

	log := s.log.WithLazy(
		zap.String("imap_command", "SEARCH"),
		zap.Any("imap_criteria", criteria),
		zap.Any("imap_opts", options))
	ctx = contextlog.WithLogger(ctx, log)

	cond, hasModSeq, err := s.criteriaAsSearcherCond(criteria)
	if err != nil {
		return nil, s.asIMAPError(err)
	}
	if hasModSeq && !s.mbox.CondStoreActive {
		s.mbox.CondStoreActive = true
	}

	result, err := s.b.messages.Search(ctx, s.accountID, s.mbox.FolderID, cond, searcher.Opts{
		ReturnAll:     options.ReturnAll || (options.ReturnCount && options.ReturnSave),
		ReturnCount:   options.ReturnCount,
		ReturnMaxUID:  options.ReturnMax,
		ReturnMinUID:  options.ReturnMin,
		ReturnModSeq:  hasModSeq,
		ReturnSeqNums: kind == imapserver.NumKindSeq,
		At:            s.mbox.At,
		DeletesAt:     s.mbox.DeletesAt,
	})
	if err != nil {
		return nil, s.asIMAPError(err)
	}

	data := &imap.SearchData{
		UID: kind == imapserver.NumKindUID,
	}

	if options.ReturnCount {
		data.Count = result.Count
	}
	if options.ReturnMin {
		if kind == imapserver.NumKindSeq {
			data.Min = result.MinSeq
		} else {
			data.Min = result.MinUID
		}
	}
	if options.ReturnMax {
		if kind == imapserver.NumKindSeq {
			data.Max = result.MaxSeq
		} else {
			data.Max = result.MaxUID
		}
	}
	if options.ReturnAll {
		if kind == imapserver.NumKindSeq {
			set := imap.SeqSet{}
			for _, ent := range result.All {
				set.AddNum(ent.SeqNum)
			}
			data.All = set
		} else {
			set := imap.UIDSet{}
			for _, ent := range result.All {
				set.AddNum(imap.UID(ent.UID))
			}
			data.All = set
		}
	}

	if options.ReturnSave {
		if options.ReturnAll {
			s.mbox.SavedSearchResult = data.All.(imap.UIDSet)
		} else if options.ReturnCount {
			set := imap.UIDSet{}
			for _, ent := range result.All {
				set.AddNum(imap.UID(ent.UID))
			}
			s.mbox.SavedSearchResult = set
		} else if options.ReturnMin || options.ReturnMax {
			s.mbox.SavedSearchResult = imap.UIDSet{}
			if options.ReturnMin {
				s.mbox.SavedSearchResult.AddNum(imap.UID(result.MinUID))
			}
			if options.ReturnMax {
				s.mbox.SavedSearchResult.AddNum(imap.UID(result.MaxUID))
			}
		} else {
			s.mbox.SavedSearchResult = imap.UIDSet{}
		}
		log.Debug("saved search result", zap.Stringer("imap_result", s.mbox.SavedSearchResult))
	}

	return data, nil
}

type loggedFetchOptions struct {
	options *imap.FetchOptions
}

func (opts loggedFetchOptions) MarshalLogArray(encoder zapcore.ArrayEncoder) error {
	if opts.options == nil {
		return nil
	}
	if opts.options.BodyStructure != nil {
		if opts.options.BodyStructure.Extended {
			encoder.AppendString("BODYSTRUCTURE")
		} else {
			encoder.AppendString("BODY")
		}
	}
	if opts.options.Envelope {
		encoder.AppendString("ENVELOPE")
	}
	if opts.options.Flags {
		encoder.AppendString("FLAGS")
	}
	if opts.options.InternalDate {
		encoder.AppendString("DATE")
	}
	if opts.options.RFC822Size {
		encoder.AppendString("RFC822.SIZE")
	}
	if opts.options.UID {
		encoder.AppendString("UID")
	}
	if opts.options.ModSeq {
		encoder.AppendString("MODSEQ")
	}
	// TODO: binary and body fields
	if opts.options.ChangedSince != 0 {
		encoder.AppendString("CHANGEDSINCE " + strconv.FormatUint(opts.options.ChangedSince, 10))
	}
	return nil
}

func (s *session) Fetch(w *imapserver.FetchWriter, numSet imap.NumSet, options *imap.FetchOptions) error {
	ctx, task := trace.NewTask(s.ctx, "maddy-storage/imap2.Fetch")
	defer task.End()

	ctx = contextlog.WithLogger(ctx, s.log.WithLazy(
		zap.String("imap_command", "FETCH"),
		zap.Stringer("imap_numset", numSet),
		zap.Array("imap_opts", loggedFetchOptions{options: options})))

	if (options.ChangedSince != 0 || options.ModSeq) && !s.mbox.CondStoreActive {
		s.mbox.CondStoreActive = true
	}

	ids, err := s.mbox.idsAsRange(numSet)
	if err != nil {
		return s.asIMAPError(err)
	}

	var changedSince folder.ModSeq
	if options.ChangedSince != 0 {
		changedSince = folder.ModSeq(options.ChangedSince)
	}

	msgs, err := s.b.messages.Fetch(ctx, s.accountID, s.mbox.FolderID, ids, changedSince, true)
	if err != nil {
		return s.asIMAPError(fmt.Errorf("fetch %v: %w", ids, err))
	}

	for _, msg := range msgs {
		err := s.writeMessage(ctx, w, msg, options)
		if err != nil {
			return s.asIMAPError(err)
		}
	}

	return nil
}

func (s *session) writeMessageBody(ctx context.Context, msgWriter *imapserver.FetchResponseWriter, msg messageusecase.FetchedMessage, sect *imap.FetchItemBodySection) error {
	path, opts := sectionAsPath(&msg.Msg, sect)
	opts.EncodeBinary = true

	// With HEADER.FIELDS, PartSize actually has to read the header
	// to calculate correct size so we skip it. Instead, we write
	// header fields into temporary memory buffer and use its size.
	if len(opts.HeaderFields) != 0 || len(opts.HeaderFieldsExcept) != 0 {
		var headerBuf bytes.Buffer
		err := s.b.messages.WritePart(ctx, &msg.Msg, path, &headerBuf, opts)
		if err != nil {
			return fmt.Errorf("failed to write part %v: %w", path, err)
		}

		wc := msgWriter.WriteBodySection(sect, int64(headerBuf.Len()))
		if _, err := io.Copy(wc, &headerBuf); err != nil {
			if cerr := wc.Close(); cerr != nil {
				return fmt.Errorf("failed to close part %v writer: %w", path, err)
			}
			return fmt.Errorf("failed to write part %v: %w", path, err)
		}
		if cerr := wc.Close(); cerr != nil {
			return fmt.Errorf("failed to close part %v writer: %w", path, err)
		}

		return nil
	}

	size, err := s.b.messages.PartSize(ctx, &msg.Msg, path, opts)
	if err != nil {
		return fmt.Errorf("failed to calculate part %v size: %w", path, err)
	}
	wc := msgWriter.WriteBodySection(sect, int64(size))
	err = s.b.messages.WritePart(ctx, &msg.Msg, path, wc, opts)
	if err != nil {
		if cerr := wc.Close(); cerr != nil {
			return fmt.Errorf("failed to close part %v writer: %w", path, err)
		}
		return fmt.Errorf("failed to write part %v: %w", path, err)
	}
	if cerr := wc.Close(); cerr != nil {
		return fmt.Errorf("failed to close part %v writer: %w", path, err)
	}

	return nil
}

func (s *session) writeMessage(ctx context.Context, w *imapserver.FetchWriter, msg messageusecase.FetchedMessage, options *imap.FetchOptions) error {
	msgWriter := w.CreateMessage(msg.Entry.SeqNum)
	defer msgWriter.Close()
	if options.BodyStructure != nil {
		extended := options.BodyStructure.Extended
		msgWriter.WriteBodyStructure(partToIMAPBodyStruct(
			message.EmptyPath(), msg.Msg.Parts[0].Content,
			msg.Msg.Parts[1:], extended, false,
		))
	}
	if options.Envelope {
		msgWriter.WriteEnvelope(contentEnvelopeToIMAP(msg.Msg.Parts[0].Content.Envelope))
	}
	if options.Flags {
		if s.mbox.Recents.IsRecent(imap.UID(msg.Entry.IMAPUID)) {
			msg.Msg.Flags = append(msg.Msg.Flags, `\Recent`)
		}
		msgWriter.WriteFlags(stringListAsFlags(msg.Msg.Flags))
	}
	if options.InternalDate {
		msgWriter.WriteInternalDate(msg.Msg.ReceivedAt)
	}
	if options.RFC822Size {
		msgWriter.WriteRFC822Size(int64(msg.Msg.TotalSize))
	}
	if options.UID {
		msgWriter.WriteUID(imap.UID(msg.Entry.IMAPUID))
	}
	if options.ModSeq {
		// TODO: Missing MODSEQ in go-imap
		panic("implement me")
	}
	for _, sect := range options.BodySection {
		if err := s.writeMessageBody(ctx, msgWriter, msg, sect); err != nil {
			return fmt.Errorf("failed to write body section %v: %w", sect, err)
		}
	}
	//for _, sect := range options.BinarySection {
	//	// TODO
	//}
	//for _, sect := range options.BinarySectionSize {
	//	// TODO
	//}
	if err := msgWriter.Close(); err != nil {
		return fmt.Errorf("failed to close message writer: %w", err)
	}
	return nil
}

func (s *session) Store(w *imapserver.FetchWriter, numSet imap.NumSet, flags *imap.StoreFlags, options *imap.StoreOptions) error {
	ctx, task := trace.NewTask(s.ctx, "maddy-storage/imap2.Store")
	defer task.End()

	if s.mbox.ReadOnly {
		return &imap.Error{
			Type: imap.StatusResponseTypeNo,
			Code: imap.ResponseCodeClientBug,
			Text: "Cannot STORE in a read-only mailbox",
		}
	}

	ctx = contextlog.WithLogger(ctx, s.log.WithLazy(
		zap.String("imap_command", "STORE"),
		zap.Stringer("imap_numset", numSet),
		zap.Stringer("folder_id", s.mbox.FolderID)))

	if options.UnchangedSince != 0 && !s.mbox.CondStoreActive {
		s.mbox.CondStoreActive = true
	}

	numIDs, err := s.mbox.idsAsRange(numSet)
	if err != nil {
		return s.asIMAPError(err)
	}
	var modSeqLe folder.ModSeq
	if options.UnchangedSince != 0 {
		modSeqLe = folder.ModSeq(options.UnchangedSince)
	}

	var (
		upds []messageusecase.UpdatedMessage
	)
	switch flags.Op {
	case imap.StoreFlagsDel:
		upds, err = s.b.messages.DeleteFlags(ctx, s.accountID, s.mbox.FolderID, numIDs,
			flagsAsStringList(flags.Flags), modSeqLe, true)
	case imap.StoreFlagsAdd:
		upds, err = s.b.messages.AddFlags(ctx, s.accountID, s.mbox.FolderID, numIDs,
			flagsAsStringList(flags.Flags), modSeqLe, true)
	case imap.StoreFlagsSet:
		upds, err = s.b.messages.SetFlags(ctx, s.accountID, s.mbox.FolderID, numIDs,
			flagsAsStringList(flags.Flags), modSeqLe, true)
	default:
		panic("unknown STORE operation")
	}
	if err != nil {
		return s.asIMAPError(err)
	}

	for _, upd := range upds {
		needUpdate := false
		if upd.PreviousAt > s.mbox.At {
			// Message changed between last synchronization
			// produce update anyway.
			needUpdate = true
		} else {
			needUpdate = !flags.Silent
		}

		if !needUpdate {
			continue
		}

		// Prevent Poll, Idle from duplicating our update
		// TODO: Consider just always sending UID and relying on
		// Poll to generate updates.
		s.mbox.SkipFlagUpdateUntil[upd.UID] = upd.At

		msgW := w.CreateMessage(upd.Seq)
		if !numIDs.SeqNum {
			msgW.WriteUID(imap.UID(upd.UID))
		}
		msgW.WriteFlags(stringListAsFlags(upd.Flags))
		// TODO: Write ModSeq
		if err := msgW.Close(); err != nil {
			return s.asIMAPError(err)
		}
	}

	return nil
}

func (s *session) Move(w *imapserver.MoveWriter, numSet imap.NumSet, dest string) error {
	ctx, task := trace.NewTask(s.ctx, "maddy-storage/imap2.Move")
	defer task.End()

	if s.mbox.ReadOnly {
		return &imap.Error{
			Type: imap.StatusResponseTypeNo,
			Code: imap.ResponseCodeClientBug,
			Text: "Cannot MOVE in a read-only mailbox",
		}
	}

	log := s.log.WithLazy(
		zap.String("imap_command", "MOVE"),
		zap.Stringer("imap_numset", numSet),
	)
	ctx = contextlog.WithLogger(ctx, log)

	ids, err := s.mbox.idsAsRange(numSet)
	if err != nil {
		return s.asIMAPError(err)
	}

	result, err := s.b.messages.Move(
		ctx, s.accountID, ids,
		s.mbox.FolderID, dest,
		true,
	)
	if err != nil {
		return s.asIMAPError(err)
	}

	if err := s.b.recents.AddRecentEntries(ctx, result.TargetEntries); err != nil {
		log.Error("failed to add recent entries", zap.Error(err))
	}

	sourceUIDs := imap.UIDSet{}
	for _, ent := range result.SourceEntries {
		sourceUIDs.AddNum(imap.UID(ent.IMAPUID))
	}
	targetUIDs := imap.UIDSet{}
	for _, ent := range result.TargetEntries {
		targetUIDs.AddNum(imap.UID(ent.IMAPUID))
	}

	// Expunges will be synchronized by Poll call after Move.

	err = w.WriteCopyData(&imap.CopyData{
		UIDValidity: result.TargetIMAP.UIDValidity,
		SourceUIDs:  sourceUIDs,
		DestUIDs:    targetUIDs,
	})
	if err != nil {
		return s.asIMAPError(err)
	}

	return nil
}

func (s *session) Copy(numSet imap.NumSet, dest string) (*imap.CopyData, error) {
	ctx, task := trace.NewTask(s.ctx, "maddy-storage/imap2.Move")
	defer task.End()

	log := s.log.WithLazy(
		zap.String("imap_command", "COPY"),
		zap.Stringer("imap_numset", numSet),
	)
	ctx = contextlog.WithLogger(ctx, log)

	ids, err := s.mbox.idsAsRange(numSet)
	if err != nil {
		return nil, s.asIMAPError(err)
	}

	result, err := s.b.messages.Copy(ctx, s.accountID, ids, s.mbox.FolderID, dest)
	if err != nil {
		return nil, s.asIMAPError(err)
	}

	if err := s.b.recents.AddRecentEntries(ctx, result.TargetEntries); err != nil {
		log.Error("failed to add recent entries", zap.Error(err))
	}

	sourceUIDs := imap.UIDSet{}
	for _, ent := range result.SourceEntries {
		sourceUIDs.AddNum(imap.UID(ent.IMAPUID))
	}
	targetUIDs := imap.UIDSet{}
	for _, ent := range result.TargetEntries {
		targetUIDs.AddNum(imap.UID(ent.IMAPUID))
	}

	return &imap.CopyData{
		UIDValidity: result.TargetIMAP.UIDValidity,
		SourceUIDs:  sourceUIDs,
		DestUIDs:    targetUIDs,
	}, nil
}

func (s *session) Append(mailbox string, r imap.LiteralReader, options *imap.AppendOptions) (*imap.AppendData, error) {
	ctx, task := trace.NewTask(s.ctx, "maddy-storage/imap2.Append")
	defer task.End()

	if s.mbox.ReadOnly {
		return nil, &imap.Error{
			Type: imap.StatusResponseTypeNo,
			Code: imap.ResponseCodeClientBug,
			Text: "Cannot APPEND in a read-only mailbox",
		}
	}

	log := s.log.WithLazy(
		zap.String("imap_command", "APPEND"),
		zap.String("imap_mailbox", mailbox),
		zap.Int64("imap_size", r.Size()))
	ctx = contextlog.WithLogger(ctx, log)

	flags := make([]string, len(options.Flags))
	for i, flag := range options.Flags {
		flags[i] = string(flag)
	}

	createdData, err := s.b.messages.CreateMessage(
		ctx, s.accountID, mailbox,
		options.Time, flags,
		r.Size(), r)
	if err != nil {
		return nil, s.asIMAPError(err)
	}

	if err := s.b.recents.AddRecent(
		ctx,
		createdData.Folder.ID,
		imap.UID(createdData.Entry.IMAPUID),
		createdData.Entry.CreatedAtModSeq,
	); err != nil {
		log.Error("failed to add recent entries", zap.Error(err))
	}

	return &imap.AppendData{
		UID:         imap.UID(createdData.Entry.IMAPUID),
		UIDValidity: createdData.IMAP.UIDValidity,
	}, nil
}

var errIdleStopped = errors.New("idle stopped")

type ExpungeWriter interface {
	WriteExpunge(seqNum uint32) error
}

func (s *session) applyExpungeUpdates(ctx context.Context, w ExpungeWriter, entries []folder.EntryChange) error {
	if len(entries) == 0 {
		return nil
	}

	log := contextlog.FromContext(ctx)

	needMaxUIDUpd := false
	newAt := s.mbox.DeletesAt

	for i := len(entries) - 1; i >= 0; i-- {
		ent := entries[i].Deleted
		if ent == nil {
			continue
		}

		if newAt < ent.ModSeq {
			newAt = ent.ModSeq
		}

		if _, ok := s.mbox.SkipExpunges[ent.IMAPUID]; ok {
			delete(s.mbox.SkipExpunges, ent.IMAPUID)
			continue
		}

		log.Debug("sending expunge",
			zap.Uint32("uid", ent.IMAPUID),
			zap.Uint32("seqnum", ent.SeqNum),
			zap.Uint64("modseq", uint64(ent.ModSeq)),
		)

		if ent.SeqNum == 0 {
			panic("missing SeqNum for IMAPUID " + strconv.Itoa(int(ent.IMAPUID)))
		}
		if s.mbox.Msgs == 0 {
			panic("sending expunge for empty mailbox view")
		}
		if err := w.WriteExpunge(ent.SeqNum); err != nil {
			return fmt.Errorf("failed to write expunge: %w", err)
		}

		if ent.IMAPUID == s.mbox.MaxUID {
			needMaxUIDUpd = true
		}
		s.mbox.Msgs--
	}

	if needMaxUIDUpd {
		res, err := s.b.messages.Search(ctx, s.accountID, s.mbox.FolderID, nil, searcher.Opts{
			ReturnMaxUID: true,
			At:           s.mbox.At,
			DeletesAt:    s.mbox.DeletesAt,
		})
		if err != nil {
			return fmt.Errorf("search max uid: %w", err)
		}
		s.mbox.MaxUID = res.MaxUID
	}

	s.mbox.DeletesAt = newAt

	return nil
}

func (s *session) applyOtherUpdates(ctx context.Context, w *imapserver.UpdateWriter, entries []folder.EntryChange) error {
	if len(entries) == 0 {
		return nil
	}

	log := contextlog.FromContext(ctx)

	newAt := s.mbox.At
	newMaxUID := s.mbox.MaxUID

	hasNewMessages := false
	for i := len(entries) - 1; i >= 0; i-- {
		ent := entries[i]
		if ent.New == nil {
			continue
		}

		if newAt < ent.At {
			newAt = ent.At
		}
		hasNewMessages = true

		log.Debug("new message in mailbox view",
			zap.Uint32("uid", ent.New.IMAPUID),
			zap.Uint32("seqnum", ent.New.SeqNum),
			zap.Uint64("modseq", uint64(ent.New.ModSeq)),
		)
		if ent.New.IMAPUID > newMaxUID {
			newMaxUID = ent.New.IMAPUID
		}
		s.mbox.Msgs++
	}

	if hasNewMessages {
		log.Debug("sending new messages count", zap.Uint32("msgs_count", s.mbox.Msgs))
		if err := w.WriteNumMessages(s.mbox.Msgs); err != nil {
			return fmt.Errorf("failed to write num messages: %w", err)
		}
	}

	fetchFlagsID := make([]uint32, 0, len(entries))
	for i := len(entries) - 1; i >= 0; i-- {
		ent := entries[i]
		if ent.Updated == nil {
			continue
		}

		if newAt < ent.At {
			newAt = ent.At
		}
		if s.mbox.SkipFlagUpdateUntil[ent.Updated.IMAPUID] >=
			ent.At {
			log.Debug("skipped flag update",
				zap.Stringer("msg_id", ent.Updated.MsgID),
				zap.Uint64("modseq", uint64(ent.Updated.ModSeq)))
			continue
		}
		fetchFlagsID = append(fetchFlagsID, ent.Updated.IMAPUID)
	}
	if len(fetchFlagsID) != 0 {
		fetched, err := s.b.messages.Fetch(
			ctx, s.accountID, s.mbox.FolderID,
			folder.Range{
				Values:    fetchFlagsID,
				At:        s.mbox.At,
				DeletesAt: s.mbox.DeletesAt,
			}, 0,
			true)
		if err != nil {
			return fmt.Errorf("failed to fetch changes: %w", err)
		}
		for _, msg := range fetched {
			err := w.WriteMessageFlags(msg.Entry.SeqNum, imap.UID(msg.Entry.IMAPUID), stringListAsFlags(msg.Msg.Flags))
			if err != nil {
				return fmt.Errorf("failed to write flags: %w", err)
			}
		}
	}

	s.mbox.MaxUID = newMaxUID
	s.mbox.At = newAt
	s.mbox.SkipFlagUpdateUntil = map[uint32]folder.ModSeq{}

	return nil
}

func (s *session) updateRecents(ctx context.Context, w *imapserver.UpdateWriter) error {
	if s.enabledCaps.Has(imap.CapIMAP4rev2) {
		return nil
	}
	var (
		newRecents recent.Set
		err        error
	)

	log := contextlog.FromContext(ctx)

	if s.mbox.ReadOnly {
		newRecents, err = s.b.recents.GetRecents(ctx, s.mbox.FolderID, s.mbox.At)
	} else {
		newRecents, err = s.b.recents.PopRecents(ctx, s.mbox.FolderID, s.mbox.At)
	}
	if err != nil {
		log.Error("failed to fetch new recents", zap.Error(err))
	} else if newRecents.Len() > 0 {
		log.Debug("added new recent entries", zap.Int("count", newRecents.Len()))
		s.mbox.Recents.MergeWith(&newRecents)
		if err := w.WriteNumRecent(uint32(s.mbox.Recents.Len())); err != nil {
			return err
		}
	}

	return nil
}

func (s *session) Poll(w *imapserver.UpdateWriter, allowExpunge bool) error {
	if !s.mbox.isOpen() {
		return nil
	}

	ctx, task := trace.NewTask(s.ctx, "maddy-storage/imap2.Poll")
	defer task.End()

	log := contextlog.FromContext(ctx).WithLazy(
		zap.Stringer("imap_selected_id", s.mbox.FolderID),
		zap.Uint32("msgs_count", s.mbox.Msgs),
	)
	initialAt, initialDeletesAt := s.mbox.At, s.mbox.DeletesAt
	ctx = contextlog.WithLogger(ctx, log)

	changeMask := folder.ChangeNewMessage | folder.ChangeMessageUpdated
	if allowExpunge {
		changeMask |= folder.ChangeMessageDeleted
	}
	entries, err := s.b.watcher.Sync(
		ctx, []ulid.ULID{s.mbox.FolderID},
		s.mbox.At, s.mbox.DeletesAt, changeMask,
	)
	if err != nil {
		log.Error("watcher error, ignoring updates", zap.Error(err))
		return nil
	}

	if allowExpunge {
		if err := s.applyExpungeUpdates(ctx, w, entries); err != nil {
			if errors.Is(err, context.Canceled) {
				return nil
			}

			log.Error("error in applyExpungeUpdates", zap.Error(err))
			return s.c.Bye("Poll failed, terminating connection to prevent corruption")
		}
	}

	if err := s.applyOtherUpdates(ctx, w, entries); err != nil {
		if errors.Is(err, context.Canceled) {
			return nil
		}

		log.Error("error in applyOtherUpdates", zap.Error(err))
		return s.c.Bye("Poll failed, terminating connection to prevent corruption")
	}

	if err := s.updateRecents(ctx, w); err != nil {
		log.Error("error in updateRecents", zap.Error(err))
		return s.c.Bye("Poll failed, terminating connection to prevent corruption")
	}

	// DeletesAt may lag behind if some Poll's are without expunges
	// but the reverse is not true - we always see updates/new messages.
	s.mbox.At = max(s.mbox.At, s.mbox.DeletesAt)
	log.Debug("synchronized",
		zap.Uint64("initial_modseq", uint64(initialAt)),
		zap.Uint64("initial_deletes_modseq", uint64(initialDeletesAt)),
		zap.Uint64("new_modseq", uint64(s.mbox.At)),
		zap.Uint64("new_deletes_modseq", uint64(s.mbox.DeletesAt)))

	return nil
}

func (s *session) Idle(w *imapserver.UpdateWriter, stop <-chan struct{}) error {
	ctx, task := trace.NewTask(s.ctx, "maddy-storage/imap2.Idle")
	defer task.End()

	ctx, cancel := context.WithCancelCause(ctx)
	defer cancel(nil)

	log := contextlog.FromContext(ctx).WithLazy(
		zap.String("imap_command", "IDLE"),
		zap.Stringer("imap_selected_id", s.mbox.FolderID),
	)
	initialAt, initialDeletesAt := s.mbox.At, s.mbox.DeletesAt
	ctx = contextlog.WithLogger(ctx, log)

	go func() {
		<-stop
		cancel(errIdleStopped)
	}()

	for {
		entries, err := s.b.watcher.Wait(
			ctx, []ulid.ULID{s.mbox.FolderID},
			s.mbox.At, s.mbox.DeletesAt,
			folder.ChangeAllMessage,
		)
		if err != nil {
			log.Error("watcher error, ignoring updates", zap.Error(err))
			time.Sleep(5 * time.Second)
			continue
		}

		if err := s.applyExpungeUpdates(ctx, w, entries); err != nil {
			if errors.Is(err, context.Canceled) {
				return nil
			}

			log.Error("error in applyExpungeUpdates", zap.Error(err))
			return s.c.Bye("IDLE failed, terminating connection to prevent corruption")
		}

		if err := s.applyOtherUpdates(ctx, w, entries); err != nil {
			if errors.Is(err, context.Canceled) {
				return nil
			}

			log.Error("error in applyOtherUpdates", zap.Error(err))
			return s.c.Bye("IDLE failed, terminating connection to prevent corruption")
		}

		if err := s.updateRecents(ctx, nil); err != nil {
			log.Error("error in updateRecents", zap.Error(err))
			return s.c.Bye("IDLE failed, terminating connection to prevent corruption")
		}

		// DeletesAt may lag behind if some Poll's are without expunges
		// but the reverse is not true - we always see updates/new messages.
		s.mbox.At = max(s.mbox.At, s.mbox.DeletesAt)
		log.Debug("synchronized",
			zap.Uint64("initial_modseq", uint64(initialAt)),
			zap.Uint64("initial_deletes_modseq", uint64(initialDeletesAt)),
			zap.Uint64("new_modseq", uint64(s.mbox.At)),
			zap.Uint64("new_deletes_modseq", uint64(s.mbox.DeletesAt)))
	}
}
