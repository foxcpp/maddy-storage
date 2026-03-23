package messageusecase

import (
	"bytes"
	"context"
	"io"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/foxcpp/maddy-storage/internal/domain/account"
	accountsqlite "github.com/foxcpp/maddy-storage/internal/domain/account/repository/sqlite"
	accountusecase "github.com/foxcpp/maddy-storage/internal/domain/account/usecase"
	storememory "github.com/foxcpp/maddy-storage/internal/domain/blob/store/memory"
	changelogsqlite "github.com/foxcpp/maddy-storage/internal/domain/changelog/repository/sqlite"
	foldersql "github.com/foxcpp/maddy-storage/internal/domain/folder/repository/sqlcommon"
	foldersqlite "github.com/foxcpp/maddy-storage/internal/domain/folder/repository/sqlite"
	"github.com/foxcpp/maddy-storage/internal/domain/message"
	messagesqlite "github.com/foxcpp/maddy-storage/internal/domain/message/repository/sqlite"
	searchersqlcommon "github.com/foxcpp/maddy-storage/internal/domain/message/searcher/metaonly/sqlcommon"
	"github.com/foxcpp/maddy-storage/internal/domain/metadata"
	"github.com/foxcpp/maddy-storage/internal/pkg/contextlib"
	"github.com/foxcpp/maddy-storage/internal/repository/sqlite"
	"github.com/oklog/ulid/v2"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap/zaptest"
)

func initMessageTestUsecase(t *testing.T) (Usecase, *storememory.Store, *account.Account) {
	ctx := contextlib.WithLogger(context.Background(), zaptest.NewLogger(t))

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
	acct, err := acctUC.Create(ctx, "test_account")
	if err != nil {
		t.Fatal(err)
	}

	searcher := searchersqlcommon.New(db, searchersqlcommon.Cfg{})

	uc := New(
		Config{},
		folderRepo, imapRepo, msgRepo,
		searcher,
		blobStore, blobStore,
		changelogRepo,
	)

	return uc, blobStore, acct
}

