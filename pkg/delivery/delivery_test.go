package delivery

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/foxcpp/maddy-storage/internal/domain/account"
	accountsqlite "github.com/foxcpp/maddy-storage/internal/domain/account/repository/sqlite"
	accountusecase "github.com/foxcpp/maddy-storage/internal/domain/account/usecase"
	storememory "github.com/foxcpp/maddy-storage/internal/domain/blob/store/memory"
	changelogsqlite "github.com/foxcpp/maddy-storage/internal/domain/changelog/repository/sqlite"
	"github.com/foxcpp/maddy-storage/internal/domain/folder"
	foldersql "github.com/foxcpp/maddy-storage/internal/domain/folder/repository/sqlcommon"
	foldersqlite "github.com/foxcpp/maddy-storage/internal/domain/folder/repository/sqlite"
	folderusecase "github.com/foxcpp/maddy-storage/internal/domain/folder/usecase"
	messagesqlite "github.com/foxcpp/maddy-storage/internal/domain/message/repository/sqlite"
	searchersqlcommon "github.com/foxcpp/maddy-storage/internal/domain/message/searcher/metaonly/sqlcommon"
	messageusecase "github.com/foxcpp/maddy-storage/internal/domain/message/usecase"
	"github.com/foxcpp/maddy-storage/internal/pkg/contextlib"
	"github.com/foxcpp/maddy-storage/internal/repository/sqlite"
	"github.com/foxcpp/maddy-storage/tests/imap2/utils"
	"github.com/stretchr/testify/assert"
	"go.uber.org/zap/zaptest"
)

var testMessageNoCT = utils.CRLF(`From: User4 <user4@domain.org>
Date: Sat, 24 Mar 2007 23:00:00 +0200
Subject: s4444

body4444
`)

func initTestContainer(t *testing.T) (*Container, *storememory.Store) {
	db, err := sqlite.NewMemory(sqlite.Cfg{})
	if err != nil {
		t.Fatal(err)
	}
	blobStore := storememory.New()

	acctRepo := accountsqlite.New(db)
	folderRepo := foldersql.New(db)
	imapRepo := foldersqlite.New(db)
	msgRepo := messagesqlite.New(db)
	changelogRepo := changelogsqlite.New(db)

	acctUC := accountusecase.NewAccount(
		accountusecase.Cfg{},
		acctRepo,
		folderRepo,
		imapRepo,
		accountusecase.StubAuth{},
		changelogRepo,
	)

	searcher := searchersqlcommon.New(db, searchersqlcommon.Cfg{})

	msgUC := messageusecase.New(
		messageusecase.Config{},
		folderRepo, imapRepo, msgRepo,
		searcher,
		blobStore, blobStore,
		changelogRepo,
	)

	foldUC := folderusecase.New(
		folderRepo, imapRepo,
		changelogRepo, searcher,
	)

	c := NewContainer(
		Config{},
		zaptest.NewLogger(t),
		acctUC,
		foldUC,
		msgUC,
	)

	return c, blobStore
}

