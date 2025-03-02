package searcher

import (
	"strconv"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestSearchCond_SplitMetaBody(t *testing.T) {
	cases := []struct {
		Input  Cond
		Output SplitSearchCond
	}{
		{
			Input: Cond{
				Flag: []string{"hello"},
			},
			Output: SplitSearchCond{
				Metadata: &Cond{
					Flag: []string{"hello"},
				},
				Body: nil,
			},
		},
		{
			Input: Cond{
				Flag:         []string{"hello"},
				InHeaderBody: []string{"pattern"},
			},
			Output: SplitSearchCond{
				Metadata: &Cond{
					Flag: []string{"hello"},
				},
				Body: &Cond{
					InHeaderBody: []string{"pattern"},
				},
			},
		},
		{
			Input: Cond{
				Flag:         []string{"hello"},
				InHeaderBody: []string{"pattern"},
				Not: []*Cond{
					{
						Flag: []string{"extra"},
					},
				},
			},
			Output: SplitSearchCond{ // TODO: more advanced splitting
				Metadata: nil,
				Body: &Cond{
					InHeaderBody: []string{"pattern"},
					Flag:         []string{"hello"},
					Not: []*Cond{
						{
							Flag: []string{"extra"},
						},
					},
				},
			},
		},
	}

	for i, c := range cases {
		t.Run(strconv.Itoa(i), func(t *testing.T) {
			split := c.Input.SplitMetaBody()
			require.Equal(t, c.Output.Metadata, split.Metadata, "metadata cond does not match")
			require.Equal(t, c.Output.Body, split.Body, "body cond does not match")
		})
	}
}
