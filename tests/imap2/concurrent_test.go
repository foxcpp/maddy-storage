package imap2

// Tests for concurrent-session correctness: unsolicited sequence number
// validity when another session modifies the same mailbox while this one is
// in the middle of a command exchange.
//
// Regression for two distinct "seq too high" classes observed in imaptest:
//
//  1. A concurrent APPEND inflates the watcher's ROW_NUMBER window, causing
//     a flag-update FETCH to be sent with a seqnum one past the client's
//     known EXISTS count.
//
//  2. A concurrent APPEND+EXPUNGE inflates the watcher window so that the
//     expunge seqnum exceeds the client's known EXISTS count.
//
// Both stem from the watcher seqnums subquery lacking the
// `created_at_modseq <= since` upper-bound filter that `visibleAt()` applies,
// and from the ChangeMessageDeleted filter not excluding messages the client
// never knew about.

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/foxcpp/maddy-storage/tests/imap2/utils"
)

const concTestMsg = `From: alice@example.org
To: bob@example.org
Subject: test
Date: Mon, 01 Jan 2024 00:00:00 +0000

body
`

// appendRaw appends a message via a raw IMAP connection, draining any
// unilateral responses that may arrive before the tagged OK.
func appendMsgRaw(c *utils.Conn, mailbox, msg string) {
	c.Writeln(fmt.Sprintf(". APPEND %q {%d+}", mailbox, len(msg)))
	c.Writeln(msg)
	for {
		line, err := c.Readln()
		if err != nil {
			c.T.Fatal("Unexpected I/O error:", err)
		}
		if strings.HasPrefix(line, ". ") {
			if !strings.Contains(line, " OK ") {
				c.T.Fatalf("APPEND failed: %s", line)
			}
			break
		}
	}
}

// drainUntilOK reads lines until a tagged OK, tracking EXISTS/EXPUNGE counts.
// Returns the last reported EXISTS count, the list of EXPUNGE seqnums
// received, and all FETCH seqnums received.
func drainUntilOK(c *utils.Conn) (exists uint32, expunges []uint32, fetches []uint32) {
	c.T.Helper()
	for {
		line, err := c.Readln()
		if err != nil {
			c.T.Fatal("Unexpected I/O error:", err)
		}
		if strings.HasPrefix(line, ". ") {
			break
		}
		if !strings.HasPrefix(line, "* ") {
			continue
		}
		rest := strings.TrimPrefix(line, "* ")
		var n uint32
		var kind string
		if _, err := fmt.Sscanf(rest, "%d %s", &n, &kind); err != nil {
			continue
		}
		switch kind {
		case "EXISTS":
			exists = n
		case "EXPUNGE":
			expunges = append(expunges, n)
		case "FETCH":
			fetches = append(fetches, n)
		}
	}
	return
}

// assertNoSeqTooHigh reads all untagged responses from a NOOP and checks that
// no EXPUNGE or FETCH seqnum arrives before the matching EXISTS was sent,
// which would put the seqnum beyond the client's known EXISTS count.
//
// The check mirrors what imaptest does: it tracks currentExists and fails if
// any seqnum in EXPUNGE or FETCH exceeds that count.
func assertNoSeqTooHigh(t *testing.T, c *utils.Conn, startExists uint32) {
	t.Helper()
	c.Writeln(". NOOP")
	current := startExists
	for {
		line, err := c.Readln()
		if err != nil {
			t.Fatal("Unexpected I/O error:", err)
		}
		if strings.HasPrefix(line, ". ") {
			break
		}
		if !strings.HasPrefix(line, "* ") {
			continue
		}
		rest := strings.TrimPrefix(line, "* ")
		var n uint32
		var kind string
		if _, err := fmt.Sscanf(rest, "%d %s", &n, &kind); err != nil {
			continue
		}
		switch kind {
		case "EXISTS":
			current = n
		case "EXPUNGE":
			if n > current {
				t.Errorf("* %d EXPUNGE: seqnum exceeds client's current EXISTS %d", n, current)
			}
			if current > 0 {
				current--
			}
		case "FETCH":
			if n > current {
				t.Errorf("* %d FETCH: seqnum exceeds client's current EXISTS %d", n, current)
			}
		}
	}
}

