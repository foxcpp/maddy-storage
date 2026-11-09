package imap2

import (
	"testing"

	"github.com/foxcpp/maddy-storage/tests/imap2/utils"
)

// listFixture creates INBOX plus:
//
//	a
//	a/b
//	a/b/c
//	d
func listFixture(t *testing.T) *utils.Conn {
	t.Helper()

	s := utils.TestServer(t)
	s.Run()
	t.Cleanup(s.Close)

	username, password := s.Account()

	c := s.Conn()
	t.Cleanup(func() { c.Close() })
	c.ExpectPattern(`\* *`)
	c.Login(username, password)

	for _, cmd := range []string{". CREATE a/b/c", ". CREATE d"} {
		c.Writeln(cmd)
		c.ExpectOK()
	}

	return c
}

// RFC 3501, 6.3.8: "%" matches any character except the hierarchy delimiter.
// Without a prefix it must match top-level names only.
func TestListBarePercentMatchesTopLevelOnly(t *testing.T) {
	c := listFixture(t)

	c.Writeln(`. LIST "" "%"`)
	c.Expect(`* LIST (\Inbox) "/" INBOX`)
	c.Expect(`* LIST () "/" "a"`)
	c.Expect(`* LIST () "/" "d"`)
	c.ExpectOK()
}

func TestListBareStarMatchesWholeTree(t *testing.T) {
	c := listFixture(t)

	c.Writeln(`. LIST "" "*"`)
	c.Expect(`* LIST (\Inbox) "/" INBOX`)
	c.Expect(`* LIST () "/" "a"`)
	c.Expect(`* LIST () "/" "a/b"`)
	c.Expect(`* LIST () "/" "a/b/c"`)
	c.Expect(`* LIST () "/" "d"`)
	c.ExpectOK()
}

// "%" must not match across the delimiter anywhere in the pattern.
func TestListPercentWithPrefixIsSingleLevel(t *testing.T) {
	c := listFixture(t)

	c.Writeln(`. LIST "" "a/%"`)
	c.Expect(`* LIST () "/" "a/b"`)
	c.ExpectOK()
}

// The reference is prepended to the pattern, a bare "%" there is single-level
// as well.
func TestListPercentWithReference(t *testing.T) {
	c := listFixture(t)

	c.Writeln(`. LIST "a/" "%"`)
	c.Expect(`* LIST () "/" "a/b"`)
	c.ExpectOK()
}

// RFC 9051, 6.3.9.5: attributes are mandatory only if the CHILDREN return
// option is present, otherwise the server has no need to compute them.
func TestListWithoutChildrenOptionHasNoChildrenAttrs(t *testing.T) {
	c := listFixture(t)

	c.Writeln(`. LIST "" "a/*"`)
	c.Expect(`* LIST () "/" "a/b"`)
	c.Expect(`* LIST () "/" "a/b/c"`)
	c.ExpectOK()
}

func TestListReturnChildren(t *testing.T) {
	c := listFixture(t)

	c.Writeln(`. LIST "" "%" RETURN (CHILDREN)`)
	c.Expect(`* LIST (\Inbox \HasNoChildren) "/" INBOX`)
	c.Expect(`* LIST (\HasChildren) "/" "a"`)
	c.Expect(`* LIST (\HasNoChildren) "/" "d"`)
	c.ExpectOK()
}

// RETURN (CHILDREN) on a plain pattern must still compute the attribute for
// mailboxes whose children are not part of the result.
func TestListReturnChildrenDeepPattern(t *testing.T) {
	c := listFixture(t)

	c.Writeln(`. LIST "" "a/%" RETURN (CHILDREN)`)
	c.Expect(`* LIST (\HasChildren) "/" "a/b"`)
	c.ExpectOK()
}

// A pattern without wildcards names exactly one mailbox, its children and
// mailboxes with the same name prefix must not be returned.
func TestListExactNameDoesNotMatchPrefixOrChildren(t *testing.T) {
	c := listFixture(t)

	c.Writeln(". CREATE ab")
	c.ExpectOK()

	c.Writeln(`. LIST "" "a"`)
	c.Expect(`* LIST () "/" "a"`)
	c.ExpectOK()
}

// Names with characters special in SQL LIKE and in regular expressions are
// matched literally.
func TestListExactNameWithSpecialChars(t *testing.T) {
	c := listFixture(t)

	for _, name := range []string{"a_b", "axb", "a.b", "a$", "a%b"} {
		c.Writeln(". CREATE " + `"` + name + `"`)
		c.ExpectOK()
	}

	for _, name := range []string{"a_b", "a.b", "a$"} {
		c.Writeln(`. LIST "" "` + name + `"`)
		c.Expect(`* LIST () "/" "` + name + `"`)
		c.ExpectOK()
	}
}

// A mailbox matching several patterns of one LIST command is returned once.
func TestListOverlappingPatternsReturnMailboxOnce(t *testing.T) {
	c := listFixture(t)

	c.Writeln(`. LIST "" ("a/b" "a/%")`)
	c.Expect(`* LIST () "/" "a/b"`)
	c.ExpectOK()
}

// Deduplication must not drop mailboxes matched by different patterns.
func TestListDistinctPatternsReturnAllMatches(t *testing.T) {
	c := listFixture(t)

	c.Writeln(`. LIST "" ("a/b" "d")`)
	c.Expect(`* LIST () "/" "a/b"`)
	c.Expect(`* LIST () "/" "d"`)
	c.ExpectOK()
}
