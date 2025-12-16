package framer_test

import (
	"errors"
	"testing"

	fr "code.hybscloud.com/framer"
)

func TestRead_NilReader_ReturnsInvalidArgument(t *testing.T) {
	r := fr.NewReader(nil)
	buf := make([]byte, 1)
	if _, err := r.Read(buf); !errors.Is(err, fr.ErrInvalidArgument) {
		t.Fatalf("err=%v want ErrInvalidArgument", err)
	}
}

func TestWrite_NilWriter_ReturnsInvalidArgument(t *testing.T) {
	w := fr.NewWriter(nil)
	if _, err := w.Write([]byte("x")); !errors.Is(err, fr.ErrInvalidArgument) {
		t.Fatalf("err=%v want ErrInvalidArgument", err)
	}
}
