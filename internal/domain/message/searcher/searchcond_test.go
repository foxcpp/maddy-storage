package searcher

import (
	"strconv"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestSearchCond_SplitMetaBody(t *testing.T) {
	cases := []struct {
		Input  Cond
		Output []SplitSearchCond
	}{
		{
			Input: Cond{
				Flag: []string{"hello"},
			},
		},
	}

	for i, c := range cases {
		t.Run(strconv.Itoa(i), func(t *testing.T) {
			require.Equal(t, c.Output, c.Input.SplitMetaBody())
		})
	}

}
