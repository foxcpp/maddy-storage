package imap2

import (
	"io"
	"testing"

	"github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapclient"
	"github.com/foxcpp/maddy-storage/tests/imap2/utils"
	"github.com/stretchr/testify/require"
)

func appendMsg(t *testing.T, cl *imapclient.Client, mailbox, msg string) {
	t.Helper()

	cmd := cl.Append(mailbox, int64(len(msg)), &imap.AppendOptions{})
	_, err := io.WriteString(cmd, msg)
	require.NoError(t, err)
	require.NoError(t, cmd.Close())
	_, err = cmd.Wait()
	require.NoError(t, err)
}

// Searches that combine NOT/OR with content (header/body) conditions cannot be
// split into a metadata-only and a body-only part and must not crash or leak
// messages from other folders.
func TestSearchNotOrWithContentConditions(t *testing.T) {
	s := utils.TestServer(t)
	s.Run()
	defer s.Close()

	username, password := s.Account()

	cl := s.Client(utils.NewUnilateralRecorder())
	defer cl.Close()
	require.NoError(t, cl.Login(username, password).Wait())

	require.NoError(t, cl.Create("other", nil).Wait())

	appendMsg(t, cl, "INBOX", utils.CRLF("Subject: hello world\n\nfirst body\n"))
	appendMsg(t, cl, "INBOX", utils.CRLF("X-Extra: hello\n\nsecond body\n"))
	// Matches "NOT HEADER Subject ''" and must not be returned for INBOX.
	appendMsg(t, cl, "other", utils.CRLF("X-Other: foo\n\nthird body\n"))

	_, err := cl.Select("INBOX", nil).Wait()
	require.NoError(t, err)

	cases := map[string]struct {
		Criteria imap.SearchCriteria
		Want     []uint32
	}{
		"not header": {
			Criteria: imap.SearchCriteria{
				Not: []imap.SearchCriteria{{
					Header: []imap.SearchCriteriaHeaderField{{Key: "Subject"}},
				}},
			},
			Want: []uint32{2},
		},
		"not header with value": {
			Criteria: imap.SearchCriteria{
				Not: []imap.SearchCriteria{{
					Header: []imap.SearchCriteriaHeaderField{{Key: "Subject", Value: "hello"}},
				}},
			},
			Want: []uint32{2},
		},
		"not body": {
			Criteria: imap.SearchCriteria{
				Not: []imap.SearchCriteria{{Body: []string{"first"}}},
			},
			Want: []uint32{2},
		},
		"or header": {
			Criteria: imap.SearchCriteria{
				Or: [][2]imap.SearchCriteria{{
					{Header: []imap.SearchCriteriaHeaderField{{Key: "Subject", Value: "hello"}}},
					{Header: []imap.SearchCriteriaHeaderField{{Key: "X-Extra", Value: "hello"}}},
				}},
			},
			Want: []uint32{1, 2},
		},
		"flag and not header": {
			Criteria: imap.SearchCriteria{
				NotFlag: []imap.Flag{imap.FlagDeleted},
				Not: []imap.SearchCriteria{{
					Header: []imap.SearchCriteriaHeaderField{{Key: "Subject"}},
				}},
			},
			Want: []uint32{2},
		},
	}

	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			data, err := cl.Search(&c.Criteria, nil).Wait()
			require.NoError(t, err)
			require.Equal(t, c.Want, data.AllSeqNums())
		})
	}
}

