package imap2

import (
	"testing"

	"github.com/foxcpp/maddy-storage/tests/imap2/utils"
)

// subscribeFixture creates the following hierarchy and subscribes to p/a/b and
// p/c only. p and p/a are intentionally left unsubscribed.
//
//	p
//	p/a
//	p/a/b  (subscribed)
//	p/c    (subscribed)
func subscribeFixture(t *testing.T) *utils.Conn {
	t.Helper()

	s := utils.TestServer(t)
	s.Run()
	t.Cleanup(s.Close)

	username, password := s.Account()

	c := s.Conn()
	t.Cleanup(func() { c.Close() })
	c.ExpectPattern(`\* *`)
	c.Login(username, password)

	for _, cmd := range []string{
		". CREATE p/a/b",
		". CREATE p/c",
		". SUBSCRIBE p/a/b",
		". SUBSCRIBE p/c",
	} {
		c.Writeln(cmd)
		c.ExpectOK()
	}

	return c
}

// RFC 3501, 6.3.9: "%" wildcard to LSUB must return an unsubscribed parent of
// a subscribed mailbox flagged with \Noselect.
func TestLSubPercentReturnsNoSelectParent(t *testing.T) {
	c := subscribeFixture(t)

	c.Writeln(`. LSUB "" "p/%"`)
	c.Expect(`* LSUB (\Noselect) "/" "p/a"`)
	c.Expect(`* LSUB (\Subscribed) "/" "p/c"`)
	c.ExpectOK()
}

// The placeholder is only needed when the subscribed child is hidden by the
// pattern, "*" already matches it.
func TestLSubStarReturnsNoParents(t *testing.T) {
	c := subscribeFixture(t)

	c.Writeln(`. LSUB "" "p/*"`)
	c.Expect(`* LSUB (\Subscribed) "/" "p/a/b"`)
	c.Expect(`* LSUB (\Subscribed) "/" "p/c"`)
	c.ExpectOK()
}

// A subscribed parent is reported as a regular entry, once.
func TestLSubPercentSubscribedParentIsNotDuplicated(t *testing.T) {
	c := subscribeFixture(t)

	c.Writeln(". SUBSCRIBE p/a")
	c.ExpectOK()

	c.Writeln(`. LSUB "" "p/%"`)
	c.Expect(`* LSUB (\Subscribed) "/" "p/a"`)
	c.Expect(`* LSUB (\Subscribed) "/" "p/c"`)
	c.ExpectOK()
}

// RFC 5258, 3.1: "LSUB" and "LIST (SUBSCRIBED)" are not the same thing, the
// latter returns exactly the subscribed names that match, \Noselect has no
// special meaning there.
func TestListSubscribedPercentHasNoPlaceholders(t *testing.T) {
	c := subscribeFixture(t)

	c.Writeln(`. LIST (SUBSCRIBED) "" "p/%"`)
	c.Expect(`* LIST (\Subscribed) "/" "p/c"`)
	c.ExpectOK()
}

// RFC 5258, 3.1 and 3.5: with RECURSIVEMATCH a matching mailbox is returned if
// it, or any of its descendants, is subscribed. In the latter case CHILDINFO
// tells why it is there. Unsubscribed mailboxes without subscribed descendants
// are not returned.
func TestListSubscribedRecursiveMatch(t *testing.T) {
	c := subscribeFixture(t)

	c.Writeln(". CREATE p/d")
	c.ExpectOK()

	c.Writeln(`. LIST (SUBSCRIBED RECURSIVEMATCH) "" "p/%"`)
	c.Expect(`* LIST () "/" "p/a" (CHILDINFO ("SUBSCRIBED"))`)
	c.Expect(`* LIST (\Subscribed) "/" "p/c"`)
	c.ExpectOK()
}

// RFC 5258, 3.5: a parent is not reported if all its subscribed descendants
// are returned on their own, here they match the pattern.
func TestListSubscribedRecursiveMatchSuppressesRedundantChildInfo(t *testing.T) {
	c := subscribeFixture(t)

	c.Writeln(`. LIST (SUBSCRIBED RECURSIVEMATCH) "" "p/*"`)
	c.Expect(`* LIST (\Subscribed) "/" "p/a/b"`)
	c.Expect(`* LIST (\Subscribed) "/" "p/c"`)
	c.ExpectOK()
}

// Subscribed descendants are found at any depth below the matched mailbox,
// not only among direct children.
func TestLSubPercentDeepDescendant(t *testing.T) {
	c := subscribeFixture(t)

	for _, cmd := range []string{". CREATE q/x/y/z", ". SUBSCRIBE q/x/y/z"} {
		c.Writeln(cmd)
		c.ExpectOK()
	}

	c.Writeln(`. LSUB "" "q/%"`)
	c.Expect(`* LSUB (\Noselect) "/" "q/x"`)
	c.ExpectOK()
}

// A bare "%" is single-level, so p/a/b and p/c are hidden and only their
// unsubscribed parent is reported.
func TestLSubBarePercent(t *testing.T) {
	c := subscribeFixture(t)

	c.Writeln(`. LSUB "" "%"`)
	c.Expect(`* LSUB (\Noselect) "/" "p"`)
	c.ExpectOK()
}
