package searchersqlite_test

import (
	"context"
	"testing"
	"time"

	"github.com/emersion/go-imap/v2"
	"github.com/foxcpp/maddy-storage/internal/domain/account"
	accountsql "github.com/foxcpp/maddy-storage/internal/domain/account/repository/sqlite"
	"github.com/foxcpp/maddy-storage/internal/domain/folder"
	"github.com/foxcpp/maddy-storage/internal/domain/folder/recent"
	foldersqlite "github.com/foxcpp/maddy-storage/internal/domain/folder/repository/sqlite"
	"github.com/foxcpp/maddy-storage/internal/domain/message"
	messagesqlite "github.com/foxcpp/maddy-storage/internal/domain/message/repository/sqlite"
	"github.com/foxcpp/maddy-storage/internal/domain/message/searcher"
	searchersql "github.com/foxcpp/maddy-storage/internal/domain/message/searcher/metaonly/sqlite"
	"github.com/foxcpp/maddy-storage/internal/domain/metadata"
	"github.com/foxcpp/maddy-storage/internal/pkg/contextlib"
	"github.com/foxcpp/maddy-storage/internal/repository/sqlite"
	"github.com/oklog/ulid/v2"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap/zaptest"
)

var base = time.Date(2024, 3, 10, 12, 0, 0, 0, time.UTC)

type fixture struct {
	t        *testing.T
	ctx      context.Context
	db       sqlite.DB
	s        searcher.Searcher
	folders  folder.Repo
	messages message.Repo
	acct     *account.Account
	inbox    ulid.ULID
	other    ulid.ULID
}

func newFixture(t *testing.T, cfg searchersql.Cfg) *fixture {
	t.Helper()

	ctx := contextlib.WithLogger(context.Background(), zaptest.NewLogger(t))

	db, err := sqlite.NewMemory(sqlite.Cfg{})
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	f := &fixture{
		t: t, ctx: ctx, db: db,
		s:        searchersql.New(db, cfg),
		folders:  foldersqlite.New(db),
		messages: messagesqlite.New(db),
	}
	f.acct = f.newAccount("test")
	f.inbox = f.mkFolder(f.acct, "inbox")
	f.other = f.mkFolder(f.acct, "other")

	return f
}

func (f *fixture) newAccount(name string) *account.Account {
	f.t.Helper()

	acct, err := account.NewAccount(name)
	require.NoError(f.t, err)
	require.NoError(f.t, accountsql.New(f.db).Create(f.ctx, acct))

	return acct
}

func (f *fixture) mkFolder(acct *account.Account, name string) ulid.ULID {
	f.t.Helper()

	fld, err := folder.NewFolder(nil, acct.ID, name, folder.RoleNone)
	require.NoError(f.t, err)
	require.NoError(f.t, f.folders.Create(f.ctx, fld))

	return fld.ID
}

type item struct {
	msg *message.Msg
	ent *folder.Entry
}

type opt func(*item)

func flags(fl ...string) opt         { return func(i *item) { i.msg.Flags = fl } }
func size(n uint32) opt              { return func(i *item) { i.msg.TotalSize = n } }
func received(t time.Time) opt       { return func(i *item) { i.msg.ReceivedAt = t } }
func updated(t time.Time) opt        { return func(i *item) { i.msg.UpdatedAt = t } }
func sent(t time.Time) opt           { return func(i *item) { i.msg.Content.Envelope.Date = t } }
func msgModSeq(ms folder.ModSeq) opt { return func(i *item) { i.msg.ModSeq = ms } }
func entModSeq(ms folder.ModSeq) opt { return func(i *item) { i.ent.ModSeq = ms } }
func createdAt(ms folder.ModSeq) opt { return func(i *item) { i.ent.CreatedAtModSeq = ms } }
func from(name, addr string) opt {
	return func(i *item) { i.msg.Content.Envelope.From = []*message.Address{{Name: name, Address: addr}} }
}
func to(name, addr string) opt {
	return func(i *item) { i.msg.Content.Envelope.To = []*message.Address{{Name: name, Address: addr}} }
}
func cc(name, addr string) opt {
	return func(i *item) { i.msg.Content.Envelope.Cc = []*message.Address{{Name: name, Address: addr}} }
}

// add stores a message with an entry in the folder. Returns the message ID.
func (f *fixture) add(fld ulid.ULID, uid uint32, opts ...opt) ulid.ULID {
	f.t.Helper()

	it := item{
		msg: &message.Msg{
			ID:              ulid.Make(),
			ReceivedAt:      base,
			CreatedAtModSeq: 1,
			ModSeq:          1,
			CreatedAt:       base,
			UpdatedAt:       base,
			Meta:            metadata.Md{},
			TotalSize:       10,
			Content:         &message.ContentData{Envelope: &message.ContentEnvelope{Date: base}},
			Parts: []message.Part{{
				ID: ulid.Make(), Path: message.Path{1}, Inline: []byte("x"),
				Content: &message.ContentPartData{Type: "text/plain"},
			}},
		},
	}
	ent := folder.NewEntry(fld, it.msg.ID, uid, 1, base)
	it.ent = &ent
	for _, o := range opts {
		o(&it)
	}

	require.NoError(f.t, f.messages.Create(f.ctx, *it.msg))
	require.NoError(f.t, f.folders.CreateEntry(f.ctx, *it.ent))

	return it.msg.ID
}

