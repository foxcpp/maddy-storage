package messageusecase

const testMessageNoCT = `From: User4 <user4@domain.org>
Date: Sat, 24 Mar 2007 23:00:00 +0200
Subject: s4444

body4444
`

const testMessageEnvelope = `Message-ID: <msg@id>
In-Reply-To: <reply@to.id>
Date: Thu, 15 Feb 2007 01:02:03 +0200
Subject: subject header
From: From Real <fromuser@fromdomain.org>
To: To Real <touser@todomain.org>
Cc: Cc Real <ccuser@ccdomain.org>
Bcc: Bcc Real <bccuser@bccdomain.org>
Sender: Sender Real <senderuser@senderdomain.org>
Reply-To: ReplyTo Real <replytouser@replytodomain.org>

body
`

const testMessageMalformedEnvelope = `Date: Thu, 15 Feb 2007 01:02:03 +0200
From: user@domain (Real Name)
Sender: 
Reply-To: 

body
`

const testMessageMultipart = `From: user@domain.org
Date: Sat, 24 Mar 2007 23:00:00 +0200
Mime-Version: 1.0
Content-Type: multipart/mixed; boundary="foo
 bar"

--foo bar
Content-Type: text/x-myown; charset=us-ascii

hello

--foo bar--
`

const testMessageMultipartRFC822 = `From: user@domain.org
Date: Sat, 24 Mar 2007 23:00:00 +0200
Mime-Version: 1.0
Content-Type: multipart/mixed; boundary="foo
 bar"

Root MIME prologue

--foo bar
Content-Type: text/x-myown; charset=us-ascii

hello

--foo bar
Content-Type: message/rfc822

From: sub@domain.org
Date: Sun, 12 Aug 2012 12:34:56 +0400
Subject: submsg
Content-Type: multipart/alternative; boundary="sub1"

Sub MIME prologue
--sub1
Content-Type: text/html

<p>Hello world</p>

--sub1
Content-Type: text/plain

Hello another world

--sub1--
Sub MIME epilogue

--foo bar--
Root MIME epilogue
`

const testMessageMultipartRFC822NoPrologueEpilogue = `From: user@domain.org
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
Date: Sun, 12 Aug 2012 12:34:56 +0400
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
`

const testMessageRFC822 = `From: user@domain.org
Date: Sat, 24 Mar 2007 23:00:00 +0200
Mime-Version: 1.0
Content-Type: message/rfc822

From: sub@domain.org
Date: Sun, 12 Aug 2012 12:34:56 +0400
Subject: submsg

Hello world
`

const testMessageRFC822Double = `From: user@domain.org
Date: Sat, 24 Mar 2007 23:00:00 +0200
Mime-Version: 1.0
Content-Type: message/rfc822

From: user2@domain.org
Date: Fri, 23 Mar 2007 11:22:33 +0200
Mime-Version: 1.0
Content-Type: message/rfc822

From: sub@domain.org
Date: Sun, 12 Aug 2012 12:34:56 +0400
Subject: submsg

Hello world
`
