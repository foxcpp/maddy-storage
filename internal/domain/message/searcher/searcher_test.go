package searcher

import (
	"testing"

	"github.com/foxcpp/maddy-storage/internal/domain/folder"
	"github.com/stretchr/testify/require"
)

func TestSearchResultAddAggregates(t *testing.T) {
	opts := &Opts{ReturnCount: true, ReturnMinUID: true, ReturnMaxUID: true, ReturnModSeq: true, ReturnTotalSize: true}

	var res SearchResult
	res.Add(opts, FoundMsg{UID: 5, SeqNum: 2, ModSeq: 7, TotalSize: 10})
	res.Add(opts, FoundMsg{UID: 2, SeqNum: 1, ModSeq: 9, TotalSize: 20})
	res.Add(opts, FoundMsg{UID: 8, SeqNum: 3, ModSeq: 4, TotalSize: 30})

	require.Equal(t, uint32(3), res.Count)
	require.Equal(t, uint32(2), res.MinUID)
	require.Equal(t, uint32(1), res.MinSeq)
	require.Equal(t, uint32(8), res.MaxUID)
	require.Equal(t, uint32(3), res.MaxSeq)
	require.Equal(t, folder.ModSeq(9), res.MaxModSeq)
	require.Equal(t, int64(60), res.TotalSize)
}

// Values that were not requested must not be filled in.
func TestSearchResultAddOnlyRequested(t *testing.T) {
	var res SearchResult
	res.Add(&Opts{}, FoundMsg{UID: 5, SeqNum: 2, ModSeq: 7, TotalSize: 10})

	require.Equal(t, SearchResult{}, res)
}
