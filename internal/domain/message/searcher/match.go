package searcher

import (
	"bufio"
	"cmp"
	"context"
	"fmt"
	"io"
	"runtime/trace"
	"strings"
	"time"

	"github.com/emersion/go-message/mail"
	"github.com/emersion/go-message/textproto"
	"github.com/foxcpp/maddy-storage/internal/domain/message"
	"github.com/foxcpp/maddy-storage/internal/pkg/contextlib"
	"github.com/foxcpp/maddy-storage/internal/pkg/mimeutils"
	"go.uber.org/zap"
)

type FuncOpenPart func(context.Context, *message.Part) (io.ReadCloser, error)

func timeInRange(val time.Time, gt, lt time.Time, dateOnly bool) bool {
	if dateOnly {
		gt = time.Date(gt.Year(), gt.Month(), gt.Day(), 0, 0, 0, 0, time.UTC)
		lt = time.Date(lt.Year(), lt.Month(), lt.Day(), 0, 0, 0, 0, time.UTC)
		val = time.Date(val.Year(), val.Month(), val.Day(), 0, 0, 0, 0, time.UTC)
	}

	if gt.IsZero() && lt.IsZero() {
		return true
	}
	if gt.IsZero() {
		return val.Before(lt)
	}
	if lt.IsZero() {
		return val.After(gt)
	}

	return val.After(gt) && val.Before(lt)
}

func valInRange[T cmp.Ordered](val T, gt, lt T) bool {
	var empty T
	if gt != empty && val <= gt {
		return false
	}
	if lt != empty && val >= lt {
		return false
	}
	return true
}

func Match(ctx context.Context, ent *FoundMsg, m *message.Msg, openPart FuncOpenPart, cond *Cond) (bool, error) {
	defer trace.StartRegion(ctx, "maddy-storage/message.searcher.Match").End()

	if cond.FolderIDs != nil {
		inFolder := false
		for _, f := range cond.FolderIDs {
			if f == ent.FolderID {
				inFolder = true
				break
			}
		}
		if !inFolder {
			return false, nil
		}
	}
	if cond.NumericIDs != nil {
		for _, ids := range cond.NumericIDs {
			if ids.SeqNum {
				if !ids.Includes(ent.SeqNum) {
					return false, nil
				}
			} else {
				if !ids.Includes(ent.UID) {
					return false, nil
				}
			}
		}
	}
	if !timeInRange(m.ReceivedAt, cond.SentAfter, cond.SentBefore, cond.SentDateOnly) {
		return false, nil
	}
	if !timeInRange(m.CreatedAt, cond.ReceivedAfter, cond.ReceivedBefore, cond.ReceivedDateOnly) {
		return false, nil
	}
	if !timeInRange(m.UpdatedAt, cond.UpdatedGt, cond.UpdatedLt, false) {
		return false, nil
	}
	if !valInRange(ent.ModSeq, cond.ModSeqGt, cond.ModSeqLt) {
		return false, nil
	}
	if !valInRange(ent.ModSeq, cond.ModSeqGt, cond.ModSeqLt) {
		return false, nil
	}

	if !matchFlags(m, cond.Flag, cond.NoFlag) {
		return false, nil
	}
	for _, not := range cond.Not {
		notMatched, err := Match(ctx, ent, m, openPart, not)
		if err != nil {
			return false, err
		}
		if notMatched {
			return false, nil
		}
	}
	for _, or := range cond.Or {
		match1, err := Match(ctx, ent, m, openPart, or[0])
		if err != nil {
			return false, err
		}
		match2, err := Match(ctx, ent, m, openPart, or[1])
		if err != nil {
			return false, err
		}

		if !match1 && !match2 {
			return false, nil
		}
	}

	if cond.Header != nil {
		match, err := matchHeader(ctx, m, openPart, cond.Header)
		if err != nil {
			return false, err
		}
		if !match {
			return false, nil
		}
	}
	if cond.InBodyOnly != nil || cond.InHeaderBody != nil {
		match, err := matchBody(ctx, m, openPart, cond.InBodyOnly, cond.InHeaderBody)
		if err != nil {
			return false, err
		}
		if !match {
			return false, nil
		}
	}

	return true, nil
}

func matchFlags(m *message.Msg, flag, noFlag []string) bool {
	flagMap := make(map[string]struct{}, len(m.Flags))
	for _, f := range m.Flags {
		flagMap[strings.ToLower(f)] = struct{}{}
	}

	for _, f := range flag {
		if _, ok := flagMap[strings.ToLower(f)]; !ok {
			return false
		}
	}
	for _, f := range noFlag {
		if _, ok := flagMap[strings.ToLower(f)]; ok {
			return false
		}
	}
	return true
}

