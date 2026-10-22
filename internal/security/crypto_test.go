package security

import (
	"testing"
)

func TestEncryptDecrypt(t *testing.T) {
	cases := []string{
		"ghp_testToken1234567890abcdefghijklmnopqrstuvwxyz",
		"glpat-abcdef1234567890",
		"custom-token-with-special-characters!@#$%^&*()_+",
		"a",
		"",
	}

	for _, tc := range cases {
		enc, err := Encrypt(tc)
		if err != nil {
			t.Fatalf("Encrypt(%q) failed: %v", tc, err)
		}
		if tc == "" {
			if enc != "" {
				t.Fatalf("expected empty string for empty input, got %q", enc)
			}
			continue
		}
		if enc == tc {
			t.Fatalf("ciphertext should not match plaintext: %q", enc)
		}

		dec, err := Decrypt(enc)
		if err != nil {
			t.Fatalf("Decrypt(%q) failed: %v", enc, err)
		}
		if dec != tc {
			t.Fatalf("expected %q, got %q", tc, dec)
		}
	}
}

func TestDecryptTampered(t *testing.T) {
	enc, err := Encrypt("secret-token")
	if err != nil {
		t.Fatalf("Encrypt failed: %v", err)
	}

	// Tamper with base64 data
	tampered := enc[:len(enc)-4] + "AAAA"
	_, err = Decrypt(tampered)
	if err == nil {
		t.Fatal("expected error when decrypting tampered data, got nil")
	}
}