func (f *fixture) search(cond *searcher.Cond, opts searcher.Opts) searcher.SearchResult {
	f.t.Helper()

	if cond == nil {
		cond = &searcher.Cond{}
	}
	res, err := f.s.Search(f.ctx, f.acct.ID, cond, opts)
	require.NoError(f.t, err)

	return res
}

func (f *fixture) uids(cond *searcher.Cond) []uint32 {
	f.t.Helper()

	res := f.search(cond, searcher.Opts{ReturnAll: true})
	uids := make([]uint32, len(res.All))
	for i, m := range res.All {
		uids[i] = m.UID
	}
	return uids
}

func (f *fixture) inboxUIDs(cond searcher.Cond) []uint32 {
	f.t.Helper()

	cond.FolderIDs = []ulid.ULID{f.inbox}
	return f.uids(&cond)
}

func (f *fixture) deleteEntry(fld, msgID ulid.ULID, modSeq folder.ModSeq) {
	f.t.Helper()

	_, err := f.folders.DeleteEntryByIDs(f.ctx, fld, []ulid.ULID{msgID}, modSeq)
	require.NoError(f.t, err)
}

func TestSearch_ReturnAllFields(t *testing.T) {
	f := newFixture(t, searchersql.Cfg{})

	id1 := f.add(f.inbox, 1, size(100), msgModSeq(9), entModSeq(4), updated(base.Add(time.Hour)))
	id2 := f.add(f.inbox, 2, size(50), msgModSeq(2), entModSeq(7))

	res := f.search(&searcher.Cond{FolderIDs: []ulid.ULID{f.inbox}}, searcher.Opts{ReturnAll: true})

	require.Len(t, res.All, 2)
	require.Equal(t, searcher.FoundMsg{
		FolderID:  f.inbox,
		UID:       1,
		ModSeq:    9,
		MessageID: id1,
		UpdatedAt: base.Add(time.Hour),
		TotalSize: 100,
	}, normalize(res.All[0]), "modseq is the max of entry and message modseqs")
	require.Equal(t, id2, res.All[1].MessageID)
	require.Equal(t, folder.ModSeq(7), res.All[1].ModSeq)
	require.Zero(t, res.All[0].SeqNum, "seqnums are not requested")
}

// normalize makes timestamps comparable regardless of the stored location.
func normalize(m searcher.FoundMsg) searcher.FoundMsg {
	m.UpdatedAt = m.UpdatedAt.UTC()
	return m
}

func TestSearch_ScopedByFolderAndAccount(t *testing.T) {
	f := newFixture(t, searchersql.Cfg{})

	f.add(f.inbox, 1)
	f.add(f.other, 1)
	f.add(f.other, 2)

	otherAcct := f.newAccount("someone-else")
	foreign := f.mkFolder(otherAcct, "inbox")
	f.add(foreign, 1)

	require.Len(t, f.uids(&searcher.Cond{FolderIDs: []ulid.ULID{f.inbox}}), 1)
	require.Len(t, f.uids(&searcher.Cond{FolderIDs: []ulid.ULID{f.other}}), 2)
	require.Len(t, f.uids(&searcher.Cond{FolderIDs: []ulid.ULID{f.inbox, f.other}}), 3)
	require.Len(t, f.uids(nil), 3, "no folder condition means all folders of the account")
	require.Empty(t, f.uids(&searcher.Cond{FolderIDs: []ulid.ULID{foreign}}), "folders of other accounts are invisible")
}

func TestSearch_EmptyFolderIDsMatchNothing(t *testing.T) {
	f := newFixture(t, searchersql.Cfg{})

	f.add(f.inbox, 1)

	require.Empty(t, f.uids(&searcher.Cond{FolderIDs: []ulid.ULID{}}))
}

