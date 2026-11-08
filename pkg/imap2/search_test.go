package imap2

import (
	"testing"
	"time"

	"github.com/emersion/go-imap/v2"
	"github.com/foxcpp/maddy-storage/internal/domain/folder"
	"github.com/foxcpp/maddy-storage/internal/domain/message/searcher"
	"github.com/stretchr/testify/require"
)

func newSearchTestSession() *session {
	return &session{
		mbox: selectedMbox{
			Msgs:      10,
			MaxUID:    20,
			At:        5,
			DeletesAt: 4,
		},
	}
}

func TestCriteriaAsSearcherCondNil(t *testing.T) {
	cond, hasModSeq, err := newSearchTestSession().criteriaAsSearcherCond(nil)
	require.NoError(t, err)
	require.False(t, hasModSeq)
	require.Nil(t, cond)
}

func TestCriteriaAsSearcherCondBuildsNotAndOr(t *testing.T) {
	s := &session{}

	cond, hasModSeq, err := s.criteriaAsSearcherCond(&imap.SearchCriteria{
		Not: []imap.SearchCriteria{
			{SentBefore: time.Date(2007, time.March, 26, 0, 0, 0, 0, time.UTC)},
		},
		Or: [][2]imap.SearchCriteria{{
			{SentSince: time.Date(2007, time.October, 28, 0, 0, 0, 0, time.UTC)},
			{SentBefore: time.Date(2007, time.October, 29, 0, 0, 0, 0, time.UTC)},
		}},
	})
	require.NoError(t, err)
	require.False(t, hasModSeq)
	require.Len(t, cond.Not, 1)
	require.Equal(t, time.Date(2007, time.March, 26, 0, 0, 0, 0, time.UTC), cond.Not[0].SentBefore)
	require.Len(t, cond.Or, 1)
	require.Len(t, cond.Or[0], 2)
	require.Equal(t, time.Date(2007, time.October, 28, 0, 0, 0, 0, time.UTC), cond.Or[0][0].SentAfter)
	require.Equal(t, time.Date(2007, time.October, 29, 0, 0, 0, 0, time.UTC), cond.Or[0][1].SentBefore)
}

// IMAP TEXT matches header and body, IMAP BODY matches the body only. Swapping them
// goes unnoticed for any term that appears in a message body, so pin it down using
// both keys with distinct terms.
func TestCriteriaAsSearcherCondTextAndBody(t *testing.T) {
	cases := map[string]struct {
		Criteria     imap.SearchCriteria
		InHeaderBody []string
		InBodyOnly   []string
	}{
		"text only": {
			Criteria:     imap.SearchCriteria{Text: []string{"qwertyuiop"}},
			InHeaderBody: []string{"qwertyuiop"},
		},
		"body only": {
			Criteria:   imap.SearchCriteria{Body: []string{"qwertyuiop"}},
			InBodyOnly: []string{"qwertyuiop"},
		},
		"both": {
			Criteria:     imap.SearchCriteria{Text: []string{"text1"}, Body: []string{"body1"}},
			InHeaderBody: []string{"text1"},
			InBodyOnly:   []string{"body1"},
		},
	}

	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			cond, _, err := newSearchTestSession().criteriaAsSearcherCond(&c.Criteria)
			require.NoError(t, err)
			require.Equal(t, c.InHeaderBody, cond.InHeaderBody)
			require.Equal(t, c.InBodyOnly, cond.InBodyOnly)
		})
	}
}

func TestCriteriaAsSearcherCondScalarFields(t *testing.T) {
	since := time.Date(2007, time.March, 24, 0, 0, 0, 0, time.UTC)
	before := time.Date(2007, time.March, 26, 0, 0, 0, 0, time.UTC)
	sentSince := time.Date(2007, time.October, 28, 0, 0, 0, 0, time.UTC)
	sentBefore := time.Date(2007, time.October, 29, 0, 0, 0, 0, time.UTC)

	cond, hasModSeq, err := newSearchTestSession().criteriaAsSearcherCond(&imap.SearchCriteria{
		Header:     []imap.SearchCriteriaHeaderField{{Key: "Subject", Value: "qwertyuiop"}},
		Flag:       []imap.Flag{imap.FlagSeen},
		NotFlag:    []imap.Flag{imap.FlagDeleted},
		Since:      since,
		Before:     before,
		SentSince:  sentSince,
		SentBefore: sentBefore,
		Larger:     100,
		Smaller:    200,
		ModSeq:     &imap.SearchCriteriaModSeq{ModSeq: 42},
	})
	require.NoError(t, err)
	require.True(t, hasModSeq)

	require.Equal(t, []searcher.HeaderField{{Key: "Subject", Value: "qwertyuiop"}}, cond.Header)
	require.Equal(t, []string{"\\Seen"}, cond.Flag)
	require.Equal(t, []string{"\\Deleted"}, cond.NoFlag)

	require.Equal(t, since, cond.ReceivedAfter)
	require.Equal(t, before, cond.ReceivedBefore)
	require.Equal(t, sentSince, cond.SentAfter)
	require.Equal(t, sentBefore, cond.SentBefore)
	require.True(t, cond.ReceivedDateOnly, "SINCE/BEFORE ignore time and timezone")
	require.True(t, cond.SentDateOnly, "SENTSINCE/SENTBEFORE ignore time and timezone")

	require.Equal(t, uint32(100), cond.SizeGt)
	require.Equal(t, uint32(200), cond.SizeLt)
	require.Equal(t, folder.ModSeq(42), cond.ModSeqGt)
}

func TestCriteriaAsSearcherCondNumericIDs(t *testing.T) {
	cond, _, err := newSearchTestSession().criteriaAsSearcherCond(&imap.SearchCriteria{
		UID:    []imap.UIDSet{imap.UIDSetNum(3, 7)},
		SeqNum: []imap.SeqSet{imap.SeqSetNum(2)},
	})
	require.NoError(t, err)
	require.Len(t, cond.NumericIDs, 2)

	uids := cond.NumericIDs[0]
	require.False(t, uids.SeqNum)
	require.Equal(t, []uint32{3, 7}, uids.Values)
	require.Equal(t, folder.ModSeq(5), uids.At)
	require.Equal(t, folder.ModSeq(4), uids.DeletesAt)

	seqs := cond.NumericIDs[1]
	require.True(t, seqs.SeqNum)
	require.Equal(t, []uint32{2}, seqs.Values)
}

func TestCriteriaAsSearcherCondSeqOutOfRange(t *testing.T) {
	_, _, err := newSearchTestSession().criteriaAsSearcherCond(&imap.SearchCriteria{
		SeqNum: []imap.SeqSet{imap.SeqSetNum(11)}, // mailbox has 10 messages
	})
	require.ErrorIs(t, err, errSeqOutOfRange)
}

func TestCriteriaAsSearcherCondModSeqPropagatesFromNestedCriteria(t *testing.T) {
	modSeq := imap.SearchCriteria{ModSeq: &imap.SearchCriteriaModSeq{ModSeq: 1}}

	cases := map[string]imap.SearchCriteria{
		"not":       {Not: []imap.SearchCriteria{modSeq}},
		"or first":  {Or: [][2]imap.SearchCriteria{{modSeq, {}}}},
		"or second": {Or: [][2]imap.SearchCriteria{{{}, modSeq}}},
	}

	for name, criteria := range cases {
		t.Run(name, func(t *testing.T) {
			_, hasModSeq, err := newSearchTestSession().criteriaAsSearcherCond(&criteria)
			require.NoError(t, err)
			require.True(t, hasModSeq)
		})
	}
}