func matchHeader(ctx context.Context, m *message.Msg, openPart FuncOpenPart, cond []HeaderField) (bool, error) {
	if len(m.Parts) == 0 {
		return false, nil
	}
	if !m.Parts[0].Path.Empty() {
		panic("first part is not root")
	}
	partReader, err := openPart(ctx, &m.Parts[0])
	if err != nil {
		return false, fmt.Errorf("failed to open part %v for header matching: %w", m.Parts[0].ID, err)
	}
	defer partReader.Close()

	hdr, err := textproto.ReadHeader(bufio.NewReader(partReader))
	if err != nil {
		contextlib.FromContext(ctx).Warn("skipping message with malformed header",
			zap.Stringer("msg_id", m.ID), zap.Error(err))
		return false, nil
	}

	for _, field := range cond {
		match := false

		isAddress := isAddressField(field.Key)

		for _, val := range hdr.Values(field.Key) {
			if isAddress {
				val = normalizeAddressField(val)
			}
			if strings.Contains(val, field.Value) {
				match = true
			}
		}
		if !match {
			return false, nil
		}
	}
	return true, nil
}

func isAddressField(key string) bool {
	switch strings.ToLower(key) {
	case "sender", "from", "to", "cc", "bcc", "reply-to":
		return true
	default:
		return false
	}
}

func normalizeAddressField(value string) string {
	addrList, err := mail.ParseAddressList(value)
	if err != nil {
		return value
	}
	b := strings.Builder{}
	for i, addr := range addrList {
		b.WriteString(addr.String())
		if i != len(addrList)-1 {
			b.WriteString(", ")
		}
	}
	return b.String()
}

func matchBody(ctx context.Context, m *message.Msg, openPart FuncOpenPart, text, body []string) (bool, error) {
	substringsFound := make(map[string]struct{}, len(text)+len(body))

	// TODO Consider using trie for faster matching for many substrings.
	partsSearched := 0
	for p := range m.SearchableTextParts() {
		if p.TotalSize() > 1*1024*1024 { // More than 1 MB of text won't be searched as it might be too expensive.
			continue
		}

		if err := matchBodyPart(ctx, m, p, openPart, text, body, substringsFound); err != nil {
			return false, err
		}

		if len(substringsFound) == len(text)+len(body) {
			return true, nil
		}

		partsSearched++
		if partsSearched >= 10 {
			return false, nil
		}
	}

	return false, nil
}

func matchBodyPart(ctx context.Context, m *message.Msg, part *message.Part,
	openPart FuncOpenPart, text, body []string, substringsFound map[string]struct{},
) error {
	rc, err := openPart(ctx, part)
	if err != nil {
		contextlib.FromContext(ctx).Warn("unable to open part, skipping",
			zap.Stringer("msg_id", m.ID), zap.Stringer("part_id", part.ID), zap.Error(err))
		return nil
	}
	defer rc.Close()

	buf := bufio.NewReader(rc)

	if len(body) == 0 {
		// TODO: Proper handling of message/rfc822 inside a MIME part.
		if err := mimeutils.SkipHeader(buf); err != nil {
			contextlib.FromContext(ctx).Warn("unable to open part, skipping",
				zap.Stringer("msg_id", m.ID), zap.Stringer("part_id", part.ID), zap.Error(err))
			return nil
		}
	} else {
		// Might be malformed so disregard if there is an error.
		hdr, err := textproto.ReadHeader(buf)
		if err == nil {
			for f := hdr.Fields(); f.Next(); {
				for _, str := range body {
					if strings.Contains(f.Value(), str) {
						substringsFound[str] = struct{}{}
					}
				}
			}
		}
	}

	r := io.Reader(buf)
	if part.Content.Encoding != "" {
		decR, err := mimeutils.EncodingReader(part.Content.Encoding, buf)
		if err != nil {
			contextlib.FromContext(ctx).Warn("unable to open part, skipping",
				zap.Stringer("msg_id", m.ID), zap.Stringer("part_id", part.ID), zap.Error(err))
			return nil
		}
		r = decR
	}

	bufScnr := bufio.NewScanner(r)
	for bufScnr.Scan() {
		for _, str := range text {
			if strings.Contains(bufScnr.Text(), str) {
				substringsFound[str] = struct{}{}
			}
		}
		for _, str := range body {
			if strings.Contains(bufScnr.Text(), str) {
				substringsFound[str] = struct{}{}
			}
		}
	}
	if err := bufScnr.Err(); err != nil {
		contextlib.FromContext(ctx).Warn("skipping message due to an I/O error",
			zap.Stringer("msg_id", m.ID), zap.Stringer("part_id", part.ID), zap.Error(err))
		return nil
	}

	return nil
}
