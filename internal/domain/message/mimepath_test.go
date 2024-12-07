package message

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestPath_IsChildOf(t *testing.T) {
	require.True(t, Path{1}.IsChildOf(Path{}))
	require.False(t, Path{}.IsChildOf(Path{1}))
	require.True(t, Path{1, 2}.IsChildOf(Path{1}))
	require.False(t, Path{1, 2}.IsChildOf(Path{1, 2, 3}))
	require.False(t, Path{1, 2, 3}.IsChildOf(Path{1, 2, 3}))
	require.False(t, Path{1, 2, 3, 4, 5}.IsChildOf(Path{1, 2, 3}))
	require.False(t, Path{1, 2, 4, 4, 5}.IsChildOf(Path{1, 2, 3}))
}

func TestPath_IsDescendantOf(t *testing.T) {
	require.True(t, Path{1}.IsDescendantOf(Path{}))
	require.False(t, Path{}.IsDescendantOf(Path{1}))
	require.True(t, Path{1, 2}.IsDescendantOf(Path{1}))
	require.False(t, Path{1, 2}.IsDescendantOf(Path{1, 2, 3}))
	require.False(t, Path{1, 2, 3}.IsDescendantOf(Path{1, 2, 3}))
	require.True(t, Path{1, 2, 3, 4, 5}.IsDescendantOf(Path{1, 2, 3}))
	require.False(t, Path{1, 2, 4, 4, 5}.IsDescendantOf(Path{1, 2, 3}))
}

func TestPathFromString(t *testing.T) {
	cases := []struct {
		str     string
		val     Path
		invalid bool
	}{
		{
			str: "",
			val: EmptyPath(),
		},
		{
			str: "1",
			val: []int{1},
		},
		{
			str: "1.2",
			val: []int{1, 2},
		},
		{
			str:     "1.",
			invalid: true,
		},
		{
			str:     ".",
			invalid: true,
		},
		{
			str:     "1..2",
			invalid: true,
		},
	}
	for _, c := range cases {
		t.Run(c.str, func(t *testing.T) {
			path, err := PathFromString(c.str)
			if c.invalid {
				require.NotNil(t, t, err)
				return
			} else {
				require.NoError(t, err)
			}
			require.Equal(t, path, c.val)
		})
	}
}
