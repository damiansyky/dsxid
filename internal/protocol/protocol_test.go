package protocol

import (
	"crypto/ed25519"
	"encoding/base64"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"dsxid/internal/identity"
)

func testKey(t *testing.T) (ed25519.PublicKey, ed25519.PrivateKey, string) {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	return pub, priv, identity.DeriveID(pub)
}

func TestSignAndVerify(t *testing.T) {
	pub, priv, kid := testKey(t)
	now := time.Now().UTC().Truncate(time.Second)
	nonce, err := NewChallenge()
	if err != nil {
		t.Fatal(err)
	}
	compact, st, err := Sign(priv, kid, "svc.example", nonce, now, 90*time.Second)
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	if st.KeyID != kid || st.Audience != "svc.example" || st.Nonce != nonce {
		t.Fatalf("unexpected statement: %+v", st)
	}
	got, err := Verify(compact, pub, "svc.example", nonce, now.Add(time.Second))
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if !reflect.DeepEqual(got, st) {
		t.Fatalf("statement changed")
	}
}

func TestSignWithProfile(t *testing.T) {
	pub, priv, kid := testKey(t)
	now := time.Now().UTC().Truncate(time.Second)
	profile := map[string]string{"fullName": "Alex", "email": "alex@example.com"}
	compact, _, err := SignWithProfile(priv, kid, "shop.example", "challenge", profile, now, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	got, err := Verify(compact, pub, "shop.example", "challenge", now)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got.Profile, profile) {
		t.Fatalf("profile = %+v", got.Profile)
	}
}

func TestSignRejectsInvalidProfile(t *testing.T) {
	_, priv, kid := testKey(t)
	now := time.Now()
	if _, _, err := SignWithProfile(priv, kid, "shop", "n", map[string]string{"bad key": "x"}, now, time.Minute); err == nil {
		t.Fatal("invalid profile key should fail")
	}
	if _, _, err := SignWithProfile(priv, kid, "shop", "n", map[string]string{"fullName": "line\nbreak"}, now, time.Minute); err == nil {
		t.Fatal("invalid profile value should fail")
	}
}

func TestVerifyRejectsWrongAudience(t *testing.T) {
	pub, priv, kid := testKey(t)
	now := time.Now()
	compact, _, _ := Sign(priv, kid, "svc.a", "n", now, time.Minute)
	if _, err := Verify(compact, pub, "svc.b", "n", now); !errors.Is(err, ErrWrongAudience) {
		t.Fatalf("want audience error, got %v", err)
	}
}

func TestVerifyRejectsWrongNonce(t *testing.T) {
	pub, priv, kid := testKey(t)
	now := time.Now()
	compact, _, _ := Sign(priv, kid, "svc", "n1", now, time.Minute)
	if _, err := Verify(compact, pub, "svc", "n2", now); !errors.Is(err, ErrWrongNonce) {
		t.Fatalf("want nonce error, got %v", err)
	}
}

func TestVerifyRejectsWrongKey(t *testing.T) {
	_, priv, kid := testKey(t)
	pub2, _, _ := testKey(t)
	now := time.Now()
	compact, _, _ := Sign(priv, kid, "svc", "n", now, time.Minute)
	if _, err := Verify(compact, pub2, "svc", "n", now); !errors.Is(err, ErrWrongKey) {
		t.Fatalf("want key error, got %v", err)
	}
}

func TestVerifyRejectsTamperedSignature(t *testing.T) {
	pub, priv, kid := testKey(t)
	now := time.Now()
	compact, _, _ := Sign(priv, kid, "svc", "n", now, time.Minute)
	parts := strings.Split(compact, ".")
	sig, _ := base64.RawURLEncoding.DecodeString(parts[2])
	sig[0] ^= 0xff
	parts[2] = base64.RawURLEncoding.EncodeToString(sig)
	tampered := strings.Join(parts, ".")
	if _, err := Verify(tampered, pub, "svc", "n", now); !errors.Is(err, ErrBadSignature) {
		t.Fatalf("want bad signature, got %v", err)
	}
}

func TestVerifyRejectsExpired(t *testing.T) {
	pub, priv, kid := testKey(t)
	now := time.Now().UTC().Truncate(time.Second).Add(-3 * time.Minute)
	compact, _, _ := Sign(priv, kid, "svc", "n", now, time.Minute)
	if _, err := Verify(compact, pub, "svc", "n", now.Add(2*time.Minute)); !errors.Is(err, ErrExpired) {
		t.Fatalf("want expired, got %v", err)
	}
}

func TestVerifyRejectsFuture(t *testing.T) {
	pub, priv, kid := testKey(t)
	now := time.Now().UTC().Truncate(time.Second).Add(10 * time.Minute)
	compact, _, _ := Sign(priv, kid, "svc", "n", now, time.Minute)
	if _, err := Verify(compact, pub, "svc", "n", time.Now()); !errors.Is(err, ErrNotYetValid) {
		t.Fatalf("want not yet valid, got %v", err)
	}
}

func TestSignRejectsMismatchedKeyID(t *testing.T) {
	_, priv, _ := testKey(t)
	if _, _, err := Sign(priv, "DSX-AAAA-AAAA-AAAA-AAAA", "svc", "n", time.Now(), time.Minute); !errors.Is(err, ErrWrongKey) {
		t.Fatalf("want wrong key, got %v", err)
	}
}

func TestMalformedAssertions(t *testing.T) {
	pub, _, _ := testKey(t)
	for _, s := range []string{"", "dsx1", "dsx1.abc", "dsx2.e30.e30", "dsx1.!!!!.!!!!"} {
		if _, err := Verify(s, pub, "svc", "n", time.Now()); !errors.Is(err, ErrMalformed) {
			t.Fatalf("%q: want malformed, got %v", s, err)
		}
	}
}
