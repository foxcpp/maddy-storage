package searcher

import (
	"context"
	"testing"
	"time"

	"github.com/foxcpp/maddy-storage/internal/domain/message"
	"github.com/stretchr/testify/require"
)

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
