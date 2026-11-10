package messagesqlite_test

import (
	"context"
	"slices"
	"testing"
	"time"

	"github.com/foxcpp/maddy-storage/internal/domain/folder"
	"github.com/foxcpp/maddy-storage/internal/domain/message"
	messagesqlite "github.com/foxcpp/maddy-storage/internal/domain/message/repository/sqlite"
	"github.com/foxcpp/maddy-storage/internal/domain/metadata"
	"github.com/foxcpp/maddy-storage/internal/pkg/contextlib"
	"github.com/foxcpp/maddy-storage/internal/repository/sqlite"
	"github.com/foxcpp/maddy-storage/internal/repository/sqlite/jetdb/model"
	"github.com/foxcpp/maddy-storage/internal/repository/sqlite/jetdb/table"
	"github.com/oklog/ulid/v2"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap/zaptest"
)

type fixture struct {
	t    *testing.T
	ctx  context.Context
	db   sqlite.DB
	repo message.Repo
}

func newFixture(t *testing.T) *fixture {
	t.Helper()

	ctx := contextlib.WithLogger(context.Background(), zaptest.NewLogger(t))

	db, err := sqlite.NewMemory(sqlite.Cfg{})
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	return &fixture{t: t, ctx: ctx, db: db, repo: messagesqlite.New(db)}
}

type msgOpt func(*message.Msg)

func withModSeq(ms folder.ModSeq) msgOpt {
	return func(m *message.Msg) { m.CreatedAtModSeq, m.ModSeq = ms, ms }
}

func withFlags(flags ...string) msgOpt {
	return func(m *message.Msg) { m.Flags = flags }
}

func withParts(parts ...message.Part) msgOpt {
	return func(m *message.Msg) { m.Parts = parts }
}

func inlinePart(order int, path message.Path, body string) message.Part {
	return message.Part{
		ID:     ulid.Make(),
		Order:  order,
		Path:   path,
		Inline: []byte(body),
		Content: &message.ContentPartData{
			Type:        "text/plain",
			ContentSize: uint32(len(body)),
		},
	}
}

func externalPart(order int, path message.Path, extID string) message.Part {
	return message.Part{
		ID:             ulid.Make(),
		Order:          order,
		Path:           path,
		ExternalBlobID: extID,
		Content: &message.ContentPartData{
			Type:        "application/octet-stream",
			ContentSize: 100,
		},
	}
}

// newMsg builds a message, it is NOT stored.
func newMsg(opts ...msgOpt) message.Msg {
	now := time.Now().UTC()
	m := message.Msg{
		ID:              ulid.Make(),
		ReceivedAt:      now.Add(-time.Hour),
		CreatedAtModSeq: 1,
		ModSeq:          1,
		CreatedAt:       now,
		UpdatedAt:       now,
		Meta:            metadata.Md{"k": "v"},
		TotalSize:       5,
		Content: &message.ContentData{Envelope: &message.ContentEnvelope{
			Date:    now.Add(-2 * time.Hour),
			Subject: "hello",
		}},
		Parts: []message.Part{inlinePart(0, message.Path{1}, "hello")},
	}
	for _, o := range opts {
		o(&m)
	}
	return m
}

func (f *fixture) store(opts ...msgOpt) message.Msg {
	f.t.Helper()

	m := newMsg(opts...)
	require.NoError(f.t, f.repo.Create(f.ctx, m))

	return m
}

func (f *fixture) get(id ulid.ULID) *message.Msg {
	f.t.Helper()

	m, err := f.repo.GetByID(f.ctx, id)
	require.NoError(f.t, err)

	return m
}

func (f *fixture) flagsOf(id ulid.ULID) []string {
	f.t.Helper()

	return sorted(f.get(id).Flags)
}

func ids(msgs []message.Msg) []ulid.ULID {
	res := make([]ulid.ULID, len(msgs))
	for i, m := range msgs {
		res[i] = m.ID
	}
	return res
}

func sorted(s []string) []string {
	s = append([]string{}, s...) // non-nil for nil input
	slices.Sort(s)
	return s
}

