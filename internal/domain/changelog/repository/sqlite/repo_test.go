package changelogsqlite_test

import (
	"context"
	"testing"
	"time"

	"github.com/foxcpp/maddy-storage/internal/domain/changelog"
	changelogsqlite "github.com/foxcpp/maddy-storage/internal/domain/changelog/repository/sqlite"
	"github.com/foxcpp/maddy-storage/internal/domain/metadata"
	"github.com/foxcpp/maddy-storage/internal/pkg/contextlib"
	"github.com/foxcpp/maddy-storage/internal/repository/sqlite"
	"github.com/oklog/ulid/v2"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap/zaptest"
)

type fixture struct {
	t    *testing.T
	ctx  context.Context
	repo changelog.Repo
}

func newFixture(t *testing.T) *fixture {
	t.Helper()

	ctx := contextlib.WithLogger(context.Background(), zaptest.NewLogger(t))

	db, err := sqlite.NewMemory(sqlite.Cfg{})
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	return &fixture{t: t, ctx: ctx, repo: changelogsqlite.New(db)}
}

// at returns a deterministic timestamp, n seconds after a fixed base.
func at(n int) time.Time {
	return time.Date(2026, 1, 1, 0, 0, n, 0, time.UTC)
}

func (f *fixture) create(entries ...changelog.Entry) {
	f.t.Helper()
	require.NoError(f.t, f.repo.Create(f.ctx, entries...))
}

func msgEntry(acct, fld, msg ulid.ULID, t time.Time, uid uint32) changelog.Entry {
	e := changelog.NewMessage(changelog.TypeMessageCreated, acct, fld, msg, &changelog.MessageEntry{UID: uid})
	e.At = t
	return *e
}

func ats(entries []changelog.Entry) []time.Time {
	res := make([]time.Time, len(entries))
	for i, e := range entries {
		res[i] = e.At.UTC()
	}
	return res
}

func TestCreate_RoundtripsAccountEntry(t *testing.T) {
	f := newFixture(t)
	acct := ulid.Make()

	e := changelog.NewAccount(changelog.TypeAccountCreated, acct, &changelog.AccountEntry{})
	e.At = at(1)
	f.create(*e)

	got, err := f.repo.GetAccountChanges(f.ctx, acct, time.Time{}, 0)
	require.NoError(t, err)
	require.Len(t, got, 1)
	require.True(t, at(1).Equal(got[0].At))
	require.Equal(t, changelog.Type(changelog.TypeAccountCreated), got[0].Type)
	require.Equal(t, acct, got[0].AccountID)
	require.Equal(t, ulid.ULID{}, got[0].FolderID)
	require.Equal(t, ulid.ULID{}, got[0].MessageID)
	require.Equal(t, &changelog.AccountEntry{}, got[0].Account)
	// changelog.NewAccount leaves Metadata nil, it must still be usable.
	require.Equal(t, metadata.New(), got[0].Metadata)
	require.Nil(t, got[0].Folder)
	require.Nil(t, got[0].Message)
}

func TestCreate_RoundtripsFolderEntry(t *testing.T) {
	f := newFixture(t)
	acct, fld := ulid.Make(), ulid.Make()

	e := changelog.NewFolder(changelog.TypeFolderRenamed, acct, fld, &changelog.FolderEntry{OldName: "a", NewName: "b"})
	e.At = at(1)
	f.create(*e)

	got, err := f.repo.GetFolderChanges(f.ctx, fld, time.Time{}, 0)
	require.NoError(t, err)
	require.Len(t, got, 1)
	require.Equal(t, changelog.Type(changelog.TypeFolderRenamed), got[0].Type)
	require.Equal(t, acct, got[0].AccountID)
	require.Equal(t, fld, got[0].FolderID)
	require.Equal(t, ulid.ULID{}, got[0].MessageID)
	require.Equal(t, &changelog.FolderEntry{OldName: "a", NewName: "b"}, got[0].Folder)
	require.Nil(t, got[0].Account)
	require.Nil(t, got[0].Message)
}

