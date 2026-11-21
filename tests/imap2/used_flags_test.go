package imap2

import (
	"io"
	"testing"

	"github.com/emersion/go-imap/v2"
	"github.com/foxcpp/maddy-storage/tests/imap2/utils"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAppendUpdatesUsedFlags(t *testing.T) {
	t.Skip("TODO: not updated correctly")

	s := utils.TestServer(t)
	s.Run()
	defer s.Close()

	username, password := s.Account()

	recorder := utils.NewUnilateralRecorder()
	cl := s.Client(recorder)
	defer cl.Close()
	cl.Login(username, password)

	selectData, err := cl.Select("INBOX", &imap.SelectOptions{}).Wait()
	require.NoError(t, err)

	assert.Contains(t, selectData.Flags, imap.FlagSeen)
	assert.Contains(t, selectData.Flags, imap.FlagFlagged)

	aCmd := cl.Append("INBOX", int64(len(testMessage1)), &imap.AppendOptions{
		Flags: []imap.Flag{imap.FlagSeen, imap.FlagFlagged, "customflag"},
	})
	_, err = io.WriteString(aCmd, testMessage1)
	require.NoError(t, err)
	require.NoError(t, aCmd.Close())
	_, err = aCmd.Wait()
	require.NoError(t, err)

	mboxUpdate := recorder.PopMailbox()
	require.GreaterOrEqual(t, len(mboxUpdate), 1)

	hadFlagUpdate := false
	for _, upd := range mboxUpdate {
		t.Logf("mboxUpdate: %#v", upd)

		if upd.Flags != nil {
			assert.Contains(t, mboxUpdate[0].Flags, imap.FlagSeen)
			assert.Contains(t, mboxUpdate[0].Flags, imap.FlagFlagged)
			assert.Contains(t, mboxUpdate[0].Flags, imap.Flag("customflag"))
			hadFlagUpdate = true
		}
	}
	if !hadFlagUpdate {
		t.Error("Flags not updated")
	}

	require.NoError(t, cl.Unselect().Wait())

	selectData, err = cl.Select("INBOX", &imap.SelectOptions{}).Wait()
	require.NoError(t, err)

	assert.Contains(t, selectData.Flags, imap.FlagSeen)
	assert.Contains(t, selectData.Flags, imap.FlagFlagged)
	assert.Contains(t, selectData.Flags, imap.Flag("customflag"))
}

func TestSelfStoreUpdatesUsedFlags(t *testing.T) {
	t.Skip("TODO: not updated correctly")

	s := utils.TestServer(t)
	s.Run()
	defer s.Close()

	username, password := s.Account()

	recorder := utils.NewUnilateralRecorder()
	cl := s.Client(recorder)
	defer cl.Close()
	cl.Login(username, password)

	aCmd := cl.Append("INBOX", int64(len(testMessage1)), &imap.AppendOptions{
		Flags: []imap.Flag{imap.FlagSeen, imap.FlagFlagged},
	})
	_, err := io.WriteString(aCmd, testMessage1)
	require.NoError(t, err)
	require.NoError(t, aCmd.Close())
	appendRes, err := aCmd.Wait()
	require.NoError(t, err)

	selectData, err := cl.Select("INBOX", &imap.SelectOptions{}).Wait()
	require.NoError(t, err)

	assert.Contains(t, selectData.Flags, imap.FlagSeen)
	assert.Contains(t, selectData.Flags, imap.FlagFlagged)
	assert.NotContains(t, selectData.Flags, imap.Flag("customflag"))

	sCmd := cl.Store(imap.UIDSetNum(appendRes.UID), &imap.StoreFlags{
		Flags: []imap.Flag{"customflag"},
		Op:    imap.StoreFlagsAdd,
	}, &imap.StoreOptions{})
	fetchData, err := sCmd.Collect()
	require.NoError(t, err)

	require.Len(t, fetchData, 1)
	assert.ElementsMatch(t, fetchData[0].Flags, []imap.Flag{imap.FlagSeen, imap.FlagFlagged, "customflag"})

	mboxUpdate := recorder.PopMailbox()
	require.GreaterOrEqual(t, len(mboxUpdate), 1)

	hadFlagUpdate := false
	for _, upd := range mboxUpdate {
		t.Logf("mboxUpdate: %#v", upd)

		if upd.Flags != nil {
			assert.Contains(t, mboxUpdate[0].Flags, imap.FlagSeen)
			assert.Contains(t, mboxUpdate[0].Flags, imap.FlagFlagged)
			assert.Contains(t, mboxUpdate[0].Flags, imap.Flag("customflag"))
			hadFlagUpdate = true
		}
	}
	if !hadFlagUpdate {
		t.Error("Flags not updated")
	}

	require.NoError(t, cl.Unselect().Wait())

	selectData, err = cl.Select("INBOX", &imap.SelectOptions{}).Wait()
	require.NoError(t, err)

	assert.Contains(t, selectData.Flags, imap.FlagSeen)
	assert.Contains(t, selectData.Flags, imap.FlagFlagged)
	assert.Contains(t, selectData.Flags, imap.Flag("customflag"))
}

func TestParallelStoreUpdatesUsedFlags(t *testing.T) {
	s := utils.TestServer(t)
	s.Run()
	defer s.Close()

	username, password := s.Account()

	recorder := utils.NewUnilateralRecorder()
	cl := s.Client(recorder)
	defer cl.Close()
	cl.Login(username, password)

	recorder2 := utils.NewUnilateralRecorder()
	cl2 := s.Client(recorder2)
	defer cl2.Close()
	cl2.Login(username, password)

	aCmd := cl.Append("INBOX", int64(len(testMessage1)), &imap.AppendOptions{
		Flags: []imap.Flag{imap.FlagSeen, imap.FlagFlagged},
	})
	_, err := io.WriteString(aCmd, testMessage1)
	require.NoError(t, err)
	require.NoError(t, aCmd.Close())
	appendRes, err := aCmd.Wait()
	require.NoError(t, err)

	selectData, err := cl.Select("INBOX", &imap.SelectOptions{}).Wait()
	require.NoError(t, err)

	_, err = cl2.Select("INBOX", &imap.SelectOptions{}).Wait()
	require.NoError(t, err)

	assert.Contains(t, selectData.Flags, imap.FlagSeen)
	assert.Contains(t, selectData.Flags, imap.FlagFlagged)
	assert.NotContains(t, selectData.Flags, imap.Flag("customflag"))

	sCmd := cl2.Store(imap.UIDSetNum(appendRes.UID), &imap.StoreFlags{
		Flags: []imap.Flag{"customflag"},
		Op:    imap.StoreFlagsAdd,
	}, &imap.StoreOptions{})
	fetchData, err := sCmd.Collect()
	require.NoError(t, err)

	require.Len(t, fetchData, 1)
	assert.ElementsMatch(t, fetchData[0].Flags, []imap.Flag{imap.FlagSeen, imap.FlagFlagged, "customflag"})

	require.NoError(t, cl.Noop().Wait())

	mboxUpdate := recorder.PopMailbox()
	require.GreaterOrEqual(t, len(mboxUpdate), 1)

	hadFlagUpdate := false
	for _, upd := range mboxUpdate {
		t.Logf("mboxUpdate: %#v", upd)

		if upd.Flags != nil {
			assert.Contains(t, mboxUpdate[0].Flags, imap.FlagSeen)
			assert.Contains(t, mboxUpdate[0].Flags, imap.FlagFlagged)
			assert.Contains(t, mboxUpdate[0].Flags, imap.Flag("customflag"))
			hadFlagUpdate = true
		}
	}
	if !hadFlagUpdate {
		t.Error("Flags not updated")
	}

	require.NoError(t, cl.Unselect().Wait())

	selectData, err = cl.Select("INBOX", &imap.SelectOptions{}).Wait()
	require.NoError(t, err)

	assert.Contains(t, selectData.Flags, imap.FlagSeen)
	assert.Contains(t, selectData.Flags, imap.FlagFlagged)
	assert.Contains(t, selectData.Flags, imap.Flag("customflag"))
}