func requireSameMsg(t *testing.T, want message.Msg, got *message.Msg) {
	t.Helper()

	require.Equal(t, want.ID, got.ID)
	require.WithinDuration(t, want.ReceivedAt, got.ReceivedAt, 0)
	require.WithinDuration(t, want.CreatedAt, got.CreatedAt, 0)
	require.WithinDuration(t, want.UpdatedAt, got.UpdatedAt, 0)
	require.Equal(t, want.CreatedAtModSeq, got.CreatedAtModSeq)
	require.Equal(t, want.ModSeq, got.ModSeq)
	require.Equal(t, want.TotalSize, got.TotalSize)
	require.Equal(t, want.Meta, got.Meta)
	require.Equal(t, sorted(want.Flags), sorted(got.Flags))
	require.Equal(t, want.Content.Envelope.Subject, got.Content.Envelope.Subject)
	require.WithinDuration(t, want.Content.Envelope.Date, got.Content.Envelope.Date, 0)

	require.Len(t, got.Parts, len(want.Parts))
	for i, wp := range want.Parts {
		gp := got.Parts[i]
		require.Equal(t, wp.ID, gp.ID)
		require.Equal(t, wp.Order, gp.Order)
		require.Equal(t, wp.Path, gp.Path)
		require.Equal(t, wp.Inline, gp.Inline)
		require.Equal(t, wp.ExternalBlobID, gp.ExternalBlobID)
		require.Equal(t, wp.Content, gp.Content)
	}
}

func TestMessage_CreateGetByID(t *testing.T) {
	f := newFixture(t)

	want := f.store(
		withFlags(`\Seen`, `\Flagged`),
		withParts(
			inlinePart(0, message.Path{1}, "hello"),
			externalPart(1, message.Path{2}, "ext-1"),
		),
	)

	got := f.get(want.ID)

	requireSameMsg(t, want, got)
}

func TestMessage_CreateNoFlags(t *testing.T) {
	f := newFixture(t)

	want := f.store()

	got := f.get(want.ID)
	require.Empty(t, got.Flags)
	requireSameMsg(t, want, got)
}

func TestMessage_CreateMany(t *testing.T) {
	f := newFixture(t)

	a, b := newMsg(withFlags("a")), newMsg(withFlags("b"))
	require.NoError(t, f.repo.Create(f.ctx, a, b))

	requireSameMsg(t, a, f.get(a.ID))
	requireSameMsg(t, b, f.get(b.ID))
}

func TestMessage_CreateDuplicateRollsBackBatch(t *testing.T) {
	f := newFixture(t)

	existing := f.store()
	fresh := newMsg()

	err := f.repo.Create(f.ctx, fresh, existing)
	require.Error(t, err)

	_, err = f.repo.GetByID(f.ctx, fresh.ID)
	require.Error(t, err, "batch must be atomic")
}

func TestMessage_GetByIDNotFound(t *testing.T) {
	f := newFixture(t)

	_, err := f.repo.GetByID(f.ctx, ulid.Make())
	require.ErrorIs(t, err, message.ErrNotFound)
}

func TestMessage_GetByIDs(t *testing.T) {
	f := newFixture(t)

	a := f.store(withModSeq(1), withFlags("a"))
	b := f.store(withModSeq(5), withFlags("b"), withParts(
		inlinePart(0, message.Path{1}, "x"),
		inlinePart(1, message.Path{2}, "y"),
	))
	c := f.store(withModSeq(9))

	t.Run("PreservesInputOrder", func(t *testing.T) {
		got, err := f.repo.GetByIDs(f.ctx, 0, c.ID, a.ID, b.ID)
		require.NoError(t, err)
		require.Equal(t, []ulid.ULID{c.ID, a.ID, b.ID}, ids(got))

		requireSameMsg(t, b, &got[2])
	})
	t.Run("SkipsMissing", func(t *testing.T) {
		got, err := f.repo.GetByIDs(f.ctx, 0, a.ID, ulid.Make(), b.ID)
		require.NoError(t, err)
		require.Equal(t, []ulid.ULID{a.ID, b.ID}, ids(got))
	})
	t.Run("DuplicateIDsReturnedOnce", func(t *testing.T) {
		got, err := f.repo.GetByIDs(f.ctx, 0, a.ID, a.ID)
		require.NoError(t, err)
		require.Equal(t, []ulid.ULID{a.ID}, ids(got))
	})
	t.Run("ModSeqGt", func(t *testing.T) {
		got, err := f.repo.GetByIDs(f.ctx, 1, a.ID, b.ID, c.ID)
		require.NoError(t, err)
		require.Equal(t, []ulid.ULID{b.ID, c.ID}, ids(got))
	})
	t.Run("NoIDs", func(t *testing.T) {
		got, err := f.repo.GetByIDs(f.ctx, 0)
		require.NoError(t, err)
		require.Empty(t, got)
	})
}

