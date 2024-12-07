package mimeutils

import (
	"bufio"
	"bytes"
	"io"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestSkipHeader(t *testing.T) {
	msgTextLF := `From: User4 <user4@domain.org>
Date: Sat, 24 Mar 2007 23:00:00 +0200
Subject: s4444

body4444`
	msgTextCRLF := strings.ReplaceAll(msgTextLF, "\n", "\r\n")

	r := bufio.NewReader(strings.NewReader(msgTextCRLF))
	if err := SkipHeader(r); err != nil {
		t.Fatal(err)
	}
	var remainder bytes.Buffer
	if _, err := io.Copy(&remainder, r); err != nil {
		t.Fatal(err)
	}
	require.Equal(t, "body4444", remainder.String())

	t.Run("lf", func(t *testing.T) {
		r := bufio.NewReader(strings.NewReader(msgTextLF))
		if err := SkipHeader(r); err != nil {
			t.Fatal(err)
		}
		var remainder bytes.Buffer
		if _, err := io.Copy(&remainder, r); err != nil {
			t.Fatal(err)
		}
		require.Equal(t, "body4444", remainder.String())
	})
}

func TestSkipHeaders(t *testing.T) {
	msgTestLF := `Message-ID: <msg@id>
In-Reply-To: <reply@to.id>
Date: Thu, 15 Feb 2007 01:02:03 +0200
Subject: subject header
From: From Real <fromuser@fromdomain.org>
To: To Real <touser@todomain.org>
Cc: Cc Real <ccuser@ccdomain.org>
Bcc: Bcc Real <bccuser@bccdomain.org>
Sender: Sender Real <senderuser@senderdomain.org>
Reply-To: ReplyTo Real <replytouser@replytodomain.org>

body`
	msgTestCRLF := strings.ReplaceAll(msgTestLF, "\n", "\r\n")

	cases := []struct {
		Name    string
		Out     string
		Include []string
		Exclude []string
	}{
		{
			Name: "no filter",
			Out:  msgTestCRLF,
		},
		{
			Name:    "from header",
			Out:     "From: From Real <fromuser@fromdomain.org>\r\n\r\nbody",
			Include: []string{"From"},
		},
		{
			Name: "no from to header",
			Out: strings.ReplaceAll(`Message-ID: <msg@id>
In-Reply-To: <reply@to.id>
Date: Thu, 15 Feb 2007 01:02:03 +0200
Subject: subject header
Cc: Cc Real <ccuser@ccdomain.org>
Bcc: Bcc Real <bccuser@bccdomain.org>
Sender: Sender Real <senderuser@senderdomain.org>
Reply-To: ReplyTo Real <replytouser@replytodomain.org>

body`, "\n", "\r\n"),
			Exclude: []string{"From", "To"},
		},
		{
			Name: "to only",
			Out: strings.ReplaceAll(`To: To Real <touser@todomain.org>

body`, "\n", "\r\n"),
			Include: []string{"From", "To"},
			Exclude: []string{"From"},
		},
	}

	for _, c := range cases {
		t.Run(c.Name, func(t *testing.T) {
			r := bufio.NewReader(strings.NewReader(msgTestCRLF))
			var output bytes.Buffer
			if err := FilterHeaderCopy(r, &output, c.Include, c.Exclude); err != nil {
				t.Fatal(err)
			}
			if _, err := io.Copy(&output, r); err != nil {
				t.Fatal(err)
			}
			require.Equal(t, c.Out, output.String())
		})
	}
}
