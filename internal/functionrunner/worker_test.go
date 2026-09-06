package functionrunner

import (
	"crypto/sha256"
	"encoding/hex"
	"io"
	"strings"
	"testing"
)

func TestChecksumReaderHashesExactlyWhatWasRead(t *testing.T) {
	input := "archive bytes are opaque"
	reader := newChecksumReader(strings.NewReader(input))
	got, err := io.ReadAll(reader)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != input {
		t.Fatalf("ReadAll() = %q, want %q", got, input)
	}
	wantHash := sha256.Sum256([]byte(input))
	if reader.SumHex() != hex.EncodeToString(wantHash[:]) {
		t.Fatalf("SumHex() = %q, want %q", reader.SumHex(), hex.EncodeToString(wantHash[:]))
	}
}

func TestChecksumReaderHandlesNilReader(t *testing.T) {
	reader := newChecksumReader(nil)
	if _, err := reader.Read(make([]byte, 1)); err != io.EOF {
		t.Fatalf("Read() error = %v, want io.EOF", err)
	}
	if reader.SumHex() == "" {
		t.Fatal("SumHex() returned empty hash")
	}
}
