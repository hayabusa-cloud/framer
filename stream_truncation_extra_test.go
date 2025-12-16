package framer_test

import (
	"bytes"
	"errors"
	"io"
	"testing"

	fr "code.hybscloud.com/framer"
)

func TestStreamRead_Truncated56BitLengthHeader_ReturnsUnexpectedEOF(t *testing.T) {
	// 0xFF indicates 7-byte extended length, but we provide too few bytes.
	wire := []byte{0xFF, 0x01}
	r := fr.NewReader(bytes.NewReader(wire), fr.WithReadTCP()).(*fr.Reader)

	buf := make([]byte, 8)
	n, err := r.Read(buf)
	if n != 0 || !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatalf("want (0, io.ErrUnexpectedEOF), got (%d, %v)", n, err)
	}
}