func TestMessage_DeleteByID(t *testing.T) {
	f := newFixture(t)

	a := f.store(withFlags("a"))
	b := f.store()
	keep := f.store(withFlags("keep"))

	require.NoError(t, f.repo.DeleteByID(f.ctx, a.ID, b.ID))

	got, err := f.repo.GetByIDs(f.ctx, 0, a.ID, b.ID, keep.ID)
	require.NoError(t, err)
	require.Equal(t, []ulid.ULID{keep.ID}, ids(got))

	var flags []model.MessageFlags
	require.NoError(t, f.db.Query(f.ctx, table.MessageFlags.SELECT(table.MessageFlags.AllColumns), &flags))
	require.Len(t, flags, 1, "flags of deleted messages must be cascaded")
}

func TestMessage_DeleteByIDNoIDs(t *testing.T) {
	f := newFixture(t)

	m := f.store()

	require.NoError(t, f.repo.DeleteByID(f.ctx))
	f.get(m.ID)
}

func TestMessage_AddFlags(t *testing.T) {
	f := newFixture(t)

	m := f.store(withModSeq(3), withFlags(`\Seen`))

	changed, err := f.repo.AddFlags(f.ctx, []ulid.ULID{m.ID}, []string{`\Flagged`, `\Seen`}, 3, 7)
	require.NoError(t, err)

	require.Len(t, changed, 1)
	require.Equal(t, message.MsgFlags{
		ID:             m.ID,
		Flags:          changed[m.ID].Flags,
		ModSeq:         7,
		PreviousModSeq: 3,
	}, changed[m.ID])
	require.Equal(t, []string{`\Flagged`, `\Seen`}, sorted(changed[m.ID].Flags))

	got := f.get(m.ID)
	require.Equal(t, folder.ModSeq(7), got.ModSeq)
	require.Equal(t, []string{`\Flagged`, `\Seen`}, f.flagsOf(m.ID))
	require.True(t, got.UpdatedAt.After(m.UpdatedAt), "updated_at must be bumped")
}

func TestMessage_AddFlagsAlreadyPresentIsNoChange(t *testing.T) {
	f := newFixture(t)

	m := f.store(withFlags(`\Seen`))

	changed, err := f.repo.AddFlags(f.ctx, []ulid.ULID{m.ID}, []string{`\Seen`}, 1, 2)
	require.NoError(t, err)
	require.Empty(t, changed)
}

func TestMessage_AddFlagsModSeqGuard(t *testing.T) {
	f := newFixture(t)

	old := f.store(withModSeq(2))
	fresh := f.store(withModSeq(9))

	changed, err := f.repo.AddFlags(f.ctx, []ulid.ULID{old.ID, fresh.ID}, []string{"x"}, 5, 10)
	require.NoError(t, err)

	require.Len(t, changed, 1)
	require.Contains(t, changed, old.ID)
	require.Equal(t, []string{"x"}, f.flagsOf(old.ID))
	require.Empty(t, f.flagsOf(fresh.ID))
	require.Equal(t, folder.ModSeq(9), f.get(fresh.ID).ModSeq)
}

