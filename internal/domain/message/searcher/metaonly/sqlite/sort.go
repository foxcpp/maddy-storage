package searchersqlite

import (
	"github.com/foxcpp/maddy-storage/internal/domain/message/searcher"
	"github.com/foxcpp/maddy-storage/internal/repository/sqlite/jetdb/table"
	j "github.com/go-jet/jet/v2/sqlite"
)

func orderBy(e j.Expression, reverse bool) j.OrderByClause {
	if reverse {
		return e.DESC()
	}
	return e.ASC()
}

// envelopeKey sorts by a JSON path in the message envelope, messages without
// the value go first regardless of the direction.
func envelopeKey(path string, reverse bool) j.OrderByClause {
	return orderBy(
		j.StringExp(j.Func("json_extract", table.Messages.Content, j.String("$.envelope."+path))),
		reverse,
	).NULLS_FIRST()
}

func sortKeyAsOrderBy(key searcher.SortKey) j.OrderByClause {
	switch key.Field {
	case searcher.SortReceivedAt:
		return orderBy(table.Messages.ReceivedAt, key.Reverse)
	case searcher.SortCC:
		return envelopeKey("cc[0].Address", key.Reverse)
	case searcher.SortFrom:
		return envelopeKey("from[0].Address", key.Reverse)
	case searcher.SortTo:
		return envelopeKey("to[0].Address", key.Reverse)
	case searcher.SortDisplayFrom:
		return envelopeKey("from[0].Name", key.Reverse)
	case searcher.SortDisplayTo:
		return envelopeKey("to[0].Name", key.Reverse)
	case searcher.SortDate:
		return orderBy(
			j.COALESCE(
				j.StringExp(j.Func("json_extract", table.Messages.Content, j.String("$.envelope.date"))),
				table.Messages.ReceivedAt,
			),
			key.Reverse,
		)
	case searcher.SortSize:
		return orderBy(table.Messages.TotalSize, key.Reverse)
	default:
		panic("unexpected sort field")
	}
}

// orderClauses returns ORDER BY terms, entries with equal keys are ordered by
// UID.
func orderClauses(keys []searcher.SortKey) []j.OrderByClause {
	clauses := make([]j.OrderByClause, 0, len(keys)+1)
	for _, k := range keys {
		clauses = append(clauses, sortKeyAsOrderBy(k))
	}
	return append(clauses, table.FolderEntries.UID.ASC())
}
