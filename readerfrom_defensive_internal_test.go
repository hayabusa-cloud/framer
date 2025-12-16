package framer

import (
	"encoding/binary"
	"io"
	"testing"
)

type oneReadSrc struct {
	done bool
}

func (s *oneReadSrc) Read(p []byte) (int, error) {
	if s.done {
		return 0, io.EOF
	}
	s.done = true
	if len(p) == 0 {
		return 0, nil
	}
	p[0] = 'x'
	return 1, nil
}

func TestWriter_ReadFrom_DefensiveShortWriteWhenInternalStateAlreadyComplete(t *testing.T) {
	// This exercises the defensive `wn != n` branch in (*Writer).ReadFrom.
	// It is only reachable if the internal stream writer state is inconsistent.
	fr := &framer{wr: io.Discard, wbo: binary.BigEndian, wpr: BinaryStream}
	fr.length = 1
	// For length=1, hdrSize=1 (exLen=0), so "complete" offset is 2.
	fr.offset = 2

	w := &Writer{fr: fr}
	_, err := w.ReadFrom(&oneReadSrc{})
	if err != io.ErrShortWrite {
		t.Fatalf("err=%v want io.ErrShortWrite", err)
	}
}