func TestCreate_RoundtripsMessageEntryAndMetadata(t *testing.T) {
	f := newFixture(t)
	acct, fld, msg := ulid.Make(), ulid.Make(), ulid.Make()

	e := msgEntry(acct, fld, msg, at(1), 42)
	e.Message.Flags = []string{`\Seen`, "custom"}
	e.Metadata = metadata.Md{"k": "v"}
	f.create(e)

	got, err := f.repo.GetMessageChanges(f.ctx, msg, time.Time{}, 0)
	require.NoError(t, err)
	require.Len(t, got, 1)
	require.Equal(t, acct, got[0].AccountID)
	require.Equal(t, fld, got[0].FolderID)
	require.Equal(t, msg, got[0].MessageID)
	require.Equal(t, &changelog.MessageEntry{UID: 42, Flags: []string{`\Seen`, "custom"}}, got[0].Message)
	require.Equal(t, metadata.Md{"k": "v"}, got[0].Metadata)
}

func TestCreate_NoEntriesIsNoop(t *testing.T) {
	f := newFixture(t)

	require.NoError(t, f.repo.Create(f.ctx))
}

func TestCreate_EntriesDoNotRequireExistingRows(t *testing.T) {
	// The log must be able to describe deleted accounts, so there are no
	// foreign keys. IDs here do not exist in any other table.
	f := newFixture(t)
	acct := ulid.Make()

	e := changelog.NewAccount(changelog.TypeAccountDeleted, acct, &changelog.AccountEntry{})
	e.At = at(1)

	require.NoError(t, f.repo.Create(f.ctx, *e))
}

func TestGetChanges_ScopedToID(t *testing.T) {
	f := newFixture(t)
	acctA, acctB := ulid.Make(), ulid.Make()
	fldA, fldB := ulid.Make(), ulid.Make()
	msgA, msgB := ulid.Make(), ulid.Make()
	f.create(msgEntry(acctA, fldA, msgA, at(1), 1), msgEntry(acctB, fldB, msgB, at(2), 2))

	byAcct, err := f.repo.GetAccountChanges(f.ctx, acctA, time.Time{}, 0)
	require.NoError(t, err)
	require.Len(t, byAcct, 1)
	require.Equal(t, uint32(1), byAcct[0].Message.UID)

	byFolder, err := f.repo.GetFolderChanges(f.ctx, fldB, time.Time{}, 0)
	require.NoError(t, err)
	require.Len(t, byFolder, 1)
	require.Equal(t, uint32(2), byFolder[0].Message.UID)

	byMsg, err := f.repo.GetMessageChanges(f.ctx, msgA, time.Time{}, 0)
	require.NoError(t, err)
	require.Len(t, byMsg, 1)
	require.Equal(t, uint32(1), byMsg[0].Message.UID)
}

func TestGetChanges_NoMatchesIsEmpty(t *testing.T) {
	f := newFixture(t)

	got, err := f.repo.GetAccountChanges(f.ctx, ulid.Make(), time.Time{}, 0)
	require.NoError(t, err)
	require.Empty(t, got)
}

func TestGetChanges_AtGtIsExclusive(t *testing.T) {
	f := newFixture(t)
	acct, fld, msg := ulid.Make(), ulid.Make(), ulid.Make()
	f.create(msgEntry(acct, fld, msg, at(1), 1), msgEntry(acct, fld, msg, at(2), 2), msgEntry(acct, fld, msg, at(3), 3))

	for name, get := range map[string]func() ([]changelog.Entry, error){
		"account": func() ([]changelog.Entry, error) { return f.repo.GetAccountChanges(f.ctx, acct, at(2), 0) },
		"folder":  func() ([]changelog.Entry, error) { return f.repo.GetFolderChanges(f.ctx, fld, at(2), 0) },
		"message": func() ([]changelog.Entry, error) { return f.repo.GetMessageChanges(f.ctx, msg, at(2), 0) },
	} {
		t.Run(name, func(t *testing.T) {
			got, err := get()
			require.NoError(t, err)
			require.Equal(t, []time.Time{at(3)}, ats(got))
		})
	}
}

