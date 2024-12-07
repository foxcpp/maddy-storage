package messageusecase

import (
	"bytes"
	"context"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/foxcpp/maddy-storage/internal/domain/message"
	"github.com/foxcpp/maddy-storage/internal/pkg/contextlog"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap/zaptest"
)

func TestOffsetWriter_Write(t *testing.T) {
	stream := strings.Repeat("A", 4092) +
		strings.Repeat("B", 4396) +
		strings.Repeat("C", 8120)
	var output bytes.Buffer
	w := offsetWriter{
		W:      &output,
		Offset: 4092,
		Size:   4396,
	}
	if _, err := io.Copy(&w, strings.NewReader(stream)); err != nil {
		t.Fatal(err)
	}

	require.Equal(t, strings.Repeat("B", 4396), output.String())
}

func TestUsecase_WritePart(t *testing.T) {
	type subcase struct {
		Name   string
		Path   message.Path
		Opts   WriteOptions
		Result string
	}

	cases := []struct {
		Name     string
		Text     string
		Subcases []subcase
	}{
		{
			Name: "no ct plain text",
			Text: testMessageNoCT,
			Subcases: []subcase{
				{
					Name: "empty path - header body",
					Path: []int{},
					Opts: WriteOptions{
						Specifier: PartHeader | PartBody,
					},
					Result: testMessageNoCT,
				},
				{
					Name: "empty path - body only",
					Path: []int{},
					Opts: WriteOptions{
						Specifier: PartBody,
					},
					Result: "body4444",
				},
				{
					Name: "empty path - header only",
					Path: []int{},
					Opts: WriteOptions{
						Specifier: PartHeader,
					},
					Result: `From: User4 <user4@domain.org>
Date: Sat, 24 Mar 2007 23:00:00 +0200
Subject: s4444

`,
				},
				{
					Name: "path 1",
					Path: []int{1},
					Opts: WriteOptions{
						Specifier: PartHeader | PartBody,
					},
					Result: ``,
				},
				{
					Name: "empty path - subject only",
					Path: []int{},
					Opts: WriteOptions{
						Specifier:    PartHeader,
						HeaderFields: []string{"Subject"},
					},
					Result: `Subject: s4444

`,
				},
				{
					Name: "empty path - no date",
					Path: []int{},
					Opts: WriteOptions{
						Specifier:          PartHeader,
						HeaderFieldsExcept: []string{"Date"},
					},
					Result: `From: User4 <user4@domain.org>
Subject: s4444

`,
				},
				{
					Name: "empty path - offset 2 size 5",
					Path: []int{},
					Opts: WriteOptions{
						Specifier: PartHeader | PartBody,
					},
					Result: `om: U`,
				},
				{
					Name: "empty path - offset 2 size 5 in body",
					Path: []int{},
					Opts: WriteOptions{
						Specifier: PartBody,
					},
					Result: `dy444`,
				},
			},
		},
		{
			Name: "multipart",
			Text: testMessageMultipart,
			Subcases: []subcase{
				{
					Name: "all",
					Path: message.EmptyPath(),
					Opts: WriteOptions{
						Specifier: PartHeader | PartBody,
					},
					Result: testMessageMultipart,
				},
				{
					Name: "all - header body",
					Path: []int{1},
					Opts: WriteOptions{
						Specifier: PartHeader | PartBody,
					},
					Result: `hello
`,
				},
				{
					Name: "all - header",
					Path: []int{1},
					Opts: WriteOptions{
						Specifier: PartHeader,
					},
					Result: ``,
				},
				{
					Name: "all - body",
					Path: []int{1},
					Opts: WriteOptions{
						Specifier: PartBody,
					},
					Result: `hello
`,
				},
				{
					Name: "all - mime",
					Path: []int{1},
					Opts: WriteOptions{
						Specifier: PartMIME,
					},
					Result: `Content-Type: text/x-myown; charset=us-ascii

`,
				},
			},
		},
		{
			Name: "multipart rfc822",
			Text: testMessageMultipartRFC822,
			Subcases: []subcase{
				{
					Name: "all",
					Path: message.EmptyPath(),
					Opts: WriteOptions{
						Specifier: PartHeader | PartBody,
					}, // prologues and epilogues are lost
					Result: `From: user@domain.org
Date: Sat, 24 Mar 2007 23:00:00 +0200
Mime-Version: 1.0
Content-Type: multipart/mixed; boundary="foo
 bar"

--foo bar
Content-Type: text/x-myown; charset=us-ascii

hello

--foo bar
Content-Type: message/rfc822

From: sub@domain.org
Date: Sun, 12 Aug 2012 12:34:56 +0300
Subject: submsg
Content-Type: multipart/alternative; boundary="sub1"

--sub1
Content-Type: text/html

<p>Hello world</p>

--sub1
Content-Type: text/plain

Hello another world

--sub1--

--foo bar--
`,
				},
				{
					Name: "part 1 - body",
					Path: []int{1},
					Opts: WriteOptions{
						Specifier: PartBody,
					},
					Result: `hello
`,
				},
				{
					Name: "part 2 - body",
					Path: []int{2},
					Opts: WriteOptions{
						Specifier: PartBody,
					},
					Result: `--sub1
Content-Type: text/html

<p>Hello world</p>

--sub1
Content-Type: text/plain

Hello another world

--sub1--
`,
				},
				{
					Name: "part 1 - mime",
					Path: []int{1},
					Opts: WriteOptions{
						Specifier: PartMIME,
					},
					Result: `Content-Type: text/x-myown; charset=us-ascii

`,
				},
				{
					Name: "part 2 - mime",
					Path: []int{2},
					Opts: WriteOptions{
						Specifier: PartMIME,
					},
					Result: `Content-Type: message/rfc822

`,
				},
				{
					Name: "part 2 - header",
					Path: []int{2},
					Opts: WriteOptions{
						Specifier: PartHeader,
					},
					Result: `From: sub@domain.org
Date: Sun, 12 Aug 2012 12:34:56 +0300
Subject: submsg
Content-Type: multipart/alternative; boundary="sub1"

`,
				},
				{
					Name: "part 2.1 - all",
					Path: []int{2, 1},
					Opts: WriteOptions{
						Specifier: PartMIME | PartHeader | PartBody,
					},
					Result: `Content-Type: text/html

<p>Hello world</p>
`,
				},
				{
					Name: "part 2.1 - mime, body",
					Path: []int{2, 1},
					Opts: WriteOptions{
						Specifier: PartMIME | PartBody,
					},
					Result: `Content-Type: text/html

<p>Hello world</p>
`,
				},
				{
					Name: "part 2.1 - header",
					Path: []int{2, 1},
					Opts: WriteOptions{
						Specifier: PartHeader | PartBody,
					},
					Result: `<p>Hello world</p>
`,
				},
				{
					Name: "part 2.2 - all",
					Path: []int{2, 2},
					Opts: WriteOptions{
						Specifier: PartMIME | PartHeader | PartBody,
					},
					Result: `Content-Type: text/plain

Hello another world
`,
				},
			},
		},
	}

	uc, _, acct, _ := initMessageTestUsecase(t)
	now := time.Now().In(time.UTC)

	for _, c := range cases {
		t.Run(c.Name, func(t *testing.T) {
			ctx := contextlog.WithLogger(context.Background(), zaptest.NewLogger(t))

			crlfText := strings.ReplaceAll(c.Text, "\n", "\r\n")
			storedMsg, err := uc.bufferStoreMessage(
				ctx, acct.ID_,
				now, []string{"$testFlag"},
				int64(len(crlfText)), strings.NewReader(crlfText))
			if err != nil {
				t.Fatal(err)
			}

			for _, subcase := range c.Subcases {
				t.Run(subcase.Name, func(t *testing.T) {
					crlfResult := strings.ReplaceAll(subcase.Result, "\n", "\r\n")

					var out bytes.Buffer
					err := uc.WritePart(ctx, storedMsg, subcase.Path, &out, subcase.Opts)
					if err != nil {
						t.Fatal(err)
					}

					require.Equal(t, crlfResult, out.String())
				})
			}
		})
	}
}
