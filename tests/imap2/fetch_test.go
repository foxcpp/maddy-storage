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