func TestSearch_DeletedEntries(t *testing.T) {
	f := newFixture(t, searchersql.Cfg{})

	f.add(f.inbox, 1)
	id2 := f.add(f.inbox, 2, createdAt(5), entModSeq(5))
	f.deleteEntry(f.inbox, id2, 8)

	t.Run("HiddenByDefault", func(t *testing.T) {
		require.Equal(t, []uint32{1}, f.inboxUIDs(searcher.Cond{}))
	})
	t.Run("VisibleBeforeDeletion", func(t *testing.T) {
		res := f.search(&searcher.Cond{FolderIDs: []ulid.ULID{f.inbox}}, searcher.Opts{ReturnAll: true, At: 6})
		require.Len(t, res.All, 1+1)
		require.Equal(t, uint32(2), res.All[1].UID)
		require.False(t, res.All[1].DeletedAt.IsZero(), "must be reported as deleted")
		require.True(t, res.All[0].DeletedAt.IsZero())
	})
	t.Run("DeletesAtSeparate", func(t *testing.T) {
		res := f.search(&searcher.Cond{FolderIDs: []ulid.ULID{f.inbox}}, searcher.Opts{ReturnAll: true, At: 9, DeletesAt: 7})
		require.Len(t, res.All, 2)
	})
	t.Run("GoneAfterDeletion", func(t *testing.T) {
		res := f.search(&searcher.Cond{FolderIDs: []ulid.ULID{f.inbox}}, searcher.Opts{ReturnAll: true, At: 9})
		require.Len(t, res.All, 1)
	})
	t.Run("NotYetCreated", func(t *testing.T) {
		res := f.search(&searcher.Cond{FolderIDs: []ulid.ULID{f.inbox}}, searcher.Opts{ReturnAll: true, At: 3})
		require.Len(t, res.All, 1)
		require.Equal(t, uint32(1), res.All[0].UID)
	})
}

func TestSearch_AtIgnoresLaterEntryModSeqBumps(t *testing.T) {
	f := newFixture(t, searchersql.Cfg{})

	// The entry exists since modseq 2, flags were changed (modseq bumped) at 9.
	f.add(f.inbox, 1, createdAt(2), entModSeq(9))

	cond := &searcher.Cond{FolderIDs: []ulid.ULID{f.inbox}}

	all := f.search(cond, searcher.Opts{ReturnAll: true, At: 5})
	require.Len(t, all.All, 1)

	agg := f.search(cond, searcher.Opts{ReturnCount: true, At: 5})
	require.Equal(t, uint32(1), agg.Count, "aggregated path must agree with ReturnAll")
}

func TestSearch_SeqNums(t *testing.T) {
	f := newFixture(t, searchersql.Cfg{})

	f.add(f.inbox, 1, createdAt(1))
	id2 := f.add(f.inbox, 3, createdAt(1))
	f.add(f.inbox, 7, createdAt(1))
	f.add(f.inbox, 9, createdAt(20)) // not yet created at 10
	f.add(f.other, 4, createdAt(1))
	f.deleteEntry(f.inbox, id2, 15)

	cond := &searcher.Cond{FolderIDs: []ulid.ULID{f.inbox}}

	t.Run("AtBeforeDeletion", func(t *testing.T) {
		res := f.search(cond, searcher.Opts{ReturnAll: true, ReturnSeqNums: true, At: 10})
		require.Len(t, res.All, 3)
		require.Equal(t, uint32(1), res.All[0].SeqNum)
		require.Equal(t, uint32(2), res.All[1].SeqNum)
		require.Equal(t, uint32(3), res.All[2].SeqNum)
	})
	t.Run("AfterDeletion", func(t *testing.T) {
		res := f.search(cond, searcher.Opts{ReturnAll: true, ReturnSeqNums: true, At: 30})
		require.Len(t, res.All, 3)
		require.Equal(t, []uint32{1, 7, 9}, []uint32{res.All[0].UID, res.All[1].UID, res.All[2].UID})
		require.Equal(t, []uint32{1, 2, 3}, []uint32{res.All[0].SeqNum, res.All[1].SeqNum, res.All[2].SeqNum})
	})
	t.Run("PerFolder", func(t *testing.T) {
		res := f.search(&searcher.Cond{}, searcher.Opts{ReturnAll: true, ReturnSeqNums: true, At: 10})
		for _, m := range res.All {
			if m.FolderID == f.other {
				require.Equal(t, uint32(1), m.SeqNum)
			}
		}
	})
	t.Run("RequiresAt", func(t *testing.T) {
		require.Panics(t, func() {
			_, _ = f.s.Search(f.ctx, f.acct.ID, cond, searcher.Opts{ReturnAll: true, ReturnSeqNums: true})
		})
	})
}

