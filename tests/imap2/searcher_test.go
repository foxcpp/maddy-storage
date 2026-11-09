package imap2

import (
	"context"
	"testing"

	"github.com/foxcpp/maddy-storage/internal/domain/folder"
	"github.com/foxcpp/maddy-storage/internal/domain/message/searcher"
	"github.com/foxcpp/maddy-storage/tests/imap2/utils"
	"github.com/stretchr/testify/require"
)

// Direct searcher tests for behavior not reachable (yet) through the IMAP
// protocol layer, e.g. because it always restricts searches to one folder.

func searcherTestSetup(t *testing.T) (s *utils.Server, accountID [16]byte, cleanup func()) {
	t.Helper()

	s = utils.TestServer(t)
	s.Run()

	username, password := s.Account()
	cl := s.Client(utils.NewUnilateralRecorder())
	require.NoError(t, cl.Login(username, password).Wait())

	require.NoError(t, cl.Create("other", nil).Wait())
	appendMsg(t, cl, "INBOX", utils.CRLF("Subject: a\n\nbody\n"))
	appendMsg(t, cl, "INBOX", utils.CRLF("Subject: b\n\nbody\n"))
	appendMsg(t, cl, "other", utils.CRLF("Subject: c\n\nbody\n"))
	require.NoError(t, cl.Close())

	acct, err := s.Accounts.GetByName(context.Background(), username)
	require.NoError(t, err)

	return s, acct.ID, s.Close
}

// FoundMsg.ModSeq is evaluated by the content matcher for MODSEQ conditions, so
// it has to be populated.
func TestSearcherFoundMsgHasModSeq(t *testing.T) {
	s, accountID, cleanup := searcherTestSetup(t)
	defer cleanup()

	res, err := s.Searcher.Search(context.Background(), accountID, &searcher.Cond{},
		searcher.Opts{ReturnAll: true, ReturnModSeq: true})
	require.NoError(t, err)
	require.Len(t, res.All, 3)

	for _, m := range res.All {
		require.NotZero(t, m.ModSeq, "uid %d", m.UID)
	}
	require.Equal(t, folder.ModSeq(3), res.MaxModSeq)
}

// Sequence numbers are per-folder even if the search is not restricted to
// any folder.
func TestSearcherSeqNumsWithoutFolderFilter(t *testing.T) {
	s, accountID, cleanup := searcherTestSetup(t)
	defer cleanup()

	res, err := s.Searcher.Search(context.Background(), accountID, &searcher.Cond{},
		searcher.Opts{ReturnAll: true, ReturnSeqNums: true, At: 100})
	require.NoError(t, err)
	require.Len(t, res.All, 3)

	seqs := map[uint32]int{}
	for _, m := range res.All {
		seqs[m.SeqNum]++
	}
	// INBOX: 1,2; other: 1.
	require.Equal(t, map[uint32]int{1: 2, 2: 1}, seqs)
}

// Aggregated searches grouped by folder return both the per-folder and the
// overall aggregates.
func TestSearcherGroupByFolder(t *testing.T) {
	s, accountID, cleanup := searcherTestSetup(t)
	defer cleanup()

	res, err := s.Searcher.Search(context.Background(), accountID, &searcher.Cond{},
		searcher.Opts{ReturnCount: true, ReturnMinUID: true, ReturnMaxUID: true, GroupByFolder: true})
	require.NoError(t, err)

	require.Equal(t, uint32(3), res.Count)
	require.Equal(t, uint32(1), res.MinUID)
	require.Equal(t, uint32(2), res.MaxUID)

	require.Len(t, res.ByFolder, 2)
	counts := []uint32{}
	for _, f := range res.ByFolder {
		counts = append(counts, f.Count)
		require.Equal(t, uint32(1), f.MinUID)
		require.Equal(t, f.Count, f.MaxUID, "UIDs are allocated sequentially from 1 in each folder")
	}
	require.ElementsMatch(t, []uint32{1, 2}, counts)
}