func TestMessage_ReplaceFlags(t *testing.T) {
	f := newFixture(t)

	m := f.store(withModSeq(3), withFlags("a", "b"))
	guarded := f.store(withModSeq(9), withFlags("keep"))

	changed, err := f.repo.ReplaceFlags(f.ctx, []ulid.ULID{m.ID, guarded.ID}, []string{"b", "c"}, 5, 10)
	require.NoError(t, err)

	require.Len(t, changed, 1)
	require.Equal(t, message.MsgFlags{
		ID:             m.ID,
		Flags:          []string{"b", "c"},
		ModSeq:         10,
		PreviousModSeq: 3,
	}, changed[m.ID])

	require.Equal(t, []string{"b", "c"}, f.flagsOf(m.ID))
	require.Equal(t, folder.ModSeq(10), f.get(m.ID).ModSeq)
	require.Equal(t, []string{"keep"}, f.flagsOf(guarded.ID))
	require.Equal(t, folder.ModSeq(9), f.get(guarded.ID).ModSeq)
}

func TestMessage_DeleteFlags(t *testing.T) {
	f := newFixture(t)

	m := f.store(withModSeq(3), withFlags("a", "b", "c"))

	changed, err := f.repo.DeleteFlags(f.ctx, []ulid.ULID{m.ID}, []string{"a", "c", "nonexistent"}, 3, 8)
	require.NoError(t, err)

	require.Len(t, changed, 1)
	require.Equal(t, []string{"b"}, changed[m.ID].Flags)
	require.Equal(t, folder.ModSeq(8), changed[m.ID].ModSeq)
	require.Equal(t, folder.ModSeq(3), changed[m.ID].PreviousModSeq)

	require.Equal(t, []string{"b"}, f.flagsOf(m.ID))
	require.Equal(t, folder.ModSeq(8), f.get(m.ID).ModSeq)
}

func TestMessage_DeleteFlagsNothingToDeleteIsNoChange(t *testing.T) {
	f := newFixture(t)

	m := f.store(withFlags("a"))

	changed, err := f.repo.DeleteFlags(f.ctx, []ulid.ULID{m.ID}, []string{"zzz"}, 1, 2)
	require.NoError(t, err)
	require.Empty(t, changed)
}

func TestMessage_DeleteFlagsModSeqGuard(t *testing.T) {
	f := newFixture(t)

	guarded := f.store(withModSeq(9), withFlags("a", "b"))

	changed, err := f.repo.DeleteFlags(f.ctx, []ulid.ULID{guarded.ID}, []string{"a"}, 5, 10)
	require.NoError(t, err)

	require.Empty(t, changed)
	require.Equal(t, []string{"a", "b"}, f.flagsOf(guarded.ID), "flags of a message that failed the modseq check must stay")
	require.Equal(t, folder.ModSeq(9), f.get(guarded.ID).ModSeq)
}

func TestMessage_TouchMsgs(t *testing.T) {
	f := newFixture(t)

	a := f.store(withModSeq(3))
	b := f.store(withModSeq(20))
	other := f.store(withModSeq(3))

	require.NoError(t, f.repo.TouchMsgs(f.ctx, []ulid.ULID{a.ID, b.ID}, 10))

	require.Equal(t, folder.ModSeq(10), f.get(a.ID).ModSeq)
	require.Equal(t, folder.ModSeq(20), f.get(b.ID).ModSeq, "modseq must never go backwards")
	require.Equal(t, folder.ModSeq(3), f.get(other.ID).ModSeq)
	require.True(t, f.get(a.ID).UpdatedAt.After(a.UpdatedAt))
}

func TestMessage_TouchMsgsNoIDs(t *testing.T) {
	f := newFixture(t)

	m := f.store(withModSeq(3))

	require.NoError(t, f.repo.TouchMsgs(f.ctx, nil, 10))
	require.Equal(t, folder.ModSeq(3), f.get(m.ID).ModSeq)
}

func TestMessage_DeletableExternalIDs(t *testing.T) {
	f := newFixture(t)

	shared := f.store(withParts(externalPart(0, message.Path{1}, "shared")))
	f.store(withParts(externalPart(0, message.Path{1}, "shared")))
	solo := f.store(withParts(
		inlinePart(0, message.Path{1}, "x"),
		externalPart(1, message.Path{2}, "solo"),
	))

	got, err := f.repo.DeletableExternalIDs(f.ctx, []string{"shared", "solo", "unknown"})
	require.NoError(t, err)
	require.Empty(t, got, "all blobs are still referenced")

	require.NoError(t, f.repo.DeleteByID(f.ctx, solo.ID))
	require.NoError(t, f.repo.DeleteByID(f.ctx, shared.ID))

	got, err = f.repo.DeletableExternalIDs(f.ctx, []string{"shared", "solo", "unknown"})
	require.NoError(t, err)
	require.Equal(t, []string{"solo"}, got, "shared blob still has a second copy")
}