func TestGetChanges_OrderedByTimeAscending(t *testing.T) {
	f := newFixture(t)
	acct, fld, msg := ulid.Make(), ulid.Make(), ulid.Make()
	f.create(msgEntry(acct, fld, msg, at(3), 3), msgEntry(acct, fld, msg, at(1), 1), msgEntry(acct, fld, msg, at(2), 2))

	got, err := f.repo.GetAccountChanges(f.ctx, acct, time.Time{}, 0)
	require.NoError(t, err)
	require.Equal(t, []time.Time{at(1), at(2), at(3)}, ats(got))
}

func TestGetChanges_LimitKeepsEarliest(t *testing.T) {
	f := newFixture(t)
	acct, fld, msg := ulid.Make(), ulid.Make(), ulid.Make()
	f.create(msgEntry(acct, fld, msg, at(3), 3), msgEntry(acct, fld, msg, at(1), 1), msgEntry(acct, fld, msg, at(2), 2))

	for name, get := range map[string]func() ([]changelog.Entry, error){
		"account": func() ([]changelog.Entry, error) { return f.repo.GetAccountChanges(f.ctx, acct, time.Time{}, 2) },
		"folder":  func() ([]changelog.Entry, error) { return f.repo.GetFolderChanges(f.ctx, fld, time.Time{}, 2) },
		"message": func() ([]changelog.Entry, error) { return f.repo.GetMessageChanges(f.ctx, msg, time.Time{}, 2) },
	} {
		t.Run(name, func(t *testing.T) {
			got, err := get()
			require.NoError(t, err)
			require.Equal(t, []time.Time{at(1), at(2)}, ats(got))
		})
	}
}

func TestLastTime_ReturnsLatestEntry(t *testing.T) {
	f := newFixture(t)
	acct, fld, msg := ulid.Make(), ulid.Make(), ulid.Make()
	f.create(msgEntry(acct, fld, msg, at(2), 2), msgEntry(acct, fld, msg, at(5), 5), msgEntry(acct, fld, msg, at(3), 3))

	a, err := f.repo.LastAccountTime(f.ctx, acct)
	require.NoError(t, err)
	require.True(t, at(5).Equal(a), "got %v", a)

	fl, err := f.repo.LastFolderTime(f.ctx, fld)
	require.NoError(t, err)
	require.True(t, at(5).Equal(fl), "got %v", fl)

	m, err := f.repo.LastMessageTime(f.ctx, msg)
	require.NoError(t, err)
	require.True(t, at(5).Equal(m), "got %v", m)
}

func TestLastTime_ScopedToID(t *testing.T) {
	f := newFixture(t)
	acct, fld, msg := ulid.Make(), ulid.Make(), ulid.Make()
	f.create(msgEntry(acct, fld, msg, at(2), 2), msgEntry(ulid.Make(), ulid.Make(), ulid.Make(), at(9), 9))

	m, err := f.repo.LastMessageTime(f.ctx, msg)
	require.NoError(t, err)
	require.True(t, at(2).Equal(m), "got %v", m)
}

func TestLastTime_NoEntriesIsZero(t *testing.T) {
	f := newFixture(t)

	a, err := f.repo.LastAccountTime(f.ctx, ulid.Make())
	require.NoError(t, err)
	require.True(t, a.IsZero())

	fl, err := f.repo.LastFolderTime(f.ctx, ulid.Make())
	require.NoError(t, err)
	require.True(t, fl.IsZero())

	m, err := f.repo.LastMessageTime(f.ctx, ulid.Make())
	require.NoError(t, err)
	require.True(t, m.IsZero())
}
