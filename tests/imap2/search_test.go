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

// RFC 5322 allows comments (CFWS) around the local-part and the domain of an
// addr-spec. net/mail does not, so such headers need a tolerant fallback for
// both SEARCH and the stored ENVELOPE.
func TestAddressWithCommentsInAddrSpec(t *testing.T) {
	s := utils.TestServer(t)
	s.Run()
	defer s.Close()

	username, password := s.Account()

	cl := s.Client(utils.NewUnilateralRecorder())
	defer cl.Close()
	require.NoError(t, cl.Login(username, password).Wait())

	appendMsg(t, cl, "INBOX", utils.CRLF("From: <user-from (comment)@ (comment) domain.org>\n\nbody\n"))
	appendMsg(t, cl, "INBOX", utils.CRLF("From: user-other@domain.org\n\nbody\n"))

	_, err := cl.Select("INBOX", nil).Wait()
	require.NoError(t, err)

	t.Run("search", func(t *testing.T) {
		data, err := cl.Search(&imap.SearchCriteria{
			Header: []imap.SearchCriteriaHeaderField{{Key: "From", Value: "user-from@domain.org"}},
		}, nil).Wait()
		require.NoError(t, err)
		require.Equal(t, []uint32{1}, data.AllSeqNums())
	})

	t.Run("envelope", func(t *testing.T) {
		bufs, err := cl.Fetch(imap.SeqSetNum(1), &imap.FetchOptions{Envelope: true}).Collect()
		require.NoError(t, err)
		require.Len(t, bufs, 1)
		require.NotNil(t, bufs[0].Envelope)

		want := []imap.Address{{Mailbox: "user-from", Host: "domain.org"}}
		require.Equal(t, want, bufs[0].Envelope.From)
		require.Equal(t, want, bufs[0].Envelope.Sender)
		require.Equal(t, want, bufs[0].Envelope.ReplyTo)
	})
}

// RFC 3501 6.4.4: BODY, TEXT and HEADER substring matching is case-insensitive.
func TestSearchSubstringCaseInsensitive(t *testing.T) {
	s := utils.TestServer(t)
	s.Run()
	defer s.Close()

	username, password := s.Account()

	cl := s.Client(utils.NewUnilateralRecorder())
	defer cl.Close()
	require.NoError(t, cl.Login(username, password).Wait())

	appendMsg(t, cl, "INBOX", utils.CRLF("Subject: Hello World\n\nSome BODY Text\n"))
	appendMsg(t, cl, "INBOX", utils.CRLF("Subject: other\n\nnothing\n"))

	_, err := cl.Select("INBOX", nil).Wait()
	require.NoError(t, err)

	cases := map[string]imap.SearchCriteria{
		"body":        {Body: []string{"some body text"}},
		"body upper":  {Body: []string{"SOME BODY"}},
		"text body":   {Text: []string{"body text"}},
		"text header": {Text: []string{"hello world"}},
		"body header": {Body: []string{"hello world"}}, // BODY must not match headers.
		"header":      {Header: []imap.SearchCriteriaHeaderField{{Key: "Subject", Value: "HELLO"}}},
	}
	want := map[string][]uint32{
		"body": {1}, "body upper": {1}, "text body": {1}, "text header": {1},
		"body header": nil, "header": {1},
	}

	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			data, err := cl.Search(&c, nil).Wait()
			require.NoError(t, err)
			require.Equal(t, want[name], data.AllSeqNums())
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

// ESEARCH MIN/MAX in plain SEARCH must be reported as sequence numbers, not
// UIDs. Message with seq 4 is expunged so that sequence numbers and UIDs
// diverge.
func TestESearchMinMaxSeqNum(t *testing.T) {
	s := utils.TestServer(t)
	s.Run()
	defer s.Close()

	username, password := s.Account()

	cl := s.Client(utils.NewUnilateralRecorder())
	defer cl.Close()
	require.NoError(t, cl.Login(username, password).Wait())

	for _, body := range []string{"1", "2", "3", "4", "5"} {
		appendMsg(t, cl, "INBOX", utils.CRLF("Subject: "+body+"\n\nbody\n"))
	}

	_, err := cl.Select("INBOX", nil).Wait()
	require.NoError(t, err)

	require.NoError(t, cl.Store(imap.SeqSetNum(1), &imap.StoreFlags{
		Op: imap.StoreFlagsAdd, Flags: []imap.Flag{imap.FlagDeleted}, Silent: true,
	}, nil).Close())
	require.NoError(t, cl.Expunge().Close())

	// Remaining UIDs: 2,3,4,5 -> seq 1,2,3,4.
	opts := &imap.SearchOptions{ReturnMin: true, ReturnMax: true, ReturnCount: true}

	data, err := cl.Search(&imap.SearchCriteria{}, opts).Wait()
	require.NoError(t, err)
	require.Equal(t, uint32(1), data.Min, "SEARCH MIN is a sequence number")
	require.Equal(t, uint32(4), data.Max, "SEARCH MAX is a sequence number")
	require.Equal(t, uint32(4), data.Count)

	data, err = cl.UIDSearch(&imap.SearchCriteria{}, opts).Wait()
	require.NoError(t, err)
	require.Equal(t, uint32(2), data.Min, "UID SEARCH MIN is a UID")
	require.Equal(t, uint32(5), data.Max, "UID SEARCH MAX is a UID")
	require.Equal(t, uint32(4), data.Count)
}

// A sequence number past the end of the mailbox is not an error for SEARCH, it
// just does not match anything.
func TestSearchSeqNumOutOfRange(t *testing.T) {
	s := utils.TestServer(t)
	s.Run()
	defer s.Close()

	username, password := s.Account()

	cl := s.Client(utils.NewUnilateralRecorder())
	defer cl.Close()
	require.NoError(t, cl.Login(username, password).Wait())

	appendMsg(t, cl, "INBOX", utils.CRLF("Subject: a\n\nbody\n"))
	appendMsg(t, cl, "INBOX", utils.CRLF("Subject: b\n\nbody\n"))

	_, err := cl.Select("INBOX", nil).Wait()
	require.NoError(t, err)

	cases := map[string]struct {
		Criteria imap.SearchCriteria
		Want     []uint32
	}{
		"single":         {imap.SearchCriteria{SeqNum: []imap.SeqSet{imap.SeqSetNum(1000)}}, nil},
		"range past end": {imap.SearchCriteria{SeqNum: []imap.SeqSet{{{Start: 2, Stop: 1000}}}}, []uint32{2}},
		"mixed":          {imap.SearchCriteria{SeqNum: []imap.SeqSet{imap.SeqSetNum(1, 1000)}}, []uint32{1}},
		"not":            {imap.SearchCriteria{Not: []imap.SearchCriteria{{SeqNum: []imap.SeqSet{imap.SeqSetNum(1000)}}}}, []uint32{1, 2}},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			data, err := cl.Search(&c.Criteria, nil).Wait()
			require.NoError(t, err)
			require.Equal(t, c.Want, data.AllSeqNums())

			data, err = cl.Search(&c.Criteria, &imap.SearchOptions{ReturnCount: true}).Wait()
			require.NoError(t, err)
			require.Equal(t, uint32(len(c.Want)), data.Count)
		})
	}
}