func TestDeliverySimple(t *testing.T) {
	ctx := context.Background()

	c, _ := initTestContainer(t)

	acct1, err := c.acct.Create(ctx, "test_account1")
	if err != nil {
		t.Fatal(err)
	}
	_, err = c.folder.Create(ctx, acct1.ID, "Spam", folder.RoleJunk, false)
	if err != nil {
		t.Fatal(err)
	}

	acct2, err := c.acct.Create(ctx, "test_account2")
	if err != nil {
		t.Fatal(err)
	}

	d, err := c.StartDelivery(ctx, "DELIVERY_ID")
	assert.NoError(t, err, "StartDelivery failed")

	d.SetFlags([]string{"custom_flag"})
	assert.NoError(t, d.AddRcpt(ctx, "test_account1", RcptOpts{}))
	assert.NoError(t, d.AddRcpt(ctx, "test_account2", RcptOpts{}))
	assert.NoError(t, d.SelectFolders(ctx, folder.RoleInbox))
	assert.NoError(t, d.PrepareBody(ctx, int64(len(testMessageNoCT)), strings.NewReader(testMessageNoCT)))
	assert.NoError(t, d.Commit(ctx))

	assert.NoError(t, d.Close(ctx), "Close after Commit must not fail")

	for _, acct := range []*account.Account{acct1, acct2} {
		foldData, err := c.msg.FetchFolderInfo(ctx, acct.ID, folder.FolderINBOX, messageusecase.InfoOpts{
			ReturnMaxUID: true,
			CountMsgs:    true,
		})
		assert.NoError(t, err)
		assert.EqualValues(t, foldData.Msgs, 1)

		msg, err := c.msg.Fetch(ctx, acct.ID, foldData.Folder.ID, folder.Range{
			Values: []uint32{foldData.MaxUID},
		}, folder.ModSeq(0), false)
		assert.NoError(t, err)
		assert.NotNil(t, msg)
		assert.Len(t, msg, 1)

		assert.Equal(t, msg[0].Msg.Flags, []string{"custom_flag"})
		assert.Equal(t, msg[0].Msg.Meta["delivery_id"], "DELIVERY_ID")

		var buf bytes.Buffer
		assert.NoError(t, c.msg.WritePart(
			ctx, &msg[0].Msg, msg[0].Msg.Parts[0].Path,
			&buf, messageusecase.WriteOptions{
				Specifier: messageusecase.PartDefault,
			},
		))
		assert.Equal(t, buf.String(), testMessageNoCT)
	}
}

func TestDeliveryRole(t *testing.T) {
	ctx := contextlib.WithLogger(context.Background(), zaptest.NewLogger(t))

	c, _ := initTestContainer(t)

	acct1, err := c.acct.Create(ctx, "test_account1")
	if err != nil {
		t.Fatal(err)
	}
	_, err = c.folder.Create(ctx, acct1.ID, "Spam", folder.RoleJunk, false)
	if err != nil {
		t.Fatal(err)
	}

	acct2, err := c.acct.Create(ctx, "test_account2")
	if err != nil {
		t.Fatal(err)
	}

	d, err := c.StartDelivery(ctx, "DELIVERY_ID")
	assert.NoError(t, err, "StartDelivery failed")

	d.SetFlags([]string{"custom_flag"})
	assert.NoError(t, d.AddRcpt(ctx, "test_account1", RcptOpts{}))
	assert.NoError(t, d.AddRcpt(ctx, "test_account2", RcptOpts{}))
	assert.NoError(t, d.SelectFolders(ctx, folder.RoleJunk))
	assert.NoError(t, d.PrepareBody(ctx, int64(len(testMessageNoCT)), strings.NewReader(testMessageNoCT)))
	assert.NoError(t, d.Commit(ctx))

	assert.NoError(t, d.Close(ctx), "Close after Commit must not fail")

	checkMsg := func(acct *account.Account, foldData *messageusecase.FolderInfo) {
		msg, err := c.msg.Fetch(ctx, acct.ID, foldData.Folder.ID, folder.Range{
			Values: []uint32{foldData.MaxUID},
		}, folder.ModSeq(0), false)
		assert.NoError(t, err)
		assert.NotNil(t, msg)
		assert.Len(t, msg, 1)

		assert.Equal(t, msg[0].Msg.Flags, []string{"custom_flag"})
		assert.Equal(t, msg[0].Msg.Meta["delivery_id"], "DELIVERY_ID")

		var buf bytes.Buffer
		assert.NoError(t, c.msg.WritePart(
			ctx, &msg[0].Msg, msg[0].Msg.Parts[0].Path,
			&buf, messageusecase.WriteOptions{
				Specifier: messageusecase.PartDefault,
			},
		))
		assert.Equal(t, buf.String(), testMessageNoCT)
	}

	fold1Data, err := c.msg.FetchFolderInfo(ctx, acct1.ID, "Spam", messageusecase.InfoOpts{
		ReturnMaxUID: true,
		CountMsgs:    true,
	})
	assert.NoError(t, err)
	assert.EqualValues(t, fold1Data.Msgs, 1)
	checkMsg(acct1, &fold1Data)

	// No RoleJunk folder available, fallback to Inbox.
	fold2Data, err := c.msg.FetchFolderInfo(ctx, acct2.ID, folder.FolderINBOX, messageusecase.InfoOpts{
		ReturnMaxUID: true,
		CountMsgs:    true,
	})
	assert.NoError(t, err)
	assert.EqualValues(t, fold2Data.Msgs, 1)
	checkMsg(acct2, &fold2Data)
}

