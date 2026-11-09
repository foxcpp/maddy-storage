package mimeutils

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestParseAddressList(t *testing.T) {
	cases := map[string]struct {
		In   string
		Want []string // Address.String() of each result.
	}{
		"plain": {
			In:   "user@domain.org",
			Want: []string{"<user@domain.org>"},
		},
		"display name": {
			In:   "Real Name <user@domain.org>",
			Want: []string{`"Real Name" <user@domain.org>`},
		},
		// Handled by net/mail: must not be changed by the fallback.
		"trailing comment is display name": {
			In:   "user@domain.org (RealName)",
			Want: []string{`"RealName" <user@domain.org>`},
		},
		"comments around local-part and domain": {
			In:   "<user-from (comment)@ (comment) domain.org>",
			Want: []string{"<user-from@domain.org>"},
		},
		"comment only before at": {
			In:   "<user (c)@domain.org>",
			Want: []string{"<user@domain.org>"},
		},
		"comment only after at": {
			In:   "<user@(c)domain.org>",
			Want: []string{"<user@domain.org>"},
		},
		"nested comments": {
			In:   "<user (a (b) c)@domain.org>",
			Want: []string{"<user@domain.org>"},
		},
		"escaped parenthesis in comment": {
			In:   `<user (a \) b)@domain.org>`,
			Want: []string{"<user@domain.org>"},
		},
		"whitespace around at": {
			In:   "<user @ domain.org>",
			Want: []string{"<user@domain.org>"},
		},
		"display name with comments in addr-spec": {
			In:   "Real <user (c)@ (c) domain.org>",
			Want: []string{"\"Real\" <user@domain.org>"},
		},
		"list with one broken and one plain address": {
			In:   "<user (c)@domain.org>, other@domain.org",
			Want: []string{"<user@domain.org>", "<other@domain.org>"},
		},
		"parentheses inside quoted string are kept": {
			In:   `"a (b)" <user (c)@domain.org>`,
			Want: []string{`"a (b)" <user@domain.org>`},
		},
		"spaces inside quoted local-part are kept": {
			In:   `<"a b" (c)@domain.org>`,
			Want: []string{`<"a b"@domain.org>`},
		},
	}

	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			got, err := ParseAddressList(c.In)
			require.NoError(t, err)

			strs := make([]string, 0, len(got))
			for _, a := range got {
				strs = append(strs, a.String())
			}
			require.Equal(t, c.Want, strs)
		})
	}
}

func TestParseAddressListErrors(t *testing.T) {
	for name, in := range map[string]string{
		"no at":                  "not an address",
		"unterminated comment":   "<user (c@domain.org>",
		"comment splits an atom": "<us(c)er@domain.org>",
		"unterminated quote":     `<"user (c)@domain.org>`,
	} {
		t.Run(name, func(t *testing.T) {
			_, err := ParseAddressList(in)
			require.Error(t, err)
		})
	}
}

func TestAddressGroupNames(t *testing.T) {
	cases := map[string]struct {
		In   string
		Want []string
	}{
		"no groups":   {In: "a@domain.org, B <b@domain.org>", Want: nil},
		"empty":       {In: "", Want: nil},
		"empty group": {In: "groupname:;", Want: []string{"groupname"}},
		"group with members": {
			In:   "groupname: a@domain.org, b@domain.org;",
			Want: []string{"groupname"},
		},
		"several groups": {
			In:   "groupname: a@domain.org, b@domain.org;,\r\n  groupname2: c@domain.org;",
			Want: []string{"groupname", "groupname2"},
		},
		"group after plain address": {
			In:   "x@domain.org, groupname: a@domain.org;",
			Want: []string{"groupname"},
		},
		"group with several words": {
			In:   "My Group: a@domain.org;",
			Want: []string{"My Group"},
		},
		"quoted group name": {
			In:   `"My: Group": a@domain.org;`,
			Want: []string{"My: Group"},
		},
		"comment in group name": {
			In:   "group (comment) name: a@domain.org;",
			Want: []string{"group name"},
		},
		"colon in comment is ignored": {
			In:   "a@domain.org (note: x)",
			Want: nil,
		},
		"colon in angle brackets is ignored": {
			In:   "<@route:a@domain.org>",
			Want: nil,
		},
		"colon in quoted local-part is ignored": {
			In:   `<"a:b"@domain.org>`,
			Want: nil,
		},
		"encoded word": {
			In:   "=?utf-8?q?gr=C3=BCppe?=: a@domain.org;",
			Want: []string{"grüppe"},
		},
	}

	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			require.Equal(t, c.Want, AddressGroupNames(c.In))
		})
	}
}
