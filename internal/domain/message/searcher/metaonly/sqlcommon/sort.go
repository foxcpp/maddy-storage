package searchersql

import (
	"github.com/foxcpp/maddy-storage/internal/domain/message/searcher"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

func sortKeyAsOrderBy(key searcher.SortKey) clause.OrderByColumn {
	switch key.Field {
	case searcher.SortReceivedAt:
		return clause.OrderByColumn{
			Column: clause.Column{Table: "messages", Name: "received_at"},
			Desc:   key.Reverse,
		}
	case searcher.SortCC:
		if key.Reverse {
			return clause.OrderByColumn{
				Column: clause.Column{
					Name: `messages.content->'$.envelope.cc[0].Address' DESC NULLS FIRST`,
					Raw:  true,
				},
			}
		} else {
			return clause.OrderByColumn{
				Column: clause.Column{
					Name: `messages.content->'$.envelope.cc[0].Address' ASC NULLS FIRST`,
					Raw:  true,
				},
			}
		}
	case searcher.SortFrom:
		if key.Reverse {
			return clause.OrderByColumn{
				Column: clause.Column{
					Name: `messages.content->'$.envelope.from[0].Address' DESC NULLS FIRST`,
					Raw:  true,
				},
			}
		} else {
			return clause.OrderByColumn{
				Column: clause.Column{
					Name: `messages.content->'$.envelope.from[0].Address' ASC NULLS FIRST`,
					Raw:  true,
				},
			}
		}
	case searcher.SortTo:
		if key.Reverse {
			return clause.OrderByColumn{
				Column: clause.Column{
					Name: `messages.content->'$.envelope.to[0].Address' DESC NULLS FIRST`,
					Raw:  true,
				},
			}
		} else {
			return clause.OrderByColumn{
				Column: clause.Column{
					Name: `messages.content->'$.envelope.to[0].Address' ASC NULLS FIRST`,
					Raw:  true,
				},
			}
		}
	case searcher.SortDisplayFrom:
		if key.Reverse {
			return clause.OrderByColumn{
				Column: clause.Column{
					Name: `messages.content->'$.envelope.from[0].Name' DESC NULLS FIRST`,
					Raw:  true,
				},
			}
		} else {
			return clause.OrderByColumn{
				Column: clause.Column{
					Name: `messages.content->'$.envelope.from[0].Name' ASC NULLS FIRST`,
					Raw:  true,
				},
			}
		}
	case searcher.SortDisplayTo:
		if key.Reverse {
			return clause.OrderByColumn{
				Column: clause.Column{
					Name: `messages.content->'$.envelope.to[0].Name' DESC NULLS FIRST`,
					Raw:  true,
				},
			}
		} else {
			return clause.OrderByColumn{
				Column: clause.Column{
					Name: `messages.content->'$.envelope.to[0].Name' ASC NULLS FIRST`,
					Raw:  true,
				},
			}
		}
	case searcher.SortDate:
		return clause.OrderByColumn{
			Column: clause.Column{
				Name: `coalesce(messages.content->'$.envelope.date', messages.received_at)`,
				Raw:  true,
			},
			Desc: key.Reverse,
		}
	case searcher.SortSize:
		return clause.OrderByColumn{
			Column: clause.Column{
				Table: "messages",
				Name:  "total_size",
			},
			Desc: key.Reverse,
		}
	default:
		panic("unexpected sort field")
	}
}

func (s *Searcher) addOrder(q *gorm.DB, key []searcher.SortKey) *gorm.DB {
	if len(key) == 0 {
		return q.Order(clause.OrderByColumn{
			Column: clause.Column{Table: "folder_entries", Name: "uid"},
		})
	}

	clauses := make([]clause.OrderByColumn, len(key)+1)
	for i, k := range key {
		clauses[i] = sortKeyAsOrderBy(k)
	}
	clauses[len(clauses)-1] = clause.OrderByColumn{
		Column: clause.Column{Table: "folder_entries", Name: "uid"},
	}

	return q.Order(clause.OrderBy{Columns: clauses})
}
