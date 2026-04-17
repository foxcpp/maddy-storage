package imap2

import (
	"runtime/trace"

	"github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapserver"
	"github.com/foxcpp/maddy-storage/internal/domain/folder"
	"github.com/foxcpp/maddy-storage/internal/domain/folder/recent"
	"github.com/foxcpp/maddy-storage/internal/domain/message/searcher"
	"github.com/foxcpp/maddy-storage/internal/pkg/contextlib"
	"go.uber.org/zap"
)

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

	if criteria.Not != nil {
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
	if criteria.Or != nil {
		cond.Or = make([][]*searcher.Cond, len(criteria.Or))
		for i, or := range criteria.Or {
			var hasModSeqOr bool
			cond.Or[i] = make([]*searcher.Cond, 2)

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
	ctx = contextlib.WithLogger(ctx, log)

	cond, hasModSeq, err := s.criteriaAsSearcherCond(criteria)
	if err != nil {
		return nil, s.asIMAPError(err)
	}
	if hasModSeq && !s.mbox.CondStoreActive {
		s.mbox.CondStoreActive = true
	}

	ctx = recent.WithSet(ctx, s.mbox.Recents)

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

	data := &imap.SearchData{}

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
