package framer_test

import (
	"errors"
	"io"
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

func TestStreamRead_NoProgressGuard(t *testing.T) {
	r := fr.NewReader(&noProgressReader{}, fr.WithProtocol(fr.BinaryStream))
	buf := make([]byte, 8)
	_, err := r.Read(buf)
	if !errors.Is(err, io.ErrNoProgress) {
		t.Fatalf("want io.ErrNoProgress, got %v", err)
	}
}

type noProgressReader struct{}

func (*noProgressReader) Read(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	return 0, nil
}

func TestStreamWrite_NoProgressGuard(t *testing.T) {
	w := fr.NewWriter(&noProgressWriter{}, fr.WithProtocol(fr.BinaryStream))
	_, err := w.Write([]byte("x"))
	if !errors.Is(err, io.ErrShortWrite) {
		t.Fatalf("want io.ErrShortWrite, got %v", err)
	}
}

type noProgressWriter struct{}

func (*noProgressWriter) Write(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	return 0, nil
}