// The rest of the scripted esearch case: ESEARCH ALL/COUNT after an expunge,
// with sequence numbers and UIDs diverging.
func TestESearchAllAfterExpunge(t *testing.T) {
	s := utils.TestServer(t)
	s.Run()
	defer s.Close()

	username, password := s.Account()

	cl := s.Client(utils.NewUnilateralRecorder())
	defer cl.Close()
	require.NoError(t, cl.Login(username, password).Wait())

	for _, body := range []string{"1", "2", "3", "4", "5", "6", "7"} {
		appendMsg(t, cl, "INBOX", utils.CRLF("Subject: "+body+"\n\nbody\n"))
	}

	_, err := cl.Select("INBOX", nil).Wait()
	require.NoError(t, err)

	require.NoError(t, cl.Store(imap.SeqSetNum(4), &imap.StoreFlags{
		Op: imap.StoreFlagsAdd, Flags: []imap.Flag{imap.FlagDeleted}, Silent: true,
	}, nil).Close())
	require.NoError(t, cl.Expunge().Close())

	// UIDs 1..3 and 5..7 remain, at sequence numbers 1..6.
	data, err := cl.Search(&imap.SearchCriteria{}, &imap.SearchOptions{ReturnAll: true}).Wait()
	require.NoError(t, err)
	require.Equal(t, []uint32{1, 2, 3, 4, 5, 6}, data.AllSeqNums())

	data, err = cl.UIDSearch(&imap.SearchCriteria{}, &imap.SearchOptions{ReturnAll: true}).Wait()
	require.NoError(t, err)
	require.Equal(t, []imap.UID{1, 2, 3, 5, 6, 7}, data.AllUIDs())

	data, err = cl.UIDSearch(&imap.SearchCriteria{}, &imap.SearchOptions{ReturnCount: true}).Wait()
	require.NoError(t, err)
	require.Equal(t, uint32(6), data.Count)
}

