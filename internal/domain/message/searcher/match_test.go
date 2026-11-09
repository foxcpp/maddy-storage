package searcher

import (
	"context"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/foxcpp/maddy-storage/internal/domain/message"
	"github.com/stretchr/testify/require"
)

func TestMatchHeaderAddress(t *testing.T) {
	cases := map[string]struct {
		Header string
		Key    string
		Value  string
		Want   bool
	}{
		"plain": {
			Header: "From: user-from@domain.org\r\n\r\n",
			Key:    "From", Value: "user-from@domain.org", Want: true,
		},
		"display name": {
			Header: "From: RealFrom <user-from@domain.org>\r\n\r\n",
			Key:    "From", Value: "user-from@domain.org", Want: true,
		},
		"trailing comment as display name": {
			Header: "From: user-from@domain.org (RealFrom)\r\n\r\n",
			Key:    "From", Value: `"RealFrom" <user-from@domain.org>`, Want: true,
		},
		"comments inside addr-spec": {
			Header: "From: <user-from (comment)@ (comment) domain.org>\r\n\r\n",
			Key:    "From", Value: "user-from@domain.org", Want: true,
		},
		"comments inside addr-spec do not leak into match": {
			Header: "From: <user-from (comment)@ (comment) domain.org>\r\n\r\n",
			Key:    "From", Value: "comment", Want: false,
		},
		"unparsable value falls back to raw": {
			Header: "From: not an address\r\n\r\n",
			Key:    "From", Value: "not an", Want: true,
		},
		// RFC 3501 6.4.4: substring matching is case-insensitive.
		"case-insensitive address": {
			Header: "From: User-From@Domain.org\r\n\r\n",
			Key:    "From", Value: "user-from@domain.org", Want: true,
		},
		"case-insensitive display name": {
			Header: "From: RealFrom <user-from@domain.org>\r\n\r\n",
			Key:    "From", Value: "realfrom", Want: true,
		},
		"display name substring": {
			Header: "From: RealFrom <user-from@domain.org>\r\n\r\n",
			Key:    "From", Value: "ealfro", Want: true,
		},
		"case-insensitive non-address header": {
			Header: "Subject: Hello World\r\n\r\n",
			Key:    "Subject", Value: "hello", Want: true,
		},
		"group name": {
			Header: "To: groupname: groupuser1@domain.org;\r\n\r\n",
			Key:    "To", Value: "groupname", Want: true,
		},
		"empty group name": {
			Header: "To: groupname:;\r\n\r\n",
			Key:    "To", Value: "groupname", Want: true,
		},
		"group member": {
			Header: "To: groupname: groupuser1@domain.org;\r\n\r\n",
			Key:    "To", Value: "groupuser1", Want: true,
		},
		"second group name in list": {
			Header: "To: groupname: a@domain.org;,\r\n  groupname2: b@domain.org;\r\n\r\n",
			Key:    "To", Value: "groupname2", Want: true,
		},
		"group name is not matched by other group": {
			Header: "To: groupname: a@domain.org;\r\n\r\n",
			Key:    "To", Value: "groupname2", Want: false,
		},
		"group name is not matched in non-address header": {
			Header: "Subject: groupname: x\r\n\r\n",
			Key:    "To", Value: "groupname", Want: false,
		},
	}

	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			msg := &message.Msg{Parts: []message.Part{{}}}
			open := func(context.Context, *message.Part) (io.ReadCloser, error) {
				return io.NopCloser(strings.NewReader(c.Header)), nil
			}

			got, err := matchHeader(context.Background(), msg, open,
				[]HeaderField{{Key: c.Key, Value: c.Value}})
			require.NoError(t, err)
			require.Equal(t, c.Want, got)
		})
	}
}

func TestMatchSentDateUsesEnvelopeDate(t *testing.T) {
	msg := &message.Msg{
		ReceivedAt: time.Date(2008, time.February, 22, 17, 6, 23, 0, time.FixedZone("EET", 2*60*60)),
		Parts: []message.Part{{
			Content: &message.ContentPartData{
				Envelope: &message.ContentEnvelope{
					Date: time.Date(2007, time.October, 28, 23, 0, 0, 0, time.FixedZone("EET", 2*60*60)),
				},
			},
		}},
	}

	matched, err := Match(context.Background(), &FoundMsg{}, msg, nil, &Cond{
		SentAfter:    time.Date(2007, time.October, 28, 0, 0, 0, 0, time.UTC),
		SentBefore:   time.Date(2007, time.October, 29, 0, 0, 0, 0, time.UTC),
		SentDateOnly: true,
	})
	require.NoError(t, err)
	require.True(t, matched)
}
