package folder

import "github.com/oklog/ulid/v2"

// SortAsTree sorts list of folders as defined by sortAsTree JMAP option.
func SortAsTree[T any](slice []T, parentID func(i int) ulid.ULID, less func(i int, j int) bool) {
	// XXX: TODO
}
