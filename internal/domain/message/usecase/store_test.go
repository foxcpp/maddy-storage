package messageusecase

import (
	"context"
	"io"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/foxcpp/maddy-storage/internal/domain/account"
	accountsqlite "github.com/foxcpp/maddy-storage/internal/domain/account/repository/sqlite"
	"github.com/foxcpp/maddy-storage/internal/domain/blob"
	storememory "github.com/foxcpp/maddy-storage/internal/domain/blob/store/memory"
	changelogsqlite "github.com/foxcpp/maddy-storage/internal/domain/changelog/repository/sqlite"
	"github.com/foxcpp/maddy-storage/internal/domain/folder"
	foldersqlite "github.com/foxcpp/maddy-storage/internal/domain/folder/repository/sqlite"
	"github.com/foxcpp/maddy-storage/internal/domain/message"
	messagesqlite "github.com/foxcpp/maddy-storage/internal/domain/message/repository/sqlite"
	"github.com/foxcpp/maddy-storage/internal/domain/metadata"
	"github.com/foxcpp/maddy-storage/internal/pkg/contextlog"
	"github.com/foxcpp/maddy-storage/internal/repository/sqlite"
	"github.com/foxcpp/maddy-storage/internal/usecase"
	"github.com/oklog/ulid/v2"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap/zaptest"
)

func initMessageTestUsecase(t *testing.T) (Usecase, blob.Store, *account.Account, *folder.Folder) {
	db, err := sqlite.NewMemory(sqlite.Cfg{})
	if err != nil {
		t.Fatal(err)
	}
	blobStore := storememory.New()

	acctRepo := accountsqlite.New(db)
	folderRepo := foldersqlite.New(db)
	msgRepo := messagesqlite.New(db)
	changelogRepo := changelogsqlite.New(db)

	acctUC := usecase.NewAccount(acctRepo, usecase.StubAuth{}, changelogRepo)
	acct, err := acctUC.Create(context.Background(), "test_account")
	if err != nil {
		t.Fatal(err)
	}

	folderUC := usecase.NewFolder(folderRepo, changelogRepo)
	inbox, err := folderUC.Create(context.Background(), acct.ID_, "INBOX", folder.RoleNone, false)
	if err != nil {
		t.Fatal(err)
	}

	uc := New(
		Config{},
		folderRepo, msgRepo,
		blobStore, blobStore,
		changelogRepo)

	return uc, blobStore, acct, inbox
}

