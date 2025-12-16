//go:build examples
// +build examples

package examples_test

import (
	"bytes"
	"fmt"
	"net"
	"testing"
	"time"

	"code.hybscloud.com/framer"
)

func TestExample_TCP_StreamFramingAndEcho(t *testing.T) {
	t.Parallel()

	// A real TCP test (Listen/Dial) is inherently flaky on shared CI (ports, firewalls, timing).
	// net.Pipe is a deterministic in-memory *stream* connection, which matches the "TCP binary stream"
	// property that matters for framer: message boundaries are NOT preserved by the transport.
	cClient, cServer := net.Pipe()
	defer cClient.Close()
	defer cServer.Close()

	type serverResult struct {
		err error
	}
	srvCh := make(chan serverResult, 1)

	// Server: read 3 framed messages, echo each back as one framed message.
	go func() {
		rw := framer.NewReadWriter(cServer, cServer, framer.WithReadTCP(), framer.WithWriteTCP())

		for i := 0; i < 3; i++ {
			buf := make([]byte, 4096)
			n, rerr := rw.Read(buf)
			if rerr != nil {
				srvCh <- serverResult{err: fmt.Errorf("server read[%d]: %w", i, rerr)}
				return
			}
			payload := buf[:n]

			if _, werr := rw.Write(payload); werr != nil {
				srvCh <- serverResult{err: fmt.Errorf("server write[%d]: %w", i, werr)}
				return
			}
		}

		srvCh <- serverResult{err: nil}
	}()

	// Client: write 3 messages, then read 3 echoes.
	rw := framer.NewReadWriter(cClient, cClient, framer.WithReadTCP(), framer.WithWriteTCP())

	msgs := [][]byte{
		[]byte("hello over tcp"),
		bytes.Repeat([]byte("B"), 260), // > 253 => extended length encoding in the frame
		[]byte("bye"),
	}

	for i, m := range msgs {
		n, err := rw.Write(m)
		if err != nil {
			t.Fatalf("client write[%d]: %v", i, err)
		}
		if n != len(m) {
			t.Fatalf("client write[%d]: n=%d want=%d", i, n, len(m))
		}
	}

	for i, want := range msgs {
		buf := make([]byte, 4096)
		n, err := rw.Read(buf)
		if err != nil {
			t.Fatalf("client read[%d]: %v", i, err)
		}
		got := buf[:n]
		if !bytes.Equal(got, want) {
			t.Fatalf("echo mismatch[%d]: got=%q want=%q", i, got, want)
		}
	}

	select {
	case res := <-srvCh:
		if res.err != nil {
			t.Fatalf("server: %v", res.err)
		}
	case <-time.After(2 * time.Second):
		t.Fatalf("timeout waiting for server")
	}
}
