package common

import (
	"strings"
	"testing"
)

func withCryptoSecret(t *testing.T, secret string, fn func()) {
	t.Helper()
	prev := CryptoSecret
	CryptoSecret = secret
	defer func() { CryptoSecret = prev }()
	fn()
}

func TestEncryptDecryptRoundTrip(t *testing.T) {
	withCryptoSecret(t, "test-secret-aaa", func() {
		cases := []string{"", "short", "中文 token 🚀", strings.Repeat("x", 1024)}
		for _, p := range cases {
			ct, err := EncryptSecret(p)
			if err != nil {
				t.Fatalf("encrypt %q: %v", p, err)
			}
			if !IsEncryptedSecret(ct) {
				t.Fatalf("expect prefix on %q -> %q", p, ct)
			}
			out, err := DecryptSecret(ct)
			if err != nil {
				t.Fatalf("decrypt %q: %v", p, err)
			}
			if out != p {
				t.Fatalf("roundtrip mismatch: want %q got %q", p, out)
			}
		}
	})
}

func TestEncryptIdempotent(t *testing.T) {
	withCryptoSecret(t, "test-secret-bbb", func() {
		ct, err := EncryptSecret("hello")
		if err != nil {
			t.Fatal(err)
		}
		ct2, err := EncryptSecret(ct)
		if err != nil {
			t.Fatal(err)
		}
		if ct != ct2 {
			t.Fatalf("encrypt of already-encrypted should be no-op, got %q vs %q", ct, ct2)
		}
	})
}

func TestDecryptPlaintextPassthrough(t *testing.T) {
	withCryptoSecret(t, "test-secret-ccc", func() {
		out, err := DecryptSecret("legacy-plaintext")
		if err != nil {
			t.Fatal(err)
		}
		if out != "legacy-plaintext" {
			t.Fatalf("plaintext should pass through, got %q", out)
		}
	})
}

func TestDecryptWrongKey(t *testing.T) {
	var ct string
	withCryptoSecret(t, "key-A", func() {
		c, err := EncryptSecret("payload")
		if err != nil {
			t.Fatal(err)
		}
		ct = c
	})
	withCryptoSecret(t, "key-B", func() {
		if _, err := DecryptSecret(ct); err == nil {
			t.Fatal("expected decrypt failure with wrong key")
		}
	})
}

func TestDecryptTamperedCiphertext(t *testing.T) {
	withCryptoSecret(t, "test-secret-ddd", func() {
		ct, err := EncryptSecret("payload-x")
		if err != nil {
			t.Fatal(err)
		}
		tampered := ct[:len(ct)-2] + "AA"
		if tampered == ct {
			t.Fatal("tamper helper failed")
		}
		if _, err := DecryptSecret(tampered); err == nil {
			t.Fatal("expected GCM auth failure on tampered ciphertext")
		}
	})
}

func TestDecryptEmpty(t *testing.T) {
	withCryptoSecret(t, "test-secret-eee", func() {
		if _, err := DecryptSecret(""); err == nil {
			t.Fatal("expected error on empty ciphertext")
		}
	})
}
