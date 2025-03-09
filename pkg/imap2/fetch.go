package imap2

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"runtime/trace"
	"strconv"
	"strings"

	"github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapserver"
	"github.com/foxcpp/maddy-storage/internal/domain/folder"
	"github.com/foxcpp/maddy-storage/internal/domain/message"
	messageusecase "github.com/foxcpp/maddy-storage/internal/domain/message/usecase"
	"github.com/foxcpp/maddy-storage/internal/pkg/contextlog"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

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