func TestMessage_CreateMessage(t *testing.T) {
	uc, blobs, acct, _ := initMessageTestUsecase(t)
	now := time.Now().In(time.UTC)

	cases := []struct {
		Name      string
		Text      string
		Data      *message.Msg
		PartBlobs map[string]string // by stringified path
	}{
		{
			Name: "no ct plain text",
			Text: testMessageNoCT,
			Data: &message.Msg{
				ReceivedAt_: now,
				Meta_:       metadata.New(),
				Flags_:      []string{"$testFlag"},
				Content_:    &message.ContentData{},
				Parts_: []message.Part{
					{
						Path_: message.EmptyPath(),
						Content_: &message.ContentPartData{
							Type:           "text/plain",
							HeaderSize:     uint32(len(strings.ReplaceAll(testMessageNoCT, "\n", "\r\n")) - 8),
							HeaderNumLines: 4,
							Size:           8,
							NumLines:       1,
							Envelope: &message.ContentEnvelope{
								Date: time.Date(2007, time.March, 24, 23, 0, 00, 00,
									time.FixedZone("", 2*3600)),
								Subject: "s4444",
								From:    []*message.Address{{Name: "User4", Address: "user4@domain.org"}},
								Sender:  []*message.Address{{Name: "User4", Address: "user4@domain.org"}},
								ReplyTo: []*message.Address{{Name: "User4", Address: "user4@domain.org"}},
							},
						},
					},
				},
			},
			PartBlobs: map[string]string{
				"": testMessageNoCT,
			},
		},
		{
			Name: "text plain envelope",
			Text: testMessageEnvelope,
			Data: &message.Msg{
				ReceivedAt_: now,
				Meta_:       metadata.New(),
				Flags_:      []string{"$testFlag"},
				Content_:    &message.ContentData{},
				Parts_: []message.Part{
					{
						Path_: message.EmptyPath(),
						Content_: &message.ContentPartData{
							Type:           "text/plain",
							HeaderSize:     uint32(len(strings.ReplaceAll(testMessageEnvelope, "\n", "\r\n")) - 4),
							HeaderNumLines: 11,
							Size:           4,
							NumLines:       1,
							Envelope: &message.ContentEnvelope{
								Date:      time.Date(2007, time.February, 15, 1, 2, 3, 0, time.FixedZone("", 2*3600)),
								Subject:   "subject header",
								From:      []*message.Address{{Name: "From Real", Address: "fromuser@fromdomain.org"}},
								Sender:    []*message.Address{{Name: "Sender Real", Address: "senderuser@senderdomain.org"}},
								ReplyTo:   []*message.Address{{Name: "ReplyTo Real", Address: "replytouser@replytodomain.org"}},
								To:        []*message.Address{{Name: "To Real", Address: "touser@todomain.org"}},
								Cc:        []*message.Address{{Name: "Cc Real", Address: "ccuser@ccdomain.org"}},
								Bcc:       []*message.Address{{Name: "Bcc Real", Address: "bccuser@bccdomain.org"}},
								InReplyTo: []string{"reply@to.id"},
								MessageID: "msg@id",
							},
						},
					},
				},
			},
			PartBlobs: map[string]string{
				"": testMessageEnvelope,
			},
		},
		{
			Name: "text plain malformed envelope",
			Text: testMessageMalformedEnvelope,
			Data: &message.Msg{
				ReceivedAt_: now,
				Meta_:       metadata.New(),
				Flags_:      []string{"$testFlag"},
				Content_:    &message.ContentData{},
				Parts_: []message.Part{
					{
						Path_: message.EmptyPath(),
						Content_: &message.ContentPartData{
							Type:           "text/plain",
							HeaderSize:     uint32(len(strings.ReplaceAll(testMessageMalformedEnvelope, "\n", "\r\n")) - 4),
							HeaderNumLines: 5,
							Size:           4,
							NumLines:       1,
							Envelope: &message.ContentEnvelope{
								Date:    time.Date(2007, time.February, 15, 1, 2, 3, 0, time.FixedZone("", 2*3600)),
								From:    []*message.Address{{Name: "Real Name", Address: "user@domain"}},
								Sender:  []*message.Address{{Name: "Real Name", Address: "user@domain"}},
								ReplyTo: []*message.Address{{Name: "Real Name", Address: "user@domain"}},
							},
						},
					},
				},
			},
			PartBlobs: map[string]string{
				"": testMessageMalformedEnvelope,
			},
		},
		{
			Name: "multipart mixed",
			Text: testMessageMultipart,
			Data: &message.Msg{
				ReceivedAt_: now,
				Meta_:       metadata.New(),
				Flags_:      []string{"$testFlag"},
				Content_:    &message.ContentData{},
				Parts_: []message.Part{
					{
						Path_: message.EmptyPath(),
						Order: 0,
						Content_: &message.ContentPartData{
							Type: "multipart/mixed",
							Params: map[string]string{
								"boundary": "foo bar",
							},
							HeaderSize:     136,
							HeaderNumLines: 6,
							Envelope: &message.ContentEnvelope{
								Date:    time.Date(2007, time.March, 24, 23, 0, 0, 0, time.FixedZone("", 2*3600)),
								From:    []*message.Address{{Name: "", Address: "user@domain.org"}},
								Sender:  []*message.Address{{Name: "", Address: "user@domain.org"}},
								ReplyTo: []*message.Address{{Name: "", Address: "user@domain.org"}},
							},
						},
					},
					{
						Path_: []int{1},
						Order: 1,
						Content_: &message.ContentPartData{
							Type: "text/x-myown",
							Params: map[string]string{
								"charset": "us-ascii",
							},
							HeaderSize:     48,
							HeaderNumLines: 2,
							Size:           7,
							NumLines:       1,
						},
					},
				},
			},
			PartBlobs: map[string]string{
				"": `From: user@domain.org
Date: Sat, 24 Mar 2007 23:00:00 +0200
Mime-Version: 1.0
Content-Type: multipart/mixed; boundary="foo
 bar"

`,
				"1": `Content-Type: text/x-myown; charset=us-ascii

hello
`,
			},
		},
		{
			Name: "multipart rfc822",
			Text: testMessageMultipartRFC822,
			Data: &message.Msg{
				ReceivedAt_: now,
				Meta_:       metadata.New(),
				Flags_:      []string{"$testFlag"},
				Content_:    &message.ContentData{},
				Parts_: []message.Part{
					{
						Path_: message.EmptyPath(),
						Order: 0,
						Content_: &message.ContentPartData{
							Type: "multipart/mixed",
							Params: map[string]string{
								"boundary": "foo bar",
							},
							HeaderSize:     136,
							HeaderNumLines: 6,
							Envelope: &message.ContentEnvelope{
								Date: time.Date(2007, time.March, 24, 23, 0, 0, 0,
									time.FixedZone("", 2*3600)),
								From:    []*message.Address{{Name: "", Address: "user@domain.org"}},
								Sender:  []*message.Address{{Name: "", Address: "user@domain.org"}},
								ReplyTo: []*message.Address{{Name: "", Address: "user@domain.org"}},
							},
						},
					},
					{
						Path_: []int{1},
						Order: 1,
						Content_: &message.ContentPartData{
							Type: "text/x-myown",
							Params: map[string]string{
								"charset": "us-ascii",
							},
							HeaderSize:     48,
							HeaderNumLines: 2,
							Size:           7,
							NumLines:       1,
						},
					},
					{
						Path_: []int{2},
						Order: 2,
						Content_: &message.ContentPartData{
							Type:           "message/rfc822",
							HeaderSize:     32,
							HeaderNumLines: 2,
							Size:           134,
							NumLines:       5,
							Nested: &message.ContentPartData{
								Type: "multipart/alternative",
								Params: map[string]string{
									"boundary": "sub1",
								},
								HeaderSize:     134,
								HeaderNumLines: 5,
								Envelope: &message.ContentEnvelope{
									Date: time.Date(
										2012, time.August, 12, 12, 34, 56, 0,
										time.FixedZone("", 3*3600),
									),
									Subject: "submsg",
									From:    []*message.Address{{Name: "", Address: "sub@domain.org"}},
									Sender:  []*message.Address{{Name: "", Address: "sub@domain.org"}},
									ReplyTo: []*message.Address{{Name: "", Address: "sub@domain.org"}},
								},
							},
						},
					},
					{
						Path_: []int{2, 1},
						Order: 3,
						Content_: &message.ContentPartData{
							Type:           "text/html",
							HeaderSize:     27,
							HeaderNumLines: 2,
							Size:           20,
							NumLines:       1,
						},
					},
					{
						Path_: []int{2, 2},
						Order: 4,
						Content_: &message.ContentPartData{
							Type:           "text/plain",
							HeaderSize:     28,
							HeaderNumLines: 2,
							Size:           21,
							NumLines:       1,
						},
					},
				},
			},
			PartBlobs: map[string]string{
				"": `From: user@domain.org
Date: Sat, 24 Mar 2007 23:00:00 +0200
Mime-Version: 1.0
Content-Type: multipart/mixed; boundary="foo
 bar"

`,
				"1": `Content-Type: text/x-myown; charset=us-ascii

hello
`,
				"2": `Content-Type: message/rfc822

From: sub@domain.org
Date: Sun, 12 Aug 2012 12:34:56 +0300
Subject: submsg
Content-Type: multipart/alternative; boundary="sub1"

`,
				"2.1": `Content-Type: text/html

<p>Hello world</p>
`,
				"2.2": `Content-Type: text/plain

Hello another world
`,
			},
		},
		{
			Name: "rfc822",
			Text: testMessageRFC822,
			Data: &message.Msg{
				ReceivedAt_: now,
				Meta_:       metadata.New(),
				Flags_:      []string{"$testFlag"},
				Content_:    &message.ContentData{},
				Parts_: []message.Part{
					{
						Path_: message.EmptyPath(),
						Order: 0,
						Content_: &message.ContentPartData{
							Type:           "message/rfc822",
							HeaderSize:     113,
							HeaderNumLines: 5,
							Envelope: &message.ContentEnvelope{
								Date: time.Date(2007, time.March, 24, 23, 0, 0, 0,
									time.FixedZone("", 2*3600)),
								From:    []*message.Address{{Name: "", Address: "user@domain.org"}},
								Sender:  []*message.Address{{Name: "", Address: "user@domain.org"}},
								ReplyTo: []*message.Address{{Name: "", Address: "user@domain.org"}},
							},
						},
					},
					{
						Path_: []int{1},
						Order: 1,
						Content_: &message.ContentPartData{
							Type:           "text/plain",
							HeaderSize:     80,
							HeaderNumLines: 4,
							Size:           11,
							NumLines:       1,
							Envelope: &message.ContentEnvelope{
								Date: time.Date(2012, time.August, 12, 12, 34, 56, 0,
									time.FixedZone("", 3*3600)),
								Subject: "submsg",
								From:    []*message.Address{{Name: "", Address: "sub@domain.org"}},
								Sender:  []*message.Address{{Name: "", Address: "sub@domain.org"}},
								ReplyTo: []*message.Address{{Name: "", Address: "sub@domain.org"}},
							},
						},
					},
				},
			},
			PartBlobs: map[string]string{
				"": `From: user@domain.org
Date: Sat, 24 Mar 2007 23:00:00 +0200
Mime-Version: 1.0
Content-Type: message/rfc822

`,
				"1": `From: sub@domain.org
Date: Sun, 12 Aug 2012 12:34:56 +0300
Subject: submsg

Hello world`,
			},
		},
		{
			Name: "double rfc822",
			Text: testMessageRFC822Double,
			Data: &message.Msg{
				ReceivedAt_: now,
				Meta_:       metadata.New(),
				Flags_:      []string{"$testFlag"},
				Content_:    &message.ContentData{},
				Parts_: []message.Part{
					{
						Path_: message.EmptyPath(),
						Order: 0,
						Content_: &message.ContentPartData{
							Type:           "message/rfc822",
							HeaderSize:     113,
							HeaderNumLines: 5,
							Envelope: &message.ContentEnvelope{
								Date: time.Date(2007, time.March, 24, 23, 0, 0, 0,
									time.FixedZone("", 2*3600)),
								From:    []*message.Address{{Name: "", Address: "user@domain.org"}},
								Sender:  []*message.Address{{Name: "", Address: "user@domain.org"}},
								ReplyTo: []*message.Address{{Name: "", Address: "user@domain.org"}},
							},
						},
					},
					{
						Path_: []int{1},
						Order: 1,
						Content_: &message.ContentPartData{
							Type:           "message/rfc822",
							HeaderSize:     114,
							HeaderNumLines: 5,
							Envelope: &message.ContentEnvelope{
								Date: time.Date(2007, time.March, 23, 11, 22, 33, 0,
									time.FixedZone("", 2*3600)),
								From:    []*message.Address{{Name: "", Address: "user2@domain.org"}},
								Sender:  []*message.Address{{Name: "", Address: "user2@domain.org"}},
								ReplyTo: []*message.Address{{Name: "", Address: "user2@domain.org"}},
							},
						},
					},
					{
						Path_: []int{1, 1},
						Order: 2,
						Content_: &message.ContentPartData{
							Type:           "text/plain",
							HeaderSize:     80,
							HeaderNumLines: 4,
							Size:           11,
							NumLines:       1,
							Envelope: &message.ContentEnvelope{
								Date: time.Date(2012, time.August, 12, 12, 34, 56, 0,
									time.FixedZone("", 3*3600)),
								Subject: "submsg",
								From:    []*message.Address{{Name: "", Address: "sub@domain.org"}},
								Sender:  []*message.Address{{Name: "", Address: "sub@domain.org"}},
								ReplyTo: []*message.Address{{Name: "", Address: "sub@domain.org"}},
							},
						},
					},
				},
			},
			PartBlobs: map[string]string{
				"": `From: user@domain.org
Date: Sat, 24 Mar 2007 23:00:00 +0200
Mime-Version: 1.0
Content-Type: message/rfc822

`,
				"1": `From: user2@domain.org
Date: Fri, 23 Mar 2007 11:22:33 +0200
Mime-Version: 1.0
Content-Type: message/rfc822

`,
				"1.1": `From: sub@domain.org
Date: Sun, 12 Aug 2012 12:34:56 +0300
Subject: submsg

Hello world`,
			},
		},
	}
	for _, c := range cases {
		t.Run(c.Name, func(t *testing.T) {
			ctx := contextlog.WithLogger(context.Background(), zaptest.NewLogger(t))

			crlfText := strings.ReplaceAll(c.Text, "\n", "\r\n")

			msgData, err := uc.CreateMessage(ctx, acct.ID_, "INBOX",
				now, []string{"$testFlag"}, int64(len(crlfText)), strings.NewReader(crlfText))
			if err != nil {
				t.Fatal(err)
			}

			gotMsg := msgData.Msg

			gotParts := make(map[string]string)
			for _, p := range gotMsg.Parts_ {
				blob := p.Inline_
				if blob == nil {
					r, err := blobs.Open(ctx, p.ExternalBlobID_)
					if err != nil {
						t.Fatalf("failed to open part %v blob: %v", p.Path_, err)
					}
					blob, err = io.ReadAll(r)
					if err != nil {
						t.Fatalf("failed to read part %v blob: %v", p.Path_, err)
					}
				}
				gotParts[p.Path_.String()] = strings.ReplaceAll(string(blob), "\r\n", "\n")
			}

			// clean volatile data
			gotMsg.ID_ = ulid.ULID{}
			gotMsg.CreatedAt_ = time.Time{}
			gotMsg.UpdatedAt_ = time.Time{}
			gotMsg.Meta_ = metadata.New()
			for i := range gotMsg.Parts_ {
				gotMsg.Parts_[i].ID_ = ulid.ULID{}
				gotMsg.Parts_[i].ExternalBlobID_ = ""
				gotMsg.Parts_[i].Inline_ = nil
			}
			sort.Slice(gotMsg.Parts_, func(i, j int) bool { return gotMsg.Parts_[i].Order < gotMsg.Parts_[j].Order })

			totalSize := uint32(0)
			totalLines := int64(0)
			for _, p := range gotMsg.Parts_ {
				totalSize += p.Content_.HeaderSize
				totalSize += p.Content_.Size
				totalLines += p.Content_.HeaderNumLines
				totalLines += p.Content_.NumLines
			}
			require.Equal(t, len(crlfText), int(totalSize), "total octet size is wrong")
			require.Equal(t, strings.Count(crlfText, "\r\n"), int(totalLines), "total line count is wrong")

			require.Equal(t, c.Data, gotMsg)
			for partPath, partContents := range gotParts {
				expectedPart := c.PartBlobs[partPath]
				require.Equal(t, expectedPart, partContents, "message part [%v]", partPath)
			}
		})
	}
}
