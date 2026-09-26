package secretkey

import (
	"bytes"
	"encoding/base64"
	"testing"
)

func TestDecode32ByteKeySupportedEncodingsAndFailures(t *testing.T) {
	key := bytes.Repeat([]byte{0x6d}, KeySize)
	tests := []struct {
		name    string
		encoded string
		valid   bool
	}{
		{name: "standard base64", encoded: base64.StdEncoding.EncodeToString(key), valid: true},
		{name: "raw url base64", encoded: base64.RawURLEncoding.EncodeToString(key), valid: true},
		{name: "invalid base64", encoded: "not-base64"},
		{name: "wrong decoded length", encoded: base64.StdEncoding.EncodeToString(key[:KeySize-1])},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			decoded, err := Decode32ByteKey(test.encoded)
			if !test.valid {
				if err == nil || decoded != nil {
					t.Fatalf("Decode32ByteKey() = %x, %v; want invalid key", decoded, err)
				}
				return
			}
			if err != nil || !bytes.Equal(decoded, key) {
				t.Fatalf("Decode32ByteKey() = %x, %v", decoded, err)
			}
		})
	}
}
