package managed

import (
	"context"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/foxcpp/maddy-storage/tests/imap2/utils"
	"github.com/stretchr/testify/require"
)

const testMessage = "From: sender@example.org\r\nTo: receiver@example.org\r\nSubject: managed subject\r\n\r\nmanaged body\r\n"

func TestManagedStorage_MessageLifecycle(t *testing.T) {
	t.Parallel()

	srv := utils.TestServer(t)
	storage := New(&srv.Accounts, &srv.Folders, &srv.Message)
	ctx := context.Background()

	acct, err := storage.CreateAccount(ctx, "managed-account", AccountOptions{})
	require.NoError(t, err)
	require.Equal(t, "managed-account", acct.Name)

	accounts, err := storage.ListAccounts(ctx, "aged-acc", 10, 0)
	require.NoError(t, err)
	require.Len(t, accounts, 1)
	require.Equal(t, acct.ID, accounts[0].ID)

	archive, err := storage.CreateFolder(ctx, acct.Name, "Archive", FolderOptions{Role: "Archive"})
	require.NoError(t, err)
	require.Equal(t, "Archive", archive.Path)
	require.Equal(t, "Archive", archive.Role)

	projects, err := storage.CreateFolder(ctx, acct.Name, "Projects", FolderOptions{})
	require.NoError(t, err)

	err = storage.RenameFolder(ctx, acct.Name, projects.Path, "Projects/2026")
	require.Error(t, err)

	projects2026, err := storage.CreateFolder(ctx, acct.Name, "Projects/2026", FolderOptions{})
	require.NoError(t, err)
	require.Equal(t, "Projects/2026", projects2026.Path)

	folders, err := storage.ListFolders(ctx, acct.Name)
	require.NoError(t, err)
	require.True(t, containsFolderPath(folders, "INBOX"))
	require.True(t, containsFolderPath(folders, archive.Path))
	require.True(t, containsFolderPath(folders, projects.Path))
	require.True(t, containsFolderPath(folders, projects2026.Path))

	added, err := storage.AddMessage(ctx, acct.Name, "INBOX", time.Date(2026, 4, 12, 10, 0, 0, 0, time.UTC), strings.NewReader(testMessage))
	require.NoError(t, err)
	require.Equal(t, "managed subject", added.Title)

	messages, err := storage.ListMessages(ctx, acct.Name, "INBOX", 10, 0)
	require.NoError(t, err)
	require.Len(t, messages, 1)
	require.Equal(t, added.ID, messages[0].ID)

	err = storage.AddFlag(ctx, acct.Name, "INBOX", []string{added.ID}, "\\Flagged")
	require.NoError(t, err)

	messages, err = storage.ListMessages(ctx, acct.Name, "INBOX", 10, 0)
	require.NoError(t, err)
	require.Contains(t, messages[0].Flags, "\\Flagged")

	dump, err := storage.DumpMessage(ctx, acct.Name, "INBOX", added.ID)
	require.NoError(t, err)
	dumped, err := io.ReadAll(dump)
	require.NoError(t, err)
	require.NoError(t, dump.Close())
	require.Contains(t, string(dumped), "Subject: managed subject\r\n")
	require.Contains(t, string(dumped), "managed body\r\n")

	copied, err := storage.CopyMessages(ctx, acct.Name, "INBOX", archive.Path, []string{added.ID})
	require.NoError(t, err)
	require.Len(t, copied, 1)
	require.NotEqual(t, added.ID, copied[0].ID)

	moved, err := storage.MoveMessages(ctx, acct.Name, "INBOX", archive.Path, []string{added.ID})
	require.NoError(t, err)
	require.Len(t, moved, 1)
	require.Equal(t, added.ID, moved[0].ID)

	inboxMessages, err := storage.ListMessages(ctx, acct.Name, "INBOX", 10, 0)
	require.NoError(t, err)
	require.Empty(t, inboxMessages)

	archiveMessages, err := storage.ListMessages(ctx, acct.Name, archive.Path, 10, 0)
	require.NoError(t, err)
	require.Len(t, archiveMessages, 2)
	require.True(t, containsMessageID(archiveMessages, added.ID))
	require.True(t, containsMessageID(archiveMessages, copied[0].ID))

	err = storage.RemoveFlag(ctx, acct.Name, archive.Path, []string{added.ID}, "\\Flagged")
	require.NoError(t, err)

	err = storage.DeleteMessages(ctx, acct.Name, archive.Path, []string{copied[0].ID})
	require.NoError(t, err)

	archiveMessages, err = storage.ListMessages(ctx, acct.Name, archive.Path, 10, 0)
	require.NoError(t, err)
	require.Len(t, archiveMessages, 1)
	require.Equal(t, added.ID, archiveMessages[0].ID)
	require.NotContains(t, archiveMessages[0].Flags, "\\Flagged")

	err = storage.DeleteFolder(ctx, acct.Name, projects.Path)
	require.NoError(t, err)

	folders, err = storage.ListFolders(ctx, acct.Name)
	require.NoError(t, err)
	require.False(t, containsFolderPath(folders, projects.Path))
	require.False(t, containsFolderPath(folders, projects2026.Path))

	err = storage.DeleteAccount(ctx, acct.Name)
	require.NoError(t, err)
}

func containsFolderPath(folders []FolderDTO, path string) bool {
	for _, folder := range folders {
		if folder.Path == path {
			return true
		}
	}
	return false
}

func containsMessageID(messages []MessageDTO, id string) bool {
	for _, msg := range messages {
		if msg.ID == id {
			return true
		}
	}
	return false
}
