package searcher

import (
	"cmp"
	"context"
	"runtime/trace"
	"slices"
	"strings"
	"time"

	"github.com/foxcpp/maddy-storage/internal/domain/message"
)

type SortField int

const (
	SortReceivedAt SortField = iota
	SortCC
	SortFrom
	SortTo
	SortDisplayFrom
	SortDisplayTo
	SortDate
	SortSize
)

func envelopeForSort(m *message.Msg, field SortField) string {
	addr := ""
	if len(m.Parts) == 0 {
		return addr
	}
	root := m.Parts[0]
	if root.Content == nil {
		return addr
	}
	env := root.Content.Envelope
	if env == nil {
		return addr
	}

	switch field {
	case SortCC:
		if len(env.Cc) == 0 {
			return addr
		}
		return env.Cc[0].Address
	case SortFrom, SortDisplayFrom:
		if len(env.From) == 0 {
			return addr
		}
		if field == SortDisplayFrom {
			return env.From[0].Name
		}
		return env.From[0].Address
	case SortTo, SortDisplayTo:
		if len(env.To) == 0 {
			return addr
		}
		if field == SortDisplayTo {
			return env.To[0].Name
		}
		return env.To[0].Address
	default:
		panic("unexpected sort field")
	}
}

func sentDateForSort(m *message.Msg) time.Time {
	if len(m.Parts) == 0 {
		return m.ReceivedAt
	}
	root := m.Parts[0]
	if root.Content == nil {
		return m.ReceivedAt
	}
	env := root.Content.Envelope
	if env == nil {
		return m.ReceivedAt
	}
	if env.Date.IsZero() {
		return m.ReceivedAt
	}
	return env.Date
}

func (s SortField) Compare(lhs, rhs *message.Msg) int {
	switch s {
	case SortReceivedAt:
		return lhs.ReceivedAt.Compare(rhs.ReceivedAt)
	case SortCC, SortFrom, SortDisplayFrom, SortTo, SortDisplayTo:
		return strings.Compare(envelopeForSort(lhs, s), envelopeForSort(rhs, s))
	case SortDate:
		return sentDateForSort(lhs).Compare(sentDateForSort(rhs))
	case SortSize:
		return cmp.Compare(lhs.TotalSize, rhs.TotalSize)
	default:
		panic("unexpected sort field")
	}
}

type SortKey struct {
	Field   SortField
	Reverse bool
}

func Compare(lhs, rhs *message.Msg, key []SortKey) int {
	for _, k := range key {
		val := k.Field.Compare(lhs, rhs)
		if k.Reverse {
			val = -val
		}
		if val != 0 {
			return val
		}
	}

	return lhs.ID.Compare(rhs.ID)
}

func Sort[Entry any](ctx context.Context, entries []Entry, msg func(Entry) *message.Msg, key []SortKey) {
	defer trace.StartRegion(ctx, "maddy-storage/message.searcher.Sort").End()

	slices.SortFunc(entries, func(a, b Entry) int {
		return Compare(msg(a), msg(b), key)
	})
}
