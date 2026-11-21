package imap2

import (
	"fmt"
	"strings"
	"testing"

	"github.com/foxcpp/maddy-storage/tests/imap2/utils"
)

func TestStoreFetchRFC822MIME(t *testing.T) {
	msg := utils.CRLF(`From: user@domain.org
Date: Sat, 24 Mar 2007 23:00:00 +0200
Mime-Version: 1.0
Content-Type: message/rfc822

From: sub@domain.org
Date: Sun, 12 Aug 2012 12:34:56 +0300
Subject: submsg
Content-Type: multipart/digest; boundary="foo"

--foo

From: m1@example.com
Subject: m1

m1 body

--foo
X-Mime: m2 header

From: m2@example.com
Subject: m2

m2 body

--foo--
`)

	s := utils.TestServer(t)
	s.Run()
	defer s.Close()

	username, password := s.Account()

	c := s.Conn()
	defer c.Close()
	c.ExpectPattern(`\* *`)
	c.Login(username, password)

	c.Writeln(". CREATE testfolder")
	c.ExpectOK()

	c.Writeln(fmt.Sprintf(`. APPEND "testfolder" "22-Feb-2008 17:06:23 +0300" {%d+}`, len(msg)))
	c.Writeln(msg)
	c.ExpectOK()

	c.Writeln(". SELECT testfolder")
	c.ExpectSelectResults(1, 1, 2)
	c.ExpectOK()

	c.Writeln(". FETCH 1 (BODY.PEEK[])")
	c.Expect(fmt.Sprintf(`* 1 FETCH (BODY[] {%d}`, len(msg)))
	lines := strings.Split(msg, "\r\n")
	for i, line := range lines {
		if i == len(lines)-1 {
			c.Expect(line + ")")
		} else {
			c.Expect(line)
		}
	}
	c.ExpectOK()
}

func TestFetchEnvelopePreservesDateTimezone(t *testing.T) {
	const messageTemplate = `Message-ID: <same@example.org>
Date: %s
From: user@example.org
Subject: Same message ID

body
`

	s := utils.TestServer(t)
	s.Run()
	defer s.Close()

	username, password := s.Account()
	c := s.Conn()
	defer c.Close()
	c.ExpectPattern(`\* *`)
	c.Login(username, password)

	for _, date := range []string{
		"Tue, 27 Jun 2006 07:43:34 -0300",
		"Tue, 27 Jun 2006 10:43:50 +0000",
	} {
		msg := utils.CRLF(fmt.Sprintf(messageTemplate, date))
		c.Writeln(fmt.Sprintf(". APPEND INBOX {%d+}", len(msg)))
		c.Writeln(msg)
		c.ExpectOK()
	}

	c.Writeln(". SELECT INBOX")
	c.Expect("* 2 EXISTS")
	c.Expect("* 2 RECENT")
	c.ExpectPattern(`\* OK \[UIDVALIDITY *\] *`)
	c.Expect("* OK [UIDNEXT 3] Predicted next UID")
	c.ExpectPattern(`\* FLAGS (*)`)
	c.ExpectPattern(`\* OK \[PERMANENTFLAGS (*)\] *`)
	c.Expect(`* LIST () "/" INBOX`)
	c.ExpectOK()

	c.Writeln(". FETCH 1:2 (ENVELOPE)")
	c.ExpectPattern(`* 1 FETCH (ENVELOPE ("Tue, 27 Jun 2006 07:43:34 -0300" *`)
	c.ExpectPattern(`* 2 FETCH (ENVELOPE ("Tue, 27 Jun 2006 10:43:50 +0000" *`)
	c.ExpectOK()
}
