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
