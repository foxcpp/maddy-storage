package searcher

import (
	"strconv"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestCond_And_MergesContentConditions(t *testing.T) {
	cases := map[string]struct {
		Base  Cond
		Other Cond
		Want  Cond
	}{
		"both set": {
			Base: Cond{
				Header:       []HeaderField{{Key: "Subject", Value: "a"}},
				InHeaderBody: []string{"text1"},
				InBodyOnly:   []string{"body1"},
			},
			Other: Cond{
				Header:       []HeaderField{{Key: "From", Value: "b"}},
				InHeaderBody: []string{"text2"},
				InBodyOnly:   []string{"body2"},
			},
			Want: Cond{
				Header:       []HeaderField{{Key: "Subject", Value: "a"}, {Key: "From", Value: "b"}},
				InHeaderBody: []string{"text1", "text2"},
				InBodyOnly:   []string{"body1", "body2"},
			},
		},
		"base empty": {
			Base: Cond{},
			Other: Cond{
				Header:       []HeaderField{{Key: "From", Value: "b"}},
				InHeaderBody: []string{"text2"},
				InBodyOnly:   []string{"body2"},
			},
			Want: Cond{
				Header:       []HeaderField{{Key: "From", Value: "b"}},
				InHeaderBody: []string{"text2"},
				InBodyOnly:   []string{"body2"},
			},
		},
		"other empty": {
			Base: Cond{
				Header:       []HeaderField{{Key: "Subject", Value: "a"}},
				InHeaderBody: []string{"text1"},
				InBodyOnly:   []string{"body1"},
			},
			Other: Cond{},
			Want: Cond{
				Header:       []HeaderField{{Key: "Subject", Value: "a"}},
				InHeaderBody: []string{"text1"},
				InBodyOnly:   []string{"body1"},
			},
		},
	}

	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			base := c.Base
			base.And(&c.Other)

			require.Equal(t, c.Want.Header, base.Header)
			require.Equal(t, c.Want.InHeaderBody, base.InHeaderBody)
			require.Equal(t, c.Want.InBodyOnly, base.InBodyOnly)
		})
	}
}

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