func TestSearch_NumericIDs(t *testing.T) {
	f := newFixture(t, searchersql.Cfg{})

	for _, uid := range []uint32{2, 4, 6, 8, 10} {
		f.add(f.inbox, uid)
	}

	at := folder.ModSeq(5)
	cases := []struct {
		name string
		rng  []folder.Range
		want []uint32
	}{
		{"Values", []folder.Range{{Values: []uint32{4, 8, 11}}}, []uint32{4, 8}},
		{"Interval", []folder.Range{{Intervals: []folder.NumInterval{{Since: 3, Until: 7}}}}, []uint32{4, 6}},
		{"ValuesOrIntervals", []folder.Range{{Values: []uint32{2}, Intervals: []folder.NumInterval{{Since: 8, Until: 20}}}}, []uint32{2, 8, 10}},
		{"EmptyValuesMatchNothing", []folder.Range{{Values: []uint32{}}}, nil},
		{"EmptyRangeMatchesAll", []folder.Range{{}}, []uint32{2, 4, 6, 8, 10}},
		{"RangesAreAnded", []folder.Range{
			{Intervals: []folder.NumInterval{{Since: 1, Until: 6}}},
			{Intervals: []folder.NumInterval{{Since: 4, Until: 10}}},
		}, []uint32{4, 6}},
		{"SeqValues", []folder.Range{{SeqNum: true, Values: []uint32{1, 5}, At: at}}, []uint32{2, 10}},
		{"SeqInterval", []folder.Range{{SeqNum: true, Intervals: []folder.NumInterval{{Since: 2, Until: 3}}, At: at}}, []uint32{4, 6}},
		{"SeqEmptyValues", []folder.Range{{SeqNum: true, Values: []uint32{}, At: at}}, nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			opts := searcher.Opts{ReturnAll: true}
			if c.rng[0].SeqNum {
				opts.At = at
			}
			res := f.search(&searcher.Cond{FolderIDs: []ulid.ULID{f.inbox}, NumericIDs: c.rng}, opts)
			got := make([]uint32, 0, len(res.All))
			for _, m := range res.All {
				got = append(got, m.UID)
			}
			if c.want == nil {
				require.Empty(t, got)
			} else {
				require.Equal(t, c.want, got)
			}
		})
	}
}

func TestSearch_DateConds(t *testing.T) {
	f := newFixture(t, searchersql.Cfg{})

	day := func(d int, h int) time.Time { return time.Date(2024, 3, d, h, 0, 0, 0, time.UTC) }

	f.add(f.inbox, 1, sent(day(1, 23)), received(day(2, 1)), updated(day(3, 1)))
	f.add(f.inbox, 2, sent(day(5, 1)), received(day(5, 12)), updated(day(6, 1)))
	f.add(f.inbox, 3, sent(day(9, 12)), received(day(9, 23)), updated(day(9, 1)))
	// Envelope date was not parsed, falls back to received_at.
	f.add(f.inbox, 4, sent(time.Time{}), received(day(7, 12)), updated(day(8, 1)))

	cases := []struct {
		name string
		cond searcher.Cond
		want []uint32
	}{
		{"SentAfter", searcher.Cond{SentAfter: day(5, 0)}, []uint32{2, 3, 4}},
		{"SentBefore", searcher.Cond{SentBefore: day(5, 12)}, []uint32{1, 2}},
		{"SentAfterDateOnly", searcher.Cond{SentAfter: day(5, 12), SentDateOnly: true}, []uint32{2, 3, 4}},
		{"SentBeforeDateOnly", searcher.Cond{SentBefore: day(5, 0), SentDateOnly: true}, []uint32{1}},
		{"ReceivedAfter", searcher.Cond{ReceivedAfter: day(5, 12)}, []uint32{3, 4}},
		{"ReceivedBefore", searcher.Cond{ReceivedBefore: day(5, 12)}, []uint32{1}},
		{"ReceivedAfterDateOnly", searcher.Cond{ReceivedAfter: day(9, 1), ReceivedDateOnly: true}, []uint32{3}},
		{"ReceivedBeforeDateOnly", searcher.Cond{ReceivedBefore: day(5, 23), ReceivedDateOnly: true}, []uint32{1}},
		{"UpdatedGt", searcher.Cond{UpdatedGt: day(6, 1)}, []uint32{3, 4}},
		{"UpdatedLt", searcher.Cond{UpdatedLt: day(6, 1)}, []uint32{1}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			require.Equal(t, c.want, f.inboxUIDs(c.cond))
		})
	}
}

func TestSearch_SizeConds(t *testing.T) {
	f := newFixture(t, searchersql.Cfg{})

	f.add(f.inbox, 1, size(10))
	f.add(f.inbox, 2, size(20))
	f.add(f.inbox, 3, size(30))

	require.Equal(t, []uint32{2, 3}, f.inboxUIDs(searcher.Cond{SizeGt: 10}))
	require.Equal(t, []uint32{1, 2}, f.inboxUIDs(searcher.Cond{SizeLt: 30}))
	require.Equal(t, []uint32{2}, f.inboxUIDs(searcher.Cond{SizeGt: 10, SizeLt: 30}))
}

