package crypto

import (
	"encoding/base64"
	"testing"
)

func TestParseKey(t *testing.T) {
	t.Parallel()
	if _, err := ParseKey(""); err != ErrMissingKey {
		t.Fatalf("empty: got %v want ErrMissingKey", err)
	}
	if _, err := ParseKey("not-base64!!"); err != ErrInvalidKey {
		t.Fatalf("bad base64: got %v want ErrInvalidKey", err)
	}
	short := base64.StdEncoding.EncodeToString(make([]byte, 16))
	if _, err := ParseKey(short); err != ErrInvalidKey {
		t.Fatalf("short key: got %v want ErrInvalidKey", err)
	}
	good := base64.StdEncoding.EncodeToString(make([]byte, 32))
	key, err := ParseKey(good)
	if err != nil || len(key) != 32 {
		t.Fatalf("good key: %v len=%d", err, len(key))
	}
}

func TestEncryptDecryptRoundTrip(t *testing.T) {
	t.Parallel()
	key := make([]byte, 32)
	for i := range key {
		key[i] = byte(i)
	}
	cipher, err := Encrypt(key, []byte("secret-password"))
	if err != nil {
		t.Fatal(err)
	}
	plain, err := Decrypt(key, cipher)
	if err != nil {
		t.Fatal(err)
	}
	if string(plain) != "secret-password" {
		t.Fatalf("got %q", plain)
	}
	if _, err := Decrypt(key, []byte("too-short")); err != ErrInvalidCipher {
		t.Fatalf("corrupt: got %v", err)
	}
}