func TestMessage_DeletableExternalIDsNoIDs(t *testing.T) {
	f := newFixture(t)

	got, err := f.repo.DeletableExternalIDs(f.ctx, nil)
	require.NoError(t, err)
	require.Empty(t, got)
}

// XXX: Old (gorm) implementation filtered by messages.account_id, a column
// that does not exist, so it always failed. A message is considered
// dangling if no folder entry references it.
func TestMessage_DanglingMsgIDs(t *testing.T) {
	f := newFixture(t)

	referenced := f.store()
	dangling1 := f.store()
	dangling2 := f.store()

	fld := insertFolder(t, f)
	insertEntry(t, f, fld, referenced.ID, 1)

	got, err := f.repo.DanglingMsgIDs(f.ctx, 10)
	require.NoError(t, err)
	require.ElementsMatch(t, []ulid.ULID{dangling1.ID, dangling2.ID}, got)

	got, err = f.repo.DanglingMsgIDs(f.ctx, 1)
	require.NoError(t, err)
	require.Len(t, got, 1)
}

// insertFolder creates an account with a folder using raw statements, the
// message repository does not depend on the account and folder repositories.
func insertFolder(t *testing.T, f *fixture) ulid.ULID {
	t.Helper()

	now := time.Now()
	acctID, fldID := ulid.Make(), ulid.Make()

	_, err := f.db.Exec(f.ctx, table.Accounts.INSERT(table.Accounts.AllColumns).MODEL(model.Accounts{
		ID: acctID[:], Name: "test", CreatedAt: now, UpdatedAt: now, Namespace: []byte("{}"),
	}))
	require.NoError(t, err)

	_, err = f.db.Exec(f.ctx, table.Folders.INSERT(table.Folders.AllColumns).MODEL(model.Folders{
		ID: fldID[:], AccountID: acctID[:], Name: "INBOX", Path: "INBOX",
		Subscribed: 1, SortOrder: 1, Meta: []byte("{}"), CreatedAt: now, UpdatedAt: now,
	}))
	require.NoError(t, err)

	return fldID
}

func insertEntry(t *testing.T, f *fixture, fldID, msgID ulid.ULID, uid int64) {
	t.Helper()

	_, err := f.db.Exec(f.ctx, table.FolderEntries.INSERT(table.FolderEntries.AllColumns).MODEL(model.FolderEntries{
		FolderID: fldID[:], MessageID: ptr(msgID[:]), UID: uid, Modseq: 1, CreatedAtModseq: 1, CreatedAt: time.Now(),
	}))
	require.NoError(t, err)
}

func ptr[T any](v T) *T { return &v }

func TestMessage_FlagOpsWithEmptyFlagListAreNoop(t *testing.T) {
	f := newFixture(t)

	m := f.store(withFlags("a"))

	changed, err := f.repo.AddFlags(f.ctx, []ulid.ULID{m.ID}, nil, 1, 2)
	require.NoError(t, err)
	require.Empty(t, changed)

	changed, err = f.repo.DeleteFlags(f.ctx, []ulid.ULID{m.ID}, nil, 1, 2)
	require.NoError(t, err)
	require.Empty(t, changed)

	require.Equal(t, []string{"a"}, f.flagsOf(m.ID))
}

func TestMessage_ReplaceFlagsWithEmptyListClearsFlags(t *testing.T) {
	f := newFixture(t)

	m := f.store(withFlags("a"))

	_, err := f.repo.ReplaceFlags(f.ctx, []ulid.ULID{m.ID}, nil, 1, 2)
	require.NoError(t, err)

	require.Empty(t, f.flagsOf(m.ID))
}