func TestMessage_CreateMessage(t *testing.T) {
	uc, blobs, acct := initMessageTestUsecase(t)
	now := time.Now().In(time.UTC)

	cases := []struct {
		Skip      bool
		Name      string
		Text      string
		Data      *message.Msg
		PartBlobs map[string]string // by stringified path
	}{
		{
			Name: "no ct plain text",
			Text: testMessageNoCT,
			Data: &message.Msg{
				ReceivedAt: now,
				Meta:       metadata.New(),
				Flags:      []string{"$testFlag"},
				TotalSize:  99,
				Content:    &message.ContentData{},
				Parts: []message.Part{
					{
						Path: message.EmptyPath(),
						Content: &message.ContentPartData{
							Type:         "text/plain",
							HeaderSize:   uint32(len(strings.ReplaceAll(testMessageNoCT, "\n", "\r\n")) - 10),
							HeaderLines:  4,
							ContentSize:  10,
							ContentLines: 1,
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
				ReceivedAt: now,
				Meta:       metadata.New(),
				Flags:      []string{"$testFlag"},
				TotalSize:  381,
				Content:    &message.ContentData{},
				Parts: []message.Part{
					{
						Path: message.EmptyPath(),
						Content: &message.ContentPartData{
							Type:         "text/plain",
							HeaderSize:   uint32(len(strings.ReplaceAll(testMessageEnvelope, "\n", "\r\n")) - 6),
							HeaderLines:  11,
							ContentSize:  6,
							ContentLines: 1,
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
				ReceivedAt: now,
				Meta:       metadata.New(),
				Flags:      []string{"$testFlag"},
				TotalSize:  100,
				Content:    &message.ContentData{},
				Parts: []message.Part{
					{
						Path: message.EmptyPath(),
						Content: &message.ContentPartData{
							Type:         "text/plain",
							HeaderSize:   uint32(len(strings.ReplaceAll(testMessageMalformedEnvelope, "\n", "\r\n")) - 6),
							HeaderLines:  5,
							ContentSize:  6,
							ContentLines: 1,
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
				ReceivedAt: now,
				Meta:       metadata.New(),
				Flags:      []string{"$testFlag"},
				TotalSize:  217,
				Content:    &message.ContentData{},
				Parts: []message.Part{
					{
						Path:  message.EmptyPath(),
						Order: 0,
						Content: &message.ContentPartData{
							Type: "multipart/mixed",
							Params: map[string]string{
								"boundary": "foo bar",
							},
							HeaderSize:     136,
							HeaderLines:    6,
							MultipartSize:  26,
							MultipartLines: 2,
							Envelope: &message.ContentEnvelope{
								Date:    time.Date(2007, time.March, 24, 23, 0, 0, 0, time.FixedZone("", 2*3600)),
								From:    []*message.Address{{Name: "", Address: "user@domain.org"}},
								Sender:  []*message.Address{{Name: "", Address: "user@domain.org"}},
								ReplyTo: []*message.Address{{Name: "", Address: "user@domain.org"}},
							},
						},
					},
					{
						Path:  []int{1},
						Order: 1,
						Content: &message.ContentPartData{
							IsMIMEPart: true,
							Type:       "text/x-myown",
							Params: map[string]string{
								"charset": "us-ascii",
							},
							HeaderSize:     48,
							HeaderLines:    2,
							MultipartLines: 1, // hack
							ContentSize:    7,
							ContentLines:   1,
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
			Text: testMessageMultipartRFC822NoPrologueEpilogue,
			Data: &message.Msg{
				ReceivedAt: now,
				Meta:       metadata.New(),
				Flags:      []string{"$testFlag"},
				TotalSize:  522,
				Content:    &message.ContentData{},
				Parts: []message.Part{
					{
						Path:  message.EmptyPath(),
						Order: 0,
						Content: &message.ContentPartData{
							Type: "multipart/mixed",
							Params: map[string]string{
								"boundary": "foo bar",
							},
							HeaderSize:     136,
							HeaderLines:    6,
							MultipartSize:  39,
							MultipartLines: 3,
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
						Path:  []int{1},
						Order: 1,
						Content: &message.ContentPartData{
							IsMIMEPart: true,
							Type:       "text/x-myown",
							Params: map[string]string{
								"charset": "us-ascii",
							},
							HeaderSize:     48,
							HeaderLines:    2,
							MultipartLines: 1,
							ContentSize:    7,
							ContentLines:   1,
						},
					},
					{
						Path:  []int{2},
						Order: 2,
						Content: &message.ContentPartData{
							IsMIMEPart:     true,
							Type:           "message/rfc822",
							HeaderSize:     32,
							HeaderLines:    2,
							MultipartSize:  30,
							MultipartLines: 4,
							ContentSize:    134,
							ContentLines:   5,
							Nested: &message.ContentPartData{
								Type: "multipart/alternative",
								Params: map[string]string{
									"boundary": "sub1",
								},
								HeaderSize:  134,
								HeaderLines: 5,
								Envelope: &message.ContentEnvelope{
									Date: time.Date(
										2012, time.August, 12, 12, 34, 56, 0,
										time.FixedZone("", 4*3600),
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
						Path:  []int{2, 1},
						Order: 3,
						Content: &message.ContentPartData{
							IsMIMEPart:     true,
							Type:           "text/html",
							HeaderSize:     27,
							HeaderLines:    2,
							MultipartLines: 1,
							ContentSize:    20,
							ContentLines:   1,
						},
					},
					{
						Path:  []int{2, 2},
						Order: 4,
						Content: &message.ContentPartData{
							IsMIMEPart:     true,
							Type:           "text/plain",
							HeaderSize:     28,
							HeaderLines:    2,
							MultipartLines: 1,
							ContentSize:    21,
							ContentLines:   1,
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
Date: Sun, 12 Aug 2012 12:34:56 +0400
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
				ReceivedAt: now,
				Meta:       metadata.New(),
				Flags:      []string{"$testFlag"},
				TotalSize:  206,
				Content:    &message.ContentData{},
				Parts: []message.Part{
					{
						Path:  message.EmptyPath(),
						Order: 0,
						Content: &message.ContentPartData{
							Type:        "message/rfc822",
							HeaderSize:  113,
							HeaderLines: 5,
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
						Path:  []int{1},
						Order: 1,
						Content: &message.ContentPartData{
							Type:         "text/plain",
							HeaderSize:   80,
							HeaderLines:  4,
							ContentSize:  13,
							ContentLines: 1,
							Envelope: &message.ContentEnvelope{
								Date: time.Date(2012, time.August, 12, 12, 34, 56, 0,
									time.FixedZone("", 4*3600)),
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
Date: Sun, 12 Aug 2012 12:34:56 +0400
Subject: submsg

Hello world
`,
			},
		},
		{
			Name: "double rfc822",
			Text: testMessageRFC822Double,
			Data: &message.Msg{
				ReceivedAt: now,
				Meta:       metadata.New(),
				Flags:      []string{"$testFlag"},
				TotalSize:  320,
				Content:    &message.ContentData{},
				Parts: []message.Part{
					{
						Path:  message.EmptyPath(),
						Order: 0,
						Content: &message.ContentPartData{
							Type:        "message/rfc822",
							HeaderSize:  113,
							HeaderLines: 5,
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
						Path:  []int{1},
						Order: 1,
						Content: &message.ContentPartData{
							Type:        "message/rfc822",
							HeaderSize:  114,
							HeaderLines: 5,
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
						Path:  []int{1, 1},
						Order: 2,
						Content: &message.ContentPartData{
							Type:         "text/plain",
							HeaderSize:   80,
							HeaderLines:  4,
							ContentSize:  13,
							ContentLines: 1,
							Envelope: &message.ContentEnvelope{
								Date: time.Date(2012, time.August, 12, 12, 34, 56, 0,
									time.FixedZone("", 4*3600)),
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
Date: Sun, 12 Aug 2012 12:34:56 +0400
Subject: submsg

Hello world
`,
			},
		},
		{
			Name: "rfc822 in mime in rfc822",
			Text: testMessageMultipartRFC822Digest,
			Data: &message.Msg{
				ReceivedAt: now,
				Meta:       metadata.New(),
				Flags:      []string{"$testFlag"},
				TotalSize:  383,
				Content:    &message.ContentData{},
				Parts: []message.Part{
					{
						Path:  message.EmptyPath(),
						Order: 0,
						Content: &message.ContentPartData{
							Type:        "message/rfc822",
							HeaderSize:  113,
							HeaderLines: 5,
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
						Path:  []int{1},
						Order: 1,
						Content: &message.ContentPartData{
							Type: "multipart/digest",
							Params: map[string]string{
								"boundary": "foo",
							},
							HeaderSize:     128,
							HeaderLines:    5,
							MultipartSize:  27,
							MultipartLines: 3,
							Envelope: &message.ContentEnvelope{
								Date: time.Date(2012, time.August, 12, 12, 34, 56, 0,
									time.FixedZone("", 4*3600)),
								From:    []*message.Address{{Name: "", Address: "sub@domain.org"}},
								Sender:  []*message.Address{{Name: "", Address: "sub@domain.org"}},
								ReplyTo: []*message.Address{{Name: "", Address: "sub@domain.org"}},
								Subject: "submsg",
							},
						},
					},
					{
						Path:  []int{1, 1},
						Order: 2,
						Content: &message.ContentPartData{
							IsMIMEPart:     true,
							Type:           "message/rfc822",
							HeaderSize:     2,
							HeaderLines:    1,
							ContentSize:    46,
							ContentLines:   4,
							MultipartLines: 1,
							Nested: &message.ContentPartData{
								Type:         "text/plain",
								HeaderSize:   37,
								HeaderLines:  3,
								ContentSize:  9,
								ContentLines: 1,
								Envelope: &message.ContentEnvelope{
									Subject: "m1",
									From:    []*message.Address{{Name: "", Address: "m1@example.com"}},
									Sender:  []*message.Address{{Name: "", Address: "m1@example.com"}},
									ReplyTo: []*message.Address{{Name: "", Address: "m1@example.com"}},
								},
							},
						},
					},
					{
						Path:  []int{1, 2},
						Order: 3,
						Content: &message.ContentPartData{
							IsMIMEPart:     true,
							Type:           "message/rfc822",
							HeaderSize:     21,
							HeaderLines:    2,
							ContentSize:    46,
							ContentLines:   4,
							MultipartLines: 1,
							Nested: &message.ContentPartData{
								Type:         "text/plain",
								HeaderSize:   37,
								HeaderLines:  3,
								ContentSize:  9,
								ContentLines: 1,
								Envelope: &message.ContentEnvelope{
									Subject: "m2",
									From:    []*message.Address{{Name: "", Address: "m2@example.com"}},
									Sender:  []*message.Address{{Name: "", Address: "m2@example.com"}},
									ReplyTo: []*message.Address{{Name: "", Address: "m2@example.com"}},
								},
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
Date: Sun, 12 Aug 2012 12:34:56 +0400
Subject: submsg
Content-Type: multipart/digest; boundary="foo"

`,
				"1.1": `
From: m1@example.com
Subject: m1

m1 body
`,
				"1.2": `X-Mime: m2 header

From: m2@example.com
Subject: m2

m2 body
`,
			},
		},
	}
	for _, c := range cases {
		t.Run(c.Name, func(t *testing.T) {
			blobs.Clear()

			ctx := contextlib.WithLogger(context.Background(), zaptest.NewLogger(t))

			crlfText := strings.ReplaceAll(c.Text, "\n", "\r\n")

			msgData, err := uc.CreateMessage(ctx, acct.ID, "INBOX",
				now, []string{"$testFlag"}, int64(len(crlfText)), strings.NewReader(crlfText))
			if err != nil {
				t.Fatal(err)
			}

			gotMsg := msgData.Msg

			var externalBlobs []string
			gotParts := make(map[string]string)
			for _, p := range gotMsg.Parts {
				blob := p.Inline
				if blob == nil {
					r, err := blobs.Open(ctx, p.ExternalBlobID)
					if err != nil {
						t.Fatalf("failed to open part %v blob: %v", p.Path, err)
					}
					blob, err = io.ReadAll(r)
					if err != nil {
						t.Fatalf("failed to read part %v blob: %v", p.Path, err)
					}
					externalBlobs = append(externalBlobs, string(blob))
				}
				gotParts[p.Path.String()] = strings.ReplaceAll(string(blob), "\r\n", "\n")
			}

			// clean volatile data
			gotMsg.ID = ulid.ULID{}
			gotMsg.CreatedAt = time.Time{}
			gotMsg.UpdatedAt = time.Time{}
			gotMsg.ModSeq = 0
			gotMsg.CreatedAtModSeq = 0
			gotMsg.Meta = metadata.New()
			for i := range gotMsg.Parts {
				gotMsg.Parts[i].ID = ulid.ULID{}
				gotMsg.Parts[i].ExternalBlobID = ""
				gotMsg.Parts[i].Inline = nil
			}
			sort.Slice(gotMsg.Parts, func(i, j int) bool { return gotMsg.Parts[i].Order < gotMsg.Parts[j].Order })
			c.Data.Content.Envelope = c.Data.Parts[0].Content.Envelope

			if len(c.Data.Parts) == len(gotMsg.Parts) {
				// if parts count match - compare them separately
				// to have more detailed comparison
				for i := range c.Data.Parts {
					require.EqualExportedValues(
						t, c.Data.Parts[i], gotMsg.Parts[i],
						"message part indx %d contents mismatch",
						i,
					)
				}
			}
			require.EqualExportedValues(t, c.Data, gotMsg)
			for partPath, partContents := range gotParts {
				expectedPart := c.PartBlobs[partPath]
				require.Equal(t, expectedPart, partContents, "message part [%v]", partPath)
			}

			totalSize := uint32(0)
			totalLines := int64(0)
			for _, p := range gotMsg.Parts {
				totalSize += p.TotalSize()
				totalLines += p.TotalLines()
			}
			require.Equal(t, len(crlfText), int(totalSize), "total octet size is wrong")
			require.Equal(t, len(crlfText), int(gotMsg.TotalSize), "total octet size in msg is wrong")
			require.Equal(t, strings.Count(crlfText, "\r\n"), int(totalLines), "total line count is wrong")

			require.Equal(t, len(externalBlobs), blobs.Len(), "stored external blobs count != external blobs in message")
		})
	}
}

func TestMessage_CreateMessage_Lossless(t *testing.T) {
	uc, _, acct := initMessageTestUsecase(t)
	now := time.Now().In(time.UTC)

	cases := []struct {
		Name string
		Text string
	}{
		{
			Name: "no ct plain text",
			Text: testMessageNoCT,
		},
		{
			Name: "text plain envelope",
			Text: testMessageEnvelope,
		},
		{
			Name: "text plain malformed envelope",
			Text: testMessageMalformedEnvelope,
		},
		{
			Name: "multipart mixed",
			Text: testMessageMultipart,
		},
		{
			Name: "multipart rfc822",
			Text: testMessageMultipartRFC822NoPrologueEpilogue,
		},
		{
			Name: "rfc822",
			Text: testMessageRFC822,
		},
		{
			Name: "double rfc822",
			Text: testMessageRFC822Double,
		},
		{
			Name: "rfc822 in mime in rfc822",
			Text: testMessageMultipartRFC822Digest,
		},
	}
	for _, c := range cases {
		t.Run(c.Name, func(t *testing.T) {
			ctx := contextlib.WithLogger(context.Background(), zaptest.NewLogger(t))

			crlfText := strings.ReplaceAll(c.Text, "\n", "\r\n")

			msgData, err := uc.CreateMessage(ctx, acct.ID, "INBOX",
				now, []string{"$testFlag"}, int64(len(crlfText)), strings.NewReader(crlfText))
			if err != nil {
				t.Fatal(err)
			}

			var out bytes.Buffer

			err = uc.WritePart(ctx, msgData.Msg, message.EmptyPath(), &out, WriteOptions{
				Specifier: PartHeader | PartBody,
			})
			require.NoError(t, err)

			require.Equal(t, crlfText, out.String())
		})
	}
}
