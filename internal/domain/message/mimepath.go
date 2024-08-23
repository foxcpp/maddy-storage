package message

import (
	"fmt"
	"strconv"
	"strings"
)

type Path []int

func EmptyPath() Path {
	return Path(nil)
}

func (p Path) Empty() bool {
	return len(p) == 0
}

func (p Path) Depth() int {
	return len(p)
}

func (p Path) NextSibling() Path {
	if p.Empty() {
		return p
	}

	sibling := make(Path, len(p))
	copy(sibling[:], p)
	sibling[len(sibling)-1]++

	return sibling
}

func (p Path) FirstChild() Path {
	child := make(Path, len(p)+1)
	copy(child[:], p)

	child[len(child)-1] = 1
	return child
}

func (p Path) String() string {
	if len(p) == 0 {
		return ""
	}
	b := strings.Builder{}
	for i, indx := range p {
		b.WriteString(strconv.Itoa(indx))
		if i != len(p)-1 {
			b.WriteByte('.')
		}
	}
	return b.String()
}

func PathFromString(s string) (Path, error) {
	if s == "" {
		return EmptyPath(), nil
	}

	parts := strings.Split(s, ".")
	path := make([]int, len(parts))
	for i, p := range parts {
		indx, err := strconv.Atoi(p)
		if err != nil {
			return nil, fmt.Errorf("invalid index at %d: %v", i, err)
		}
		path[i] = indx
	}

	return path, nil
}