func TestSearch_Flags(t *testing.T) {
	f := newFixture(t, searchersql.Cfg{})

	f.add(f.inbox, 1, flags(`\Seen`, `\Flagged`))
	f.add(f.inbox, 2, flags(`\Seen`))
	f.add(f.inbox, 3)
	f.add(f.inbox, 4, flags(`\Flagged`, "custom"))

	cases := []struct {
		name string
		cond searcher.Cond
		want []uint32
	}{
		{"Flag", searcher.Cond{Flag: []string{`\Seen`}}, []uint32{1, 2}},
		{"FlagIsCaseInsensitive", searcher.Cond{Flag: []string{`\SEEN`}}, []uint32{1, 2}},
		{"FlagsAreAnded", searcher.Cond{Flag: []string{`\Seen`, `\Flagged`}}, []uint32{1}},
		{"UnknownFlag", searcher.Cond{Flag: []string{"nope"}}, nil},
		{"NoFlag", searcher.Cond{NoFlag: []string{`\Seen`}}, []uint32{3, 4}},
		{"NoFlagIsCaseInsensitive", searcher.Cond{NoFlag: []string{"CUSTOM"}}, []uint32{1, 2, 3}},
		{"NoFlagsAreOred", searcher.Cond{NoFlag: []string{`\Seen`, "custom"}}, []uint32{3}},
		{"FlagAndNoFlag", searcher.Cond{Flag: []string{`\Flagged`}, NoFlag: []string{`\Seen`}}, []uint32{4}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := f.inboxUIDs(c.cond)
			if c.want == nil {
				require.Empty(t, got)
			} else {
				require.Equal(t, c.want, got)
			}
		})
	}
}

func TestSearch_RecentFlag(t *testing.T) {
	f := newFixture(t, searchersql.Cfg{})

	f.add(f.inbox, 1, flags(`\Seen`))
	f.add(f.inbox, 2, flags(`\Seen`))
	f.add(f.inbox, 3)

	recents := func(uids ...imap.UID) context.Context {
		set := recent.Set{}
		for _, u := range uids {
			set.Set.AddNum(u)
			set.Size++
		}
		return recent.WithSet(f.ctx, set)
	}
	search := func(ctx context.Context, cond searcher.Cond) []uint32 {
		cond.FolderIDs = []ulid.ULID{f.inbox}
		res, err := f.s.Search(ctx, f.acct.ID, &cond, searcher.Opts{ReturnAll: true})
		require.NoError(t, err)
		var uids []uint32
		for _, m := range res.All {
			uids = append(uids, m.UID)
		}
		return uids
	}

	require.Equal(t, []uint32{2, 3}, search(recents(2, 3), searcher.Cond{Flag: []string{`\Recent`}}))
	require.Equal(t, []uint32{2}, search(recents(2, 3), searcher.Cond{Flag: []string{`\recent`, `\Seen`}}))
	require.Empty(t, search(f.ctx, searcher.Cond{Flag: []string{`\Recent`}}), "no recents in context")
	require.Empty(t, search(recents(), searcher.Cond{Flag: []string{`\Recent`}}), "empty recents set")

	require.Equal(t, []uint32{1}, search(recents(2, 3), searcher.Cond{NoFlag: []string{`\Recent`}}))
	require.Equal(t, []uint32{1, 2, 3}, search(f.ctx, searcher.Cond{NoFlag: []string{`\Recent`}}), "nothing is recent")
	require.Equal(t, []uint32{3}, search(recents(2, 3), searcher.Cond{NoFlag: []string{`\Seen`}}))
}

func TestSearch_Not(t *testing.T) {
	f := newFixture(t, searchersql.Cfg{})

	f.add(f.inbox, 1, size(10), flags(`\Seen`))
	f.add(f.inbox, 2, size(20), flags(`\Seen`))
	f.add(f.inbox, 3, size(30))

	require.Equal(t, []uint32{3}, f.inboxUIDs(searcher.Cond{
		Not: []*searcher.Cond{{Flag: []string{`\Seen`}}},
	}))
	require.Equal(t, []uint32{1, 3}, f.inboxUIDs(searcher.Cond{
		Not: []*searcher.Cond{{Flag: []string{`\Seen`}, SizeGt: 10}},
	}), "NOT applies to the whole nested condition")
	require.Equal(t, []uint32{3}, f.inboxUIDs(searcher.Cond{
		Not: []*searcher.Cond{{Flag: []string{`\Seen`}}, {SizeLt: 5}},
	}), "multiple NOTs are anded")
	require.Equal(t, []uint32{2}, f.inboxUIDs(searcher.Cond{
		Not: []*searcher.Cond{{NumericIDs: []folder.Range{{Values: []uint32{1, 3}}}}},
	}))
	require.Equal(t, []uint32{1, 2}, f.inboxUIDs(searcher.Cond{
		Not: []*searcher.Cond{{Not: []*searcher.Cond{{Flag: []string{`\Seen`}}}}},
	}), "double negation")
}