func TestDeliveryAdditionalPremable(t *testing.T) {
	ctx := contextlib.WithLogger(context.Background(), zaptest.NewLogger(t))

	c, _ := initTestContainer(t)

	acct1, err := c.acct.Create(ctx, "test_account1")
	if err != nil {
		t.Fatal(err)
	}
	_, err = c.folder.Create(ctx, acct1.ID, "Spam", folder.RoleJunk, false)
	if err != nil {
		t.Fatal(err)
	}

	acct2, err := c.acct.Create(ctx, "test_account2")
	if err != nil {
		t.Fatal(err)
	}

	d, err := c.StartDelivery(ctx, "DELIVERY_ID")
	assert.NoError(t, err, "StartDelivery failed")

	d.SetFlags([]string{"custom_flag"})
	assert.NoError(t, d.AddRcpt(ctx, "test_account1", RcptOpts{
		AdditionalPremable: []byte("X-Delivered-To: test_account1\r\n"),
	}))
	assert.NoError(t, d.AddRcpt(ctx, "test_account2", RcptOpts{
		AdditionalPremable: []byte("X-Delivered-To: test_account2\r\n"),
	}))
	assert.NoError(t, d.SelectFolders(ctx, folder.RoleJunk))
	assert.NoError(t, d.PrepareBody(ctx, int64(len(testMessageNoCT)), strings.NewReader(testMessageNoCT)))
	assert.NoError(t, d.Commit(ctx))

	checkMsg := func(acct *account.Account, foldData *messageusecase.FolderInfo) {
		t.Helper()

		msg, err := c.msg.Fetch(ctx, acct.ID, foldData.Folder.ID, folder.Range{
			Values: []uint32{foldData.MaxUID},
		}, folder.ModSeq(0), false)
		assert.NoError(t, err)
		assert.NotNil(t, msg)
		assert.Len(t, msg, 1)

		assert.Equal(t, msg[0].Msg.Flags, []string{"custom_flag"})
		assert.Equal(t, msg[0].Msg.Meta["delivery_id"], "DELIVERY_ID")

		var buf bytes.Buffer
		assert.NoError(t, c.msg.WritePart(
			ctx, &msg[0].Msg, msg[0].Msg.Parts[0].Path,
			&buf, messageusecase.WriteOptions{
				Specifier: messageusecase.PartDefault,
			},
		))
		assert.Equal(t, buf.String(), "X-Delivered-To: "+acct.Name+"\r\n"+testMessageNoCT)
	}

	fold1Data, err := c.msg.FetchFolderInfo(ctx, acct1.ID, "Spam", messageusecase.InfoOpts{
		ReturnMaxUID: true,
		CountMsgs:    true,
	})
	assert.NoError(t, err)
	assert.EqualValues(t, fold1Data.Msgs, 1)
	checkMsg(acct1, &fold1Data)

	// No RoleJunk folder available, fallback to Inbox.
	fold2Data, err := c.msg.FetchFolderInfo(ctx, acct2.ID, folder.FolderINBOX, messageusecase.InfoOpts{
		ReturnMaxUID: true,
		CountMsgs:    true,
	})
	assert.NoError(t, err)
	assert.EqualValues(t, fold2Data.Msgs, 1)
	checkMsg(acct2, &fold2Data)
}
