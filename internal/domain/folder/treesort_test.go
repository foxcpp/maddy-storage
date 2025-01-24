package folder

import (
	"math/rand"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/oklog/ulid/v2"
	"github.com/stretchr/testify/require"
)

func TestSortAsTree(t *testing.T) {
	folder3, err := NewFolder(nil, ulid.ULID{}, "folder3", RoleNone)
	require.NoError(t, err)

	folder2, err := NewFolder(nil, ulid.ULID{}, "folder2", RoleNone)
	require.NoError(t, err)

	folder1, err := NewFolder(nil, ulid.ULID{}, "folder1", RoleNone)
	require.NoError(t, err)

	folder1Sub3, err := NewFolder(folder1, ulid.ULID{}, "sub3", RoleNone)
	require.NoError(t, err)

	folder1Sub1, err := NewFolder(folder1, ulid.ULID{}, "sub1", RoleNone)
	require.NoError(t, err)

	folder1Sub1Sub2, err := NewFolder(folder1Sub1, ulid.ULID{}, "sub2", RoleNone)
	require.NoError(t, err)

	folders := []*Folder{folder1Sub1, folder1, folder2, folder1Sub1Sub2, folder3, folder1Sub3}

	for i := 0; i < 100; i++ {
		rand.Shuffle(len(folders), func(i, j int) {
			folders[i], folders[j] = folders[j], folders[i]
		})

		folderPaths := make([]string, len(folders))
		for i, folder := range folders {
			folderPaths[i] = folder.Path
		}
		t.Log("original sequence:", folderPaths)

		SortAsTree(folders, func(e **Folder) *Folder {
			return *e
		}, func(a, b *Folder) int {
			return strings.Compare(a.Name, b.Name)
		})

		folderPaths = make([]string, len(folders))
		for i, folder := range folders {
			folderPaths[i] = folder.Path
		}
		require.Equal(t, []string{"folder1", "folder1/sub1", "folder1/sub1/sub2", "folder1/sub3", "folder2", "folder3"}, folderPaths)
	}
}

func generateRandomTree(b *testing.B, size int) []*Folder {
	folders := make([]*Folder, size)

	for i := 0; i < size; i++ {
		parentIndx := rand.Intn(len(folders) + 1)
		var parent *Folder
		if parentIndx != len(folders) {
			parent = folders[parentIndx]
		}

		f, err := NewFolder(parent, ulid.ULID{}, "folder"+strconv.Itoa(i), RoleNone)
		require.NoError(b, err)

		folders[i] = f
	}

	return folders
}

func BenchmarkSortAsTree(b *testing.B) {
	run := func(b *testing.B, n int) {
		folders := generateRandomTree(b, n)
		b.ResetTimer()
		for range b.N {
			folders := slices.Clone(folders)
			SortAsTree(folders, func(e **Folder) *Folder {
				return *e
			}, func(a, b *Folder) int {
				return strings.Compare(a.Name, b.Name)
			})
		}
	}

	b.Run("1", func(b *testing.B) { run(b, 1) })
	b.Run("10", func(b *testing.B) { run(b, 10) })
	b.Run("100", func(b *testing.B) { run(b, 100) })
	b.Run("1000", func(b *testing.B) { run(b, 1000) })
	b.Run("10000", func(b *testing.B) { run(b, 10000) })
}
