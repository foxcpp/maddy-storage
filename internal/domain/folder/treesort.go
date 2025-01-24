package folder

import (
	"slices"

	"github.com/oklog/ulid/v2"
)

func putWithChildren[Entry any](out *[]Entry, children map[ulid.ULID][]Entry, ent Entry, folder func(e *Entry) *Folder) {
	*out = append(*out, ent)
	for _, child := range children[folder(&ent).ID] {
		putWithChildren[Entry](out, children, child, folder)
	}
}

// SortAsTree sorts list of folders as defined by sortAsTree JMAP option.
//
// Indirection is added to make sure
func SortAsTree[Entry any](slice []Entry, folder func(e *Entry) *Folder, cmp func(a, b *Folder) int) []Entry {
	byID := make(map[ulid.ULID]*Folder, len(slice))
	entByID := make(map[ulid.ULID]Entry, len(byID))
	for _, entry := range slice {
		f := folder(&entry)
		byID[f.ID] = folder(&entry)
		entByID[f.ID] = entry
	}

	// Short-cut without hierarchy.
	roots := make([]Entry, 0, len(byID))
	for _, entry := range slice {
		if f := folder(&entry); !f.HasParent() {
			roots = append(roots, entry)
		}
	}

	if len(roots) == len(byID) {
		slices.SortFunc(slice, func(a, b Entry) int {
			return cmp(folder(&a), folder(&b))
		})
	}

	children := make(map[ulid.ULID][]Entry, len(byID))
	for _, entry := range slice {
		f := folder(&entry)
		children[f.ParentID] = append(children[f.ParentID], entry)
	}

	for _, subtree := range children {
		slices.SortFunc(subtree, func(a, b Entry) int {
			return cmp(folder(&a), folder(&b))
		})
	}

	slices.SortFunc(roots, func(a, b Entry) int {
		return cmp(folder(&a), folder(&b))
	})

	out := slice[:0]
	for _, root := range roots {
		putWithChildren(&out, children, root, folder)
	}

	return out
}