// Sequence number ranges combined with NOT/OR and header conditions are
// evaluated in Go by the body matcher, which needs sequence numbers even for
// UID SEARCH.
func TestSearchSeqNumRangeWithNotOr(t *testing.T) {
	s := utils.TestServer(t)
	s.Run()
	defer s.Close()

	username, password := s.Account()

	cl := s.Client(utils.NewUnilateralRecorder())
	defer cl.Close()
	require.NoError(t, cl.Login(username, password).Wait())

	appendMsg(t, cl, "INBOX", utils.CRLF("Subject: hello world\n\nfirst body\n"))
	appendMsg(t, cl, "INBOX", utils.CRLF("X-Extra: hello\n\nsecond body\n"))
	appendMsg(t, cl, "INBOX", utils.CRLF("X-Extra: bye\n\nthird body\n"))

	_, err := cl.Select("INBOX", nil).Wait()
	require.NoError(t, err)

	subjectHello := imap.SearchCriteriaHeaderField{Key: "Subject", Value: "hello"}
	hasSubject := imap.SearchCriteriaHeaderField{Key: "Subject"}

	// Sequence numbers equal UIDs in this mailbox, so both commands must
	// return the same values.
	cases := map[string]struct {
		Criteria imap.SearchCriteria
		Want     []uint32
	}{
		"not seq with header": {
			Criteria: imap.SearchCriteria{
				Header: []imap.SearchCriteriaHeaderField{subjectHello},
				Not:    []imap.SearchCriteria{{SeqNum: []imap.SeqSet{imap.SeqSetNum(1)}}},
			},
			Want: nil,
		},
		"or seq with header": {
			Criteria: imap.SearchCriteria{
				Or: [][2]imap.SearchCriteria{{
					{SeqNum: []imap.SeqSet{imap.SeqSetNum(2)}},
					{Header: []imap.SearchCriteriaHeaderField{subjectHello}},
				}},
			},
			Want: []uint32{1, 2},
		},
		"top-level seq with not header": {
			Criteria: imap.SearchCriteria{
				SeqNum: []imap.SeqSet{imap.SeqSetNum(2, 3)},
				Not:    []imap.SearchCriteria{{Header: []imap.SearchCriteriaHeaderField{hasSubject}}},
			},
			Want: []uint32{2, 3},
		},
		"top-level seq with not seq and header": {
			Criteria: imap.SearchCriteria{
				SeqNum: []imap.SeqSet{imap.SeqSetNum(1, 2)},
				Not:    []imap.SearchCriteria{{SeqNum: []imap.SeqSet{imap.SeqSetNum(1)}}},
				Header: []imap.SearchCriteriaHeaderField{{Key: "X-Extra"}},
			},
			Want: []uint32{2},
		},
	}

	for name, c := range cases {
		t.Run(name+"/SEARCH", func(t *testing.T) {
			data, err := cl.Search(&c.Criteria, nil).Wait()
			require.NoError(t, err)
			require.Equal(t, c.Want, data.AllSeqNums())
		})
		t.Run(name+"/UID SEARCH", func(t *testing.T) {
			data, err := cl.UIDSearch(&c.Criteria, nil).Wait()
			require.NoError(t, err)

			var uids []uint32
			for _, u := range data.AllUIDs() {
				uids = append(uids, uint32(u))
			}
			require.Equal(t, c.Want, uids)
		})
	}
}

