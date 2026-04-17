package imap2

import (
	"testing"
	"time"

	"github.com/emersion/go-imap/v2"
	"github.com/stretchr/testify/require"
)

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
