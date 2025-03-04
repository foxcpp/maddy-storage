package imap2

import (
	"io"
	"testing"

	"github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapclient"
	"github.com/foxcpp/maddy-storage/tests/imap2/utils"
	"github.com/stretchr/testify/require"
)

// Copy of "append" from imaptest.

var testMessage1 = utils.CRLF(`From: user1@domain.org
Date: Sat, 24 Mar 2007 23:00:00 +0200
Subject: s1

body1
`)

var testMessage2 = utils.CRLF(`From: user2@domain.org
Date: Sat, 24 Mar 2007 23:00:00 +0200
Subject: s22

body22
`)

var testMessage3 = utils.CRLF(`From: user3@domain.org
Date: Sat, 24 Mar 2007 23:00:00 +0200
Subject: s333

body33
`)

func uint32Ptr(i uint32) *uint32 {
	return &i
}

func TestImaptestAppendFetch(t *testing.T) {
	s := utils.TestServer(t)
	s.Run()
	defer s.Close()

	username, password := s.Account()

	recorder1 := utils.NewUnilateralRecorder()
	cl1 := s.Client(recorder1)
	defer cl1.Close()
	cl1.Login(username, password)

	recorder2 := utils.NewUnilateralRecorder()
	cl2 := s.Client(recorder2)
	defer cl2.Close()
	require.NoError(t, cl2.Login(username, password).Wait())

	recorder3 := utils.NewUnilateralRecorder()
	cl3 := s.Client(recorder3)
	defer cl3.Close()
	require.NoError(t, cl3.Login(username, password).Wait())

	_, err := cl1.Select("INBOX", &imap.SelectOptions{}).Wait()
	require.NoError(t, err)

	_, err = cl2.Select("INBOX", &imap.SelectOptions{}).Wait()
	require.NoError(t, err)

	t.Run("message 1", func(t *testing.T) {
		aCmd := cl1.Append("INBOX", int64(len(testMessage1)), &imap.AppendOptions{
			Flags: []imap.Flag{imap.FlagSeen, imap.FlagFlagged},
		})
		_, err = io.WriteString(aCmd, testMessage1)
		require.NoError(t, err)
		require.NoError(t, aCmd.Close())
		_, err := aCmd.Wait()
		require.NoError(t, err)

		require.Equal(t, []*imapclient.UnilateralDataMailbox{
			{NumMessages: uint32Ptr(1)},
		}, recorder1.PopMailbox())

		require.NoError(t, cl2.Noop().Wait())
		require.Equal(t, []*imapclient.UnilateralDataMailbox{
			{NumMessages: uint32Ptr(1)},
		}, recorder2.PopMailbox())

		status3, err := cl3.Status("INBOX", &imap.StatusOptions{
			NumMessages: true,
			NumUnseen:   true,
			//NumRecent: true,
		}).Wait()
		require.NoError(t, err)
		require.Equal(t, &imap.StatusData{
			Mailbox:     "INBOX",
			NumMessages: uint32Ptr(1),
			NumUnseen:   uint32Ptr(0),
			//NumRecent:   uint32Ptr(0),
		}, status3)

		fetch1, err := cl1.Fetch(imap.SeqSetNum(1), &imap.FetchOptions{
			UID:   true,
			Flags: true,
		}).Collect()
		require.NoError(t, err)
		require.Equal(t, []*imapclient.FetchMessageBuffer{
			{SeqNum: 1, UID: imap.UID(1), Flags: []imap.Flag{imap.FlagFlagged, imap.FlagSeen, `\Recent`}},
		}, fetch1)

		fetch2, err := cl2.Fetch(imap.SeqSetNum(1), &imap.FetchOptions{
			UID:   true,
			Flags: true,
		}).Collect()
		require.NoError(t, err)
		require.Equal(t, []*imapclient.FetchMessageBuffer{
			{SeqNum: 1, UID: imap.UID(1), Flags: []imap.Flag{imap.FlagFlagged, imap.FlagSeen}},
		}, fetch2)
	})

	t.Run("message 2", func(t *testing.T) {
		aCmd := cl2.Append("INBOX", int64(len(testMessage2)), &imap.AppendOptions{})
		_, err = io.WriteString(aCmd, testMessage2)
		require.NoError(t, err)
		require.NoError(t, aCmd.Close())
		_, err := aCmd.Wait()
		require.NoError(t, err)

		require.Equal(t, []*imapclient.UnilateralDataMailbox{
			{NumMessages: uint32Ptr(2)},
		}, recorder2.PopMailbox())

		require.NoError(t, cl1.Noop().Wait())
		require.Equal(t, []*imapclient.UnilateralDataMailbox{
			{NumMessages: uint32Ptr(2)},
		}, recorder1.PopMailbox())

		status3, err := cl3.Status("INBOX", &imap.StatusOptions{
			NumMessages: true,
			NumUnseen:   true,
		}).Wait()
		require.NoError(t, err)
		require.Equal(t, &imap.StatusData{
			Mailbox:     "INBOX",
			NumMessages: uint32Ptr(2),
			NumUnseen:   uint32Ptr(1),
		}, status3)

		fetch1, err := cl1.Fetch(imap.SeqSetNum(2), &imap.FetchOptions{
			UID:   true,
			Flags: true,
		}).Collect()
		require.NoError(t, err)
		require.Equal(t, []*imapclient.FetchMessageBuffer{
			{SeqNum: 2, UID: imap.UID(2), Flags: nil},
		}, fetch1)

		fetch2, err := cl2.Fetch(imap.SeqSetNum(2), &imap.FetchOptions{
			UID:   true,
			Flags: true,
		}).Collect()
		require.NoError(t, err)
		require.Equal(t, []*imapclient.FetchMessageBuffer{
			{SeqNum: 2, UID: imap.UID(2), Flags: []imap.Flag{`\Recent`}},
		}, fetch2)
	})

	t.Run("message 3", func(t *testing.T) {
		aCmd := cl3.Append("INBOX", int64(len(testMessage3)), &imap.AppendOptions{})
		_, err = io.WriteString(aCmd, testMessage3)
		require.NoError(t, err)
		require.NoError(t, aCmd.Close())
		_, err := aCmd.Wait()
		require.NoError(t, err)

		require.Equal(t, []*imapclient.UnilateralDataMailbox{}, recorder3.PopMailbox())

		status3, err := cl3.Status("INBOX", &imap.StatusOptions{
			NumMessages: true,
			NumUnseen:   true,
		}).Wait()
		require.NoError(t, err)
		require.Equal(t, &imap.StatusData{
			Mailbox:     "INBOX",
			NumMessages: uint32Ptr(3),
			NumUnseen:   uint32Ptr(2),
		}, status3)

		require.NoError(t, cl2.Noop().Wait())
		require.Equal(t, []*imapclient.UnilateralDataMailbox{
			{NumMessages: uint32Ptr(3)},
		}, recorder2.PopMailbox())

		require.NoError(t, cl1.Noop().Wait())
		require.Equal(t, []*imapclient.UnilateralDataMailbox{
			{NumMessages: uint32Ptr(3)},
		}, recorder1.PopMailbox())

		fetch1, err := cl1.Fetch(imap.SeqSetNum(3), &imap.FetchOptions{
			UID:   true,
			Flags: true,
		}).Collect()
		require.NoError(t, err)
		require.Equal(t, []*imapclient.FetchMessageBuffer{
			{SeqNum: 3, UID: imap.UID(3), Flags: nil},
		}, fetch1)

		fetch2, err := cl2.Fetch(imap.SeqSetNum(3), &imap.FetchOptions{
			UID:   true,
			Flags: true,
		}).Collect()
		require.NoError(t, err)
		require.Equal(t, []*imapclient.FetchMessageBuffer{
			{SeqNum: 3, UID: imap.UID(3), Flags: []imap.Flag{`\Recent`}},
		}, fetch2)
	})
}
