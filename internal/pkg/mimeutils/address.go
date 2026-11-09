package mimeutils

import (
	"mime"
	"strings"

	"github.com/emersion/go-message"
	"github.com/emersion/go-message/mail"
)

// ParseAddressList parses an RFC 5322 address-list.
//
// It behaves like mail.ParseAddressList but additionally accepts comments
// (CFWS) around the local-part and the domain of an addr-spec, e.g.
// "<user (comment)@ (comment) domain.org>", which RFC 5322 allows but net/mail
// rejects.
//
// The tolerant parsing is only attempted if the regular one fails, so
// everything net/mail understands (including "user@domain (Display Name)")
// is parsed exactly the same way. The original error is returned if the
// fallback does not help either.
func ParseAddressList(value string) ([]*mail.Address, error) {
	list, err := mail.ParseAddressList(value)
	if err == nil {
		return list, nil
	}

	stripped := stripAddrSpecCFWS(value)
	if stripped == value {
		return nil, err
	}

	list, strippedErr := mail.ParseAddressList(stripped)
	if strippedErr != nil {
		return nil, err
	}

	return list, nil
}

// stripAddrSpecCFWS removes comments and the folding whitespace around '@',
// '<' and '>' that net/mail cannot handle. Quoted strings are copied as is.
// Comments are treated as whitespace, so they never glue atoms together.
func stripAddrSpecCFWS(s string) string {
	var (
		out     strings.Builder
		last    byte // Last byte written to out, 0 if nothing was.
		inQuote bool
		depth   int  // Comment nesting level.
		space   bool // Whitespace is pending, not written yet.
	)

	write := func(c byte) {
		out.WriteByte(c)
		last = c
	}

	out.Grow(len(s))

	for i := 0; i < len(s); i++ {
		c := s[i]

		switch {
		case depth > 0:
			switch c {
			case '\\':
				i++ // Skip the escaped character.
			case '(':
				depth++
			case ')':
				depth--
			}
			continue
		case inQuote:
			write(c)
			switch c {
			case '\\':
				if i+1 < len(s) {
					i++
					write(s[i])
				}
			case '"':
				inQuote = false
			}
			continue
		}

		switch c {
		case '(':
			depth++
			space = true
			continue
		case ' ', '\t', '\r', '\n':
			space = true
			continue
		}

		if space {
			// Whitespace is not allowed in addr-spec outside quoted
			// strings, drop it next to the delimiters.
			if c != '@' && c != '>' && last != '@' && last != '<' {
				write(' ')
			}
			space = false
		}

		write(c)
		if c == '"' {
			inQuote = true
		}
	}

	if depth > 0 {
		// Unterminated comment, nothing sensible to parse.
		return s
	}

	return out.String()
}

// AddressGroupNames returns display names of RFC 5322 groups
// ("name: member, member;") found in an address-list header value.
//
// net/mail parses group members but discards the group names, so they are
// extracted separately for searching.
func AddressGroupNames(value string) []string {
	var (
		names   []string
		seg     []byte // Phrase since the last list delimiter.
		inQuote bool
		inAngle bool
		depth   int // Comment nesting level.
	)

	decoder := &mime.WordDecoder{CharsetReader: message.CharsetReader}

	for i := 0; i < len(value); i++ {
		c := value[i]

		switch {
		case depth > 0:
			switch c {
			case '\\':
				i++
			case '(':
				depth++
			case ')':
				depth--
			}
			continue
		case inQuote:
			switch c {
			case '\\':
				if i+1 < len(value) {
					i++
					seg = append(seg, value[i])
				}
			case '"':
				inQuote = false
			default:
				seg = append(seg, c)
			}
			continue
		}

		switch c {
		case '(':
			depth++
			seg = append(seg, ' ') // Comments are whitespace.
		case '"':
			inQuote = true
		case '<':
			inAngle = true
			seg = append(seg, c)
		case '>':
			inAngle = false
			seg = append(seg, c)
		case ':':
			if inAngle {
				seg = append(seg, c)
				break
			}
			name := strings.Join(strings.Fields(string(seg)), " ")
			if name != "" {
				if dec, err := decoder.DecodeHeader(name); err == nil {
					name = dec
				}
				names = append(names, name)
			}
			seg = seg[:0]
		case ',', ';':
			if inAngle {
				seg = append(seg, c)
				break
			}
			seg = seg[:0]
		default:
			seg = append(seg, c)
		}
	}

	return names
}
