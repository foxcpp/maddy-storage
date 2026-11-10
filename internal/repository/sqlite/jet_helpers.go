package sqlite

import (
	"time"

	j "github.com/go-jet/jet/v2/sqlite"
	"github.com/oklog/ulid/v2"
)

func Timestamp(t time.Time) j.TimestampExpression {
	// Matches time.Time serialization for models.
	return j.RawTimestamp("#t", j.RawArgs{"#t": t})
}

func ULID(id ulid.ULID) j.BlobExpression {
	return j.Blob(id[:])
}

func ULIDList(ids []ulid.ULID) []j.Expression {
	res := make([]j.Expression, len(ids))
	for i, id := range ids {
		res[i] = ULID(id)
	}
	return res
}

// LikeEscape builds "col LIKE pattern ESCAPE escape", jet has no native ESCAPE support.
func LikeEscape(col j.Column, pattern, escape string) j.BoolExpression {
	return j.RawBool(
		"("+col.TableName()+"."+col.Name()+" LIKE #pattern ESCAPE '"+escape+"')",
		j.RawArgs{"#pattern": pattern},
	)
}

func StringValues(ss []string) []j.Expression {
	res := make([]j.Expression, len(ss))
	for i, s := range ss {
		res[i] = j.String(s)
	}
	return res
}
