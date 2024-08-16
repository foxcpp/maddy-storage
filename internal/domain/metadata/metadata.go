package metadata

type Md map[string]string

func New() Md {
	return make(Md)
}

func (m Md) Set(key, value string) {
	m[key] = value
}

func (m Md) Get(key string) (value string, ok bool) {
	val, ok := m[key]
	return val, ok
}

func (m Md) Copy() Md {
	cpy := make(map[string]string, len(m))
	for k, v := range m {
		cpy[k] = v
	}
	return cpy
}