func TestSearch_Or(t *testing.T) {
	f := newFixture(t, searchersql.Cfg{})

	f.add(f.inbox, 1, size(10), flags(`\Seen`))
	f.add(f.inbox, 2, size(20), flags(`\Flagged`))
	f.add(f.inbox, 3, size(30))
	f.add(f.inbox, 4, size(40), flags(`\Seen`, `\Flagged`))

	require.Equal(t, []uint32{1, 2, 4}, f.inboxUIDs(searcher.Cond{
		Or: [][]*searcher.Cond{{{Flag: []string{`\Seen`}}, {Flag: []string{`\Flagged`}}}},
	}))
	require.Equal(t, []uint32{2}, f.inboxUIDs(searcher.Cond{
		SizeLt: 30,
		Or:     [][]*searcher.Cond{{{Flag: []string{`\Seen`}}, {Flag: []string{`\Flagged`}}}},
		Not:    []*searcher.Cond{{Flag: []string{`\Seen`}}},
	}), "OR is anded with the rest of the conditions")
	require.Equal(t, []uint32{1, 3}, f.inboxUIDs(searcher.Cond{
		Or: [][]*searcher.Cond{{{SizeLt: 15}, {SizeGt: 25, Not: []*searcher.Cond{{Flag: []string{`\Flagged`}}}}}},
	}))
	require.Equal(t, []uint32{4}, f.inboxUIDs(searcher.Cond{
		Or: [][]*searcher.Cond{
			{{Flag: []string{`\Seen`}}, {Flag: []string{`\Flagged`}}},
			{{SizeGt: 35}, {SizeLt: 5}},
		},
	}), "multiple ORs are anded")
}

func TestSearch_Sort(t *testing.T) {
	f := newFixture(t, searchersql.Cfg{})

	f.add(f.inbox, 1, size(30), received(base.Add(2*time.Hour)), sent(base.Add(3*time.Hour)),
		from("Zed", "a@x"), to("Bob", "z@x"), cc("", "m@x"))
	f.add(f.inbox, 2, size(10), received(base.Add(3*time.Hour)), sent(base.Add(1*time.Hour)),
		from("Amy", "c@x"), to("Cat", "y@x"), cc("", "k@x"))
	f.add(f.inbox, 3, size(20), received(base.Add(1*time.Hour)), sent(base.Add(2*time.Hour)),
		from("Mia", "b@x"), to("Ann", "x@x"))
	f.add(f.inbox, 4, size(20), received(base.Add(1*time.Hour)), sent(base.Add(2*time.Hour)),
		from("Mia", "b@x"), to("Ann", "x@x"))

	cases := []struct {
		name string
		keys []searcher.SortKey
		want []uint32
	}{
		{"Default", nil, []uint32{1, 2, 3, 4}},
		{"Size", []searcher.SortKey{{Field: searcher.SortSize}}, []uint32{2, 3, 4, 1}},
		{"SizeReverse", []searcher.SortKey{{Field: searcher.SortSize, Reverse: true}}, []uint32{1, 3, 4, 2}},
		{"ReceivedAt", []searcher.SortKey{{Field: searcher.SortReceivedAt}}, []uint32{3, 4, 1, 2}},
		{"ReceivedAtReverse", []searcher.SortKey{{Field: searcher.SortReceivedAt, Reverse: true}}, []uint32{2, 1, 3, 4}},
		{"Date", []searcher.SortKey{{Field: searcher.SortDate}}, []uint32{2, 3, 4, 1}},
		{"DateReverse", []searcher.SortKey{{Field: searcher.SortDate, Reverse: true}}, []uint32{1, 3, 4, 2}},
		{"From", []searcher.SortKey{{Field: searcher.SortFrom}}, []uint32{1, 3, 4, 2}},
		{"FromReverse", []searcher.SortKey{{Field: searcher.SortFrom, Reverse: true}}, []uint32{2, 3, 4, 1}},
		{"DisplayFrom", []searcher.SortKey{{Field: searcher.SortDisplayFrom}}, []uint32{2, 3, 4, 1}},
		{"DisplayFromReverse", []searcher.SortKey{{Field: searcher.SortDisplayFrom, Reverse: true}}, []uint32{1, 3, 4, 2}},
		{"To", []searcher.SortKey{{Field: searcher.SortTo}}, []uint32{3, 4, 2, 1}},
		{"ToReverse", []searcher.SortKey{{Field: searcher.SortTo, Reverse: true}}, []uint32{1, 2, 3, 4}},
		{"DisplayTo", []searcher.SortKey{{Field: searcher.SortDisplayTo}}, []uint32{3, 4, 1, 2}},
		{"DisplayToReverse", []searcher.SortKey{{Field: searcher.SortDisplayTo, Reverse: true}}, []uint32{2, 1, 3, 4}},
		{"CCNullsFirst", []searcher.SortKey{{Field: searcher.SortCC}}, []uint32{3, 4, 2, 1}},
		{"CCReverseNullsFirst", []searcher.SortKey{{Field: searcher.SortCC, Reverse: true}}, []uint32{3, 4, 1, 2}},
		{"Multiple", []searcher.SortKey{{Field: searcher.SortSize}, {Field: searcher.SortReceivedAt, Reverse: true}}, []uint32{2, 3, 4, 1}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			res := f.search(&searcher.Cond{FolderIDs: []ulid.ULID{f.inbox}}, searcher.Opts{ReturnAll: true, Sort: c.keys})
			got := make([]uint32, len(res.All))
			for i, m := range res.All {
				got[i] = m.UID
			}
			require.Equal(t, c.want, got)
		})
	}
}

