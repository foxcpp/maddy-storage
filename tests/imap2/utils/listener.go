package utils

import (
	"net"
)

type testListener struct {
	conns chan net.Conn
}

func (t *testListener) Connect() net.Conn {
	if t.conns == nil {
		panic("cannot create connections for closed listener")
	}

	c1, c2 := net.Pipe()
	t.conns <- c1
	return c2
}

func (t *testListener) Accept() (net.Conn, error) {
	if t.conns == nil {
		return nil, net.ErrClosed
	}
	conn, ok := <-t.conns
	if !ok {
		return nil, net.ErrClosed
	}
	return conn, nil
}

func (t *testListener) Close() error {
	close(t.conns)
	t.conns = nil
	return nil
}

func (t *testListener) Addr() net.Addr {
	return &net.TCPAddr{
		IP:   net.IPv4(127, 0, 0, 1),
		Port: 143,
	}
}

func TestListener() (net.Listener, func() net.Conn) {
	l := &testListener{
		conns: make(chan net.Conn, 1),
	}
	return l, l.Connect
}