// RFC822.SIZE must report the real message size and SMALLER/LARGER searches
// (also nested in NOT/OR) must be evaluated against it.
func TestSearchSize(t *testing.T) {
	s := utils.TestServer(t)
	s.Run()
	defer s.Close()

	username, password := s.Account()

	cl := s.Client(utils.NewUnilateralRecorder())
	defer cl.Close()
	require.NoError(t, cl.Login(username, password).Wait())

	require.NoError(t, cl.Create("other", nil).Wait())

	// Different sizes in increasing order.
	msgs := []string{
		utils.CRLF("Subject: a\n\n1\n"),
		utils.CRLF("Subject: a\n\n22\n"),
		utils.CRLF("Subject: a\n\n333\n"),
		utils.CRLF("Subject: a\n\n4444\n"),
	}
	for _, m := range msgs {
		appendMsg(t, cl, "INBOX", m)
	}
	// Must never be returned for INBOX.
	appendMsg(t, cl, "other", utils.CRLF("Subject: a\n\n55555\n"))

	_, err := cl.Select("INBOX", nil).Wait()
	require.NoError(t, err)

	t.Run("fetch rfc822.size", func(t *testing.T) {
		bufs, err := cl.Fetch(imap.SeqSetNum(1, 2, 3, 4), &imap.FetchOptions{RFC822Size: true}).Collect()
		require.NoError(t, err)
		require.Len(t, bufs, 4)
		// Responses are not guaranteed to be ordered.
		for _, b := range bufs {
			require.Equal(t, int64(len(msgs[b.SeqNum-1])), b.RFC822Size, "message %d", b.SeqNum)
		}
	})

	mid := uint32(len(msgs[2]))
	smaller := imap.SearchCriteria{Smaller: int64(mid)}
	larger := imap.SearchCriteria{Larger: int64(mid)}

	cases := map[string]struct {
		Criteria imap.SearchCriteria
		Want     []uint32
	}{
		"smaller": {smaller, []uint32{1, 2}},
		"larger":  {larger, []uint32{4}},
		"not smaller": {
			imap.SearchCriteria{Not: []imap.SearchCriteria{smaller}}, []uint32{3, 4},
		},
		"not larger": {
			imap.SearchCriteria{Not: []imap.SearchCriteria{larger}}, []uint32{1, 2, 3},
		},
		"not smaller not larger": {
			imap.SearchCriteria{Not: []imap.SearchCriteria{smaller, larger}}, []uint32{3},
		},
		"or smaller larger": {
			imap.SearchCriteria{Or: [][2]imap.SearchCriteria{{smaller, larger}}}, []uint32{1, 2, 4},
		},
		"smaller and larger": {
			imap.SearchCriteria{Smaller: int64(mid + 1), Larger: int64(mid - 1)}, []uint32{3},
		},
		"smaller and header": {
			imap.SearchCriteria{
				Smaller: int64(mid),
				Header:  []imap.SearchCriteriaHeaderField{{Key: "Subject", Value: "a"}},
			},
			[]uint32{1, 2},
		},
		"not smaller and header": {
			imap.SearchCriteria{
				Not:    []imap.SearchCriteria{smaller},
				Header: []imap.SearchCriteriaHeaderField{{Key: "Subject", Value: "a"}},
			},
			[]uint32{3, 4},
		},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			data, err := cl.Search(&c.Criteria, nil).Wait()
			require.NoError(t, err)
			require.Equal(t, c.Want, data.AllSeqNums())
		})
	}
}

// Copies must keep the size of the original message, both as reported by
// FETCH and as seen by SMALLER/LARGER searches.
func TestCopyPreservesSize(t *testing.T) {
	s := utils.TestServer(t)
	s.Run()
	defer s.Close()

	username, password := s.Account()

	cl := s.Client(utils.NewUnilateralRecorder())
	defer cl.Close()
	require.NoError(t, cl.Login(username, password).Wait())

	require.NoError(t, cl.Create("copy", nil).Wait())

	msg := utils.CRLF("Subject: a\n\nbody\n")
	appendMsg(t, cl, "INBOX", msg)

	_, err := cl.Select("INBOX", nil).Wait()
	require.NoError(t, err)
	_, err = cl.Copy(imap.SeqSetNum(1), "copy").Wait()
	require.NoError(t, err)

	_, err = cl.Select("copy", nil).Wait()
	require.NoError(t, err)

	bufs, err := cl.Fetch(imap.SeqSetNum(1), &imap.FetchOptions{RFC822Size: true}).Collect()
	require.NoError(t, err)
	require.Len(t, bufs, 1)
	require.Equal(t, int64(len(msg)), bufs[0].RFC822Size)

	data, err := cl.Search(&imap.SearchCriteria{Larger: int64(len(msg) - 1)}, nil).Wait()
	require.NoError(t, err)
	require.Equal(t, []uint32{1}, data.AllSeqNums())
}
