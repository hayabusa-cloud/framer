//go:build examples
// +build examples

package examples_test

import (
	"bytes"
	"io"
	"net"
	"testing"

	"code.hybscloud.com/framer"
)

func TestExample_NetPipe_StreamFraming(t *testing.T) {
	t.Parallel()

	// net.Pipe is an in-memory stream. Like TCP, it does NOT preserve message boundaries.
	c1, c2 := net.Pipe()
	defer c1.Close()
	defer c2.Close()

	w := framer.NewWriter(c1, framer.WithWriteTCP())
	r := framer.NewReader(c2, framer.WithReadTCP())

	msgs := [][]byte{
		[]byte("hello"),
		[]byte("world"),
		bytes.Repeat([]byte("A"), 300), // > 253 => extended length encoding
	}

	for i, m := range msgs {
		n, err := w.Write(m)
		if err != nil {
			t.Fatalf("write[%d]: %v", i, err)
		}
		if n != len(m) {
			t.Fatalf("write[%d]: n=%d want=%d", i, n, len(m))
		}
	}

	for i, want := range msgs {
		buf := make([]byte, len(want))
		n, err := r.Read(buf)
		if err != nil {
			t.Fatalf("read[%d]: %v", i, err)
		}
		if n != len(want) {
			t.Fatalf("read[%d]: n=%d want=%d", i, n, len(want))
		}
		if !bytes.Equal(buf[:n], want) {
			t.Fatalf("read[%d]: payload mismatch", i)
		}
	}

	// Sanity: no extra bytes should remain.
	extra := make([]byte, 1)
	n, err := r.Read(extra)
	if err != io.EOF && err != nil {
		t.Fatalf("extra read: n=%d err=%v", n, err)
	}
}