// ESEARCH COUNT must work for searches that need message content matching
// (header/body), not only for metadata ones.
func TestESearchCountWithContentConditions(t *testing.T) {
	s := utils.TestServer(t)
	s.Run()
	defer s.Close()

	username, password := s.Account()

	cl := s.Client(utils.NewUnilateralRecorder())
	defer cl.Close()
	require.NoError(t, cl.Login(username, password).Wait())

	appendMsg(t, cl, "INBOX", utils.CRLF("Subject: hello\n\nfoo\n"))
	appendMsg(t, cl, "INBOX", utils.CRLF("Subject: hello\n\nbar\n"))
	appendMsg(t, cl, "INBOX", utils.CRLF("Subject: other\n\nfoo\n"))

	_, err := cl.Select("INBOX", nil).Wait()
	require.NoError(t, err)

	cases := map[string]struct {
		Criteria imap.SearchCriteria
		Count    uint32
	}{
		"header": {imap.SearchCriteria{Header: []imap.SearchCriteriaHeaderField{{Key: "Subject", Value: "hello"}}}, 2},
		"body":   {imap.SearchCriteria{Body: []string{"foo"}}, 2},
		"none":   {imap.SearchCriteria{Body: []string{"nonexistent"}}, 0},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			data, err := cl.Search(&c.Criteria, &imap.SearchOptions{ReturnCount: true}).Wait()
			require.NoError(t, err)
			require.Equal(t, c.Count, data.Count)
		})
	}
}

// Errors that are already IMAP responses must reach the client as is instead
// of being reported as an internal server error.
func TestFetchSeqNumOutOfRangeIsNotServerBug(t *testing.T) {
	s := utils.TestServer(t)
	s.Run()
	defer s.Close()

	username, password := s.Account()

	cl := s.Client(utils.NewUnilateralRecorder())
	defer cl.Close()
	require.NoError(t, cl.Login(username, password).Wait())

	appendMsg(t, cl, "INBOX", utils.CRLF("Subject: a\n\nbody\n"))

	_, err := cl.Select("INBOX", nil).Wait()
	require.NoError(t, err)

	_, err = cl.Fetch(imap.SeqSetNum(1000), &imap.FetchOptions{UID: true}).Collect()
	var imapErr *imap.Error
	require.ErrorAs(t, err, &imapErr)
	require.NotEqual(t, imap.ResponseCodeServerBug, imapErr.Code)
	require.Equal(t, imap.ResponseCodeCannot, imapErr.Code)
}

// LIST-STATUS counters are aggregated per folder.
func TestListStatusCountsPerFolder(t *testing.T) {
	s := utils.TestServer(t)
	s.Run()
	defer s.Close()

	username, password := s.Account()

	cl := s.Client(utils.NewUnilateralRecorder())
	defer cl.Close()
	require.NoError(t, cl.Login(username, password).Wait())

	require.NoError(t, cl.Create("other", nil).Wait())
	require.NoError(t, cl.Create("empty", nil).Wait())
	appendMsg(t, cl, "INBOX", utils.CRLF("Subject: a\n\nbody\n"))
	appendMsg(t, cl, "INBOX", utils.CRLF("Subject: b\n\nbody\n"))
	appendMsg(t, cl, "other", utils.CRLF("Subject: c\n\nbody\n"))

	list, err := cl.List("", "*", &imap.ListOptions{
		ReturnStatus: &imap.StatusOptions{NumMessages: true, NumUnseen: true},
	}).Collect()
	require.NoError(t, err)

	got := map[string][2]uint32{}
	for _, l := range list {
		require.NotNil(t, l.Status, l.Mailbox)
		got[l.Mailbox] = [2]uint32{*l.Status.NumMessages, *l.Status.NumUnseen}
	}
	require.Equal(t, map[string][2]uint32{
		"INBOX": {2, 2},
		"other": {1, 1},
		"empty": {0, 0},
	}, got)
}

// FETCH responses must come in ascending sequence number order. Imaptest
// scripts (and many clients) depend on it.
func TestFetchReturnsMessagesInOrder(t *testing.T) {
	s := utils.TestServer(t)
	s.Run()
	defer s.Close()

	username, password := s.Account()

	cl := s.Client(utils.NewUnilateralRecorder())
	defer cl.Close()
	require.NoError(t, cl.Login(username, password).Wait())

	const count = 30
	for i := 0; i < count; i++ {
		appendMsg(t, cl, "INBOX", utils.CRLF("Subject: msg\n\nbody\n"))
	}

	_, err := cl.Select("INBOX", nil).Wait()
	require.NoError(t, err)

	msgs, err := cl.Fetch(imap.SeqSet{{Start: 1, Stop: 0}}, &imap.FetchOptions{UID: true}).Collect()
	require.NoError(t, err)
	require.Len(t, msgs, count)

	for i, m := range msgs {
		require.Equal(t, uint32(i+1), m.SeqNum, "response #%d", i)
		require.Equal(t, imap.UID(i+1), m.UID, "response #%d", i)
	}
}
