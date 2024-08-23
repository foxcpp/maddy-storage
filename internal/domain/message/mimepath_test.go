package message

import (
	"testing"

	"github.com/stretchr/testify/require"
)

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