func TestSearch_MaxResults(t *testing.T) {
	f := newFixture(t, searchersql.Cfg{MaxResults: 2})

	for uid := uint32(1); uid <= 4; uid++ {
		f.add(f.inbox, uid)
	}

	require.Equal(t, []uint32{1, 2}, f.uids(&searcher.Cond{FolderIDs: []ulid.ULID{f.inbox}}))
}

func TestSearch_Aggregated(t *testing.T) {
	f := newFixture(t, searchersql.Cfg{})

	f.add(f.inbox, 2, size(10), msgModSeq(3), entModSeq(5))
	f.add(f.inbox, 5, size(20), msgModSeq(11), entModSeq(4))
	f.add(f.inbox, 9, size(30), msgModSeq(1), entModSeq(1), flags(`\Seen`))
	f.add(f.other, 1, size(40))

	cond := &searcher.Cond{FolderIDs: []ulid.ULID{f.inbox}}

	t.Run("Everything", func(t *testing.T) {
		res := f.search(cond, searcher.Opts{
			ReturnCount: true, ReturnMinUID: true, ReturnMaxUID: true,
			ReturnModSeq: true, ReturnTotalSize: true, ReturnSeqNums: true, At: 100,
		})
		require.Equal(t, uint32(3), res.Count)
		require.Equal(t, uint32(2), res.MinUID)
		require.Equal(t, uint32(9), res.MaxUID)
		require.Equal(t, uint32(1), res.MinSeq)
		require.Equal(t, uint32(3), res.MaxSeq)
		require.Equal(t, folder.ModSeq(11), res.MaxModSeq)
		require.Equal(t, int64(60), res.TotalSize, "total size is returned without GroupByFolder too")
	})
	t.Run("OnlyRequested", func(t *testing.T) {
		res := f.search(cond, searcher.Opts{ReturnCount: true})
		require.Equal(t, searcher.SearchResult{Count: 3}, res)
	})
	t.Run("NothingRequested", func(t *testing.T) {
		res := f.search(cond, searcher.Opts{})
		require.Equal(t, searcher.SearchResult{}, res)
	})
	t.Run("WithCondition", func(t *testing.T) {
		res := f.search(&searcher.Cond{FolderIDs: []ulid.ULID{f.inbox}, Flag: []string{`\Seen`}},
			searcher.Opts{ReturnCount: true, ReturnMinUID: true, ReturnMaxUID: true})
		require.Equal(t, searcher.SearchResult{Count: 1, MinUID: 9, MaxUID: 9}, res)
	})
	t.Run("NoMatches", func(t *testing.T) {
		res := f.search(&searcher.Cond{FolderIDs: []ulid.ULID{f.inbox}, Flag: []string{"nope"}},
			searcher.Opts{ReturnCount: true, ReturnMinUID: true, ReturnMaxUID: true, ReturnModSeq: true, ReturnTotalSize: true})
		require.Equal(t, searcher.SearchResult{}, res)
	})
	t.Run("SeqNumConditionWithoutSeqNumsReturned", func(t *testing.T) {
		res := f.search(&searcher.Cond{
			FolderIDs:  []ulid.ULID{f.inbox},
			NumericIDs: []folder.Range{{SeqNum: true, Intervals: []folder.NumInterval{{Since: 2, Until: 3}}}},
		}, searcher.Opts{ReturnCount: true, ReturnMinUID: true, At: 100})
		require.Equal(t, uint32(2), res.Count)
		require.Equal(t, uint32(5), res.MinUID)
	})
	t.Run("DeletedExcluded", func(t *testing.T) {
		id := f.add(f.other, 7)
		f.deleteEntry(f.other, id, 50)
		res := f.search(&searcher.Cond{FolderIDs: []ulid.ULID{f.other}}, searcher.Opts{ReturnCount: true})
		require.Equal(t, uint32(1), res.Count)
	})
}

