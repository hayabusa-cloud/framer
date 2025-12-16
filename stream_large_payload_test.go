package framer_test

import (
	"bytes"
	"testing"

	fr "code.hybscloud.com/framer"
)

func TestStreamRoundTrip_LargePayload_Uses56BitLength(t *testing.T) {
	// Exercise the 7-byte (56-bit) length encoding/decoding path.
	payload := bytes.Repeat([]byte{'x'}, 70<<10) // > 65535

	var raw bytes.Buffer
	w := fr.NewWriter(&raw, fr.WithWriteTCP()).(*fr.Writer)
	r := fr.NewReader(&raw, fr.WithReadTCP()).(*fr.Reader)

	wn, we := w.Write(payload)
	if we != nil || wn != len(payload) {
		t.Fatalf("write wn=%d we=%v", wn, we)
	}

	buf := make([]byte, len(payload))
	rn, re := r.Read(buf)
	if re != nil || rn != len(payload) {
		t.Fatalf("read rn=%d re=%v", rn, re)
	}
	if !bytes.Equal(buf, payload) {
		t.Fatalf("payload mismatch")
	}
}

func TestStreamRoundTrip_LargePayload_LocalEndian_Uses56BitLength(t *testing.T) {
	// Cover the little-endian 56-bit length decoding path in readStream on LE hosts.
	payload := bytes.Repeat([]byte{'y'}, 70<<10) // > 65535

	var raw bytes.Buffer
	w := fr.NewWriter(&raw, fr.WithWriteLocal()).(*fr.Writer)
	r := fr.NewReader(&raw, fr.WithReadLocal()).(*fr.Reader)

	wn, we := w.Write(payload)
	if we != nil || wn != len(payload) {
		t.Fatalf("write wn=%d we=%v", wn, we)
	}

	buf := make([]byte, len(payload))
	rn, re := r.Read(buf)
	if re != nil || rn != len(payload) {
		t.Fatalf("read rn=%d re=%v", rn, re)
	}
	if !bytes.Equal(buf, payload) {
		t.Fatalf("payload mismatch")
	}
}
