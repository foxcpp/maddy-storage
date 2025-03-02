package searcher

import (
	"time"

	"github.com/foxcpp/maddy-storage/internal/domain/folder"
	"github.com/oklog/ulid/v2"
)

type HeaderField struct {
	Key   string
	Value string
}

type Cond struct {
	FolderIDs  []ulid.ULID
	NumericIDs []folder.Range

	SentAfter        time.Time
	SentBefore       time.Time
	SentDateOnly     bool // ignore timezone and time
	ReceivedAfter    time.Time
	ReceivedBefore   time.Time
	ReceivedDateOnly bool // ignore timezone and time
	UpdatedGt        time.Time
	UpdatedLt        time.Time
	ModSeqGt         folder.ModSeq
	ModSeqLt         folder.ModSeq

	Header       []HeaderField // Matches root part header.
	InHeaderBody []string      // Matches any part.
	InBodyOnly   []string      // Matches only text/* parts.

	SizeGt uint32
	SizeLt uint32

	Flag   []string
	NoFlag []string

	Not []*Cond
	Or  [][]*Cond
}

func (s *Cond) And(other *Cond) {
	s.FolderIDs = append(s.FolderIDs, other.FolderIDs...) // actually, should be an interaction...
	s.NumericIDs = append(s.NumericIDs, other.NumericIDs...)
	if other.SentAfter.After(s.SentAfter) {
		s.SentAfter = other.SentAfter
	}
	if other.SentBefore.Before(s.SentBefore) {
		s.SentBefore = other.SentBefore
	}
	if other.ReceivedAfter.After(s.ReceivedAfter) {
		s.ReceivedAfter = other.ReceivedAfter
	}
	if other.ReceivedBefore.Before(s.ReceivedBefore) {
		s.ReceivedBefore = other.ReceivedBefore
	}
	if other.UpdatedGt.After(s.UpdatedGt) {
		s.UpdatedGt = other.UpdatedGt
	}
	if other.UpdatedLt.Before(s.UpdatedLt) {
		s.UpdatedLt = other.UpdatedLt
	}
	s.Header = append(s.Header, s.Header...)
	s.InHeaderBody = append(s.InHeaderBody, s.InHeaderBody...)
	s.InBodyOnly = append(s.InBodyOnly, s.InBodyOnly...)
	s.SizeGt = max(s.SizeGt, other.SizeGt)
	s.SizeLt = min(s.SizeLt, other.SizeLt)
	s.Flag = append(s.Flag, other.Flag...)
	s.Not = append(s.Not, other.Not...)
	s.Or = append(s.Or, other.Or...)
}

func (s *Cond) NeedsBody() bool {
	if s.Header != nil || s.InHeaderBody != nil || s.InBodyOnly != nil {
		return true
	}
	for _, not := range s.Not {
		if not.NeedsBody() {
			return true
		}
	}
	for _, or := range s.Or {
		if or[0].NeedsBody() || or[1].NeedsBody() {
			return true
		}
	}
	return false
}

type SplitSearchCond struct {
	Metadata *Cond
	Body     *Cond
}

func (s *Cond) NeedsSeqNum() bool {
	for _, rang := range s.NumericIDs {
		if rang.SeqNum {
			return true
		}
	}

	for _, not := range s.Not {
		if not.NeedsSeqNum() {
			return true
		}
	}
	for _, or := range s.Or {
		if or[0].NeedsSeqNum() || or[1].NeedsSeqNum() {
			return true
		}
	}

	return false
}

func (s *Cond) SplitMetaBody() SplitSearchCond {
	if !s.NeedsBody() {
		return SplitSearchCond{
			Metadata: s,
			Body:     nil,
		}
	}

	if s.Not != nil || s.Or != nil {
		return SplitSearchCond{
			Metadata: nil,
			Body:     s,
		}
	}

	meta := *s
	meta.InHeaderBody = nil
	meta.InBodyOnly = nil
	meta.Header = nil
	return SplitSearchCond{
		Metadata: &meta,
		Body: &Cond{
			Header:       s.Header,
			InHeaderBody: s.InHeaderBody,
			InBodyOnly:   s.InBodyOnly,
		},
	}

	/* TODO: Consider some boolean algebra and support more cases with NOT and OR support.
	s = metadataOnly & bodyOnly

	s = meta1 & body1 & !(meta2 & body2)
	s = meta1 & body1 & (!meta2 | !body2)
	s = (meta1 & !meta2 & body1) | (meta1 & body1 & !body2)

	s = meta1 & body1 & ((meta2 & body2) | (meta3 & body3))
	s = (meta1 & meta2 & body1 & body2) | (meta1 & meta3 body1 & body3)

	*/
}