func TestSearch_AggregatedGroupByFolder(t *testing.T) {
	f := newFixture(t, searchersql.Cfg{})

	empty := f.mkFolder(f.acct, "empty")
	f.add(f.inbox, 1, size(10), msgModSeq(4))
	f.add(f.inbox, 2, size(20), msgModSeq(6))
	f.add(f.other, 5, size(40), msgModSeq(9))

	res := f.search(&searcher.Cond{FolderIDs: []ulid.ULID{f.inbox, f.other, empty}}, searcher.Opts{
		GroupByFolder: true, ReturnCount: true, ReturnMinUID: true, ReturnMaxUID: true,
		ReturnModSeq: true, ReturnTotalSize: true,
	})

	require.Equal(t, uint32(3), res.Count)
	require.Equal(t, uint32(1), res.MinUID)
	require.Equal(t, uint32(5), res.MaxUID)
	require.Equal(t, folder.ModSeq(9), res.MaxModSeq)
	require.Equal(t, int64(70), res.TotalSize)

	require.Len(t, res.ByFolder, 3)
	require.Equal(t, searcher.SearchResult{
		Count: 2, MinUID: 1, MaxUID: 2, MaxModSeq: 6, TotalSize: 30,
	}, *res.ByFolder[f.inbox])
	require.Equal(t, searcher.SearchResult{
		Count: 1, MinUID: 5, MaxUID: 5, MaxModSeq: 9, TotalSize: 40,
	}, *res.ByFolder[f.other])
	require.Equal(t, searcher.SearchResult{}, *res.ByFolder[empty])
}

func TestSearch_AggregatedGroupByFolderSeqNums(t *testing.T) {
	f := newFixture(t, searchersql.Cfg{})

	f.add(f.inbox, 4)
	f.add(f.inbox, 8)
	f.add(f.other, 3)

	res := f.search(&searcher.Cond{FolderIDs: []ulid.ULID{f.inbox, f.other}}, searcher.Opts{
		GroupByFolder: true, ReturnMinUID: true, ReturnMaxUID: true, ReturnSeqNums: true, At: 10,
	})

	require.Equal(t, uint32(1), res.ByFolder[f.inbox].MinSeq)
	require.Equal(t, uint32(2), res.ByFolder[f.inbox].MaxSeq)
	require.Equal(t, uint32(1), res.ByFolder[f.other].MaxSeq)
}

func TestSearch_Shortcuts(t *testing.T) {
	f := newFixture(t, searchersql.Cfg{})

	f.add(f.inbox, 1, flags(`\Seen`))
	f.add(f.inbox, 2)
	f.add(f.other, 3, flags(`\Seen`))

	opts := searcher.Opts{ReturnAll: true}

	res, err := f.s.SearchFolder(f.ctx, f.acct.ID, f.inbox, opts)
	require.NoError(t, err)
	require.Len(t, res.All, 2)

	res, err = f.s.SearchFlag(f.ctx, f.acct.ID, f.inbox, `\Seen`, opts)
	require.NoError(t, err)
	require.Len(t, res.All, 1)
	require.Equal(t, uint32(1), res.All[0].UID)

	res, err = f.s.SearchWithoutFlag(f.ctx, f.acct.ID, f.inbox, `\Seen`, opts)
	require.NoError(t, err)
	require.Len(t, res.All, 1)
	require.Equal(t, uint32(2), res.All[0].UID)
}

func TestSearch_Tx(t *testing.T) {
	f := newFixture(t, searchersql.Cfg{})

	f.add(f.inbox, 1)

	err := f.s.Tx(f.ctx, func(ctx context.Context, s searcher.Searcher) error {
		res, err := s.SearchFolder(ctx, f.acct.ID, f.inbox, searcher.Opts{ReturnCount: true})
		require.NoError(t, err)
		require.Equal(t, uint32(1), res.Count)
		return nil
	})
	require.NoError(t, err)
}

func TestSearch_EmptyNestedConds(t *testing.T) {
	f := newFixture(t, searchersql.Cfg{})

	f.add(f.inbox, 1, flags(`\Seen`))
	f.add(f.inbox, 2)

	seen := &searcher.Cond{Flag: []string{`\Seen`}}

	t.Run("OrWithEmptyOperandIsTrue", func(t *testing.T) {
		require.Equal(t, []uint32{1, 2}, f.inboxUIDs(searcher.Cond{
			Or: [][]*searcher.Cond{{{}, seen}},
		}))
	})
	t.Run("OrWithEmptySecondOperandIsTrue", func(t *testing.T) {
		require.Equal(t, []uint32{1, 2}, f.inboxUIDs(searcher.Cond{
			Or: [][]*searcher.Cond{{seen, {}}},
		}))
	})
	t.Run("NotEmpty", func(t *testing.T) {
		// XXX: NOT of an empty (always true) condition should match nothing,
		// but it has always been a no-op. Characterized as is.
		require.Equal(t, []uint32{1, 2}, f.inboxUIDs(searcher.Cond{Not: []*searcher.Cond{{}}}))
	})
}