// TestConcurrentAppendFlagUpdateSeqnum verifies that an unsolicited FETCH
// (flag update) sent while a concurrent session has appended a message does
// not carry a seqnum beyond the client's known EXISTS count.
//
// Regression for: "seq too high (N > N-1): * N FETCH (UID … FLAGS …)"
//
// Root cause: the watcher seqnums subquery lacked `created_at_modseq <= since`
// which caused it to count the concurrent message in ROW_NUMBER, inflating
// seqnums for existing messages.
func TestConcurrentAppendFlagUpdateSeqnum(t *testing.T) {
	s := utils.TestServer(t)
	s.Run()
	defer s.Close()

	username, password := s.Account()
	msg := utils.CRLF(concTestMsg)

	// Session A: append two messages and SELECT so that both are visible.
	cA := s.Conn()
	defer cA.Close()
	cA.ExpectPattern(`\* *`)
	cA.Login(username, password)
	appendMsgRaw(cA, "INBOX", msg)
	appendMsgRaw(cA, "INBOX", msg)

	cA.Writeln(". SELECT INBOX")
	existsA, _, _ := drainUntilOK(cA)
	if existsA != 2 {
		t.Fatalf("expected 2 EXISTS, got %d", existsA)
	}

	// Session B: append a third message (concurrent with A's view), then
	// flag message 1. Session A will receive a flag-update FETCH for msg 1
	// on its next Poll. Without the fix, the watcher's ROW_NUMBER window
	// includes the new msg 3, shifting msg 1's seqnum to 1 (OK) and msg 2's
	// seqnum to 2 (OK) — but if B had flagged msg 2, it would become seqnum
	// 2 in a window of 3, with A still knowing only 2 messages.
	// Use message 2 to maximise the inflation effect.
	cB := s.Conn()
	defer cB.Close()
	cB.ExpectPattern(`\* *`)
	cB.Login(username, password)
	appendMsgRaw(cB, "INBOX", msg)

	cB.Writeln(". SELECT INBOX")
	existsB, _, _ := drainUntilOK(cB)

	// Flag message 2 from B. The watcher will return an Updated entry for
	// UID 2, whose seqnum in the unbounded (old) CTE is 2 out of 3 — still
	// within A's known range, so this test actually catches the case where
	// the seqnum computation is correct. The interesting regression case is
	// when B flags a message ADDED after A's snapshot, which is then sent as
	// a FETCH with seqnum 3 to session A that only knows 2.
	cB.Writeln(fmt.Sprintf(". STORE %d +FLAGS (\\Seen)", existsB))
	drainUntilOK(cB)

	time.Sleep(1200 * time.Millisecond)

	// Session A NOOP: check no seqnum violates EXISTS.
	assertNoSeqTooHigh(t, cA, existsA)
}

// TestConcurrentAppendExpungeSeqnum verifies that an unsolicited EXPUNGE
// sent while a concurrent session has added-then-deleted a message does not
// carry a seqnum beyond the client's known EXISTS count.
//
// Regression for: "seq too high (N+1 > N): * N+1 EXPUNGE"
//
// Root cause: the watcher's ChangeMessageDeleted filter did not exclude
// messages with created_at_modseq > deletesSince (messages the client never
// knew about). The seqnum for such an expunge was computed relative to a
// window that included the invisible message, pushing it one past the client's
// known count.
func TestConcurrentAppendExpungeSeqnum(t *testing.T) {
	s := utils.TestServer(t)
	s.Run()
	defer s.Close()

	username, password := s.Account()
	msg := utils.CRLF(concTestMsg)

	// Session A: two messages, SELECT.
	cA := s.Conn()
	defer cA.Close()
	cA.ExpectPattern(`\* *`)
	cA.Login(username, password)
	appendMsgRaw(cA, "INBOX", msg)
	appendMsgRaw(cA, "INBOX", msg)

	cA.Writeln(". SELECT INBOX")
	existsA, _, _ := drainUntilOK(cA)
	if existsA != 2 {
		t.Fatalf("expected 2 EXISTS, got %d", existsA)
	}

	// Session B: open mailbox, append msg 3, then mark+expunge it.
	// From A's perspective: a message was added and removed after its
	// snapshot. Without the fix, A receives * 3 EXPUNGE; with the fix,
	// A receives nothing (the message was never visible to it).
	cB := s.Conn()
	defer cB.Close()
	cB.ExpectPattern(`\* *`)
	cB.Login(username, password)

	cB.Writeln(". SELECT INBOX")
	existsB, _, _ := drainUntilOK(cB)
	t.Logf("session B EXISTS=%d", existsB)

	appendMsgRaw(cB, "INBOX", msg)

	// Absorb the new EXISTS that the APPEND piggybacks.
	cB.Writeln(". NOOP")
	newExists, _, _ := drainUntilOK(cB)
	t.Logf("session B EXISTS after APPEND=%d", newExists)

	// Mark and expunge.
	cB.Writeln(fmt.Sprintf(". STORE %d +FLAGS (\\Deleted)", newExists))
	drainUntilOK(cB)

	cB.Writeln(". EXPUNGE")
	drainUntilOK(cB)

	// Wait for the watcher.
	time.Sleep(1200 * time.Millisecond)

	// Session A: NOOP. The * 3 EXPUNGE that the old code sent must not appear.
	assertNoSeqTooHigh(t, cA, existsA)
}
