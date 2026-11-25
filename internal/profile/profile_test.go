package profile

import (
	"crypto/ed25519"
	"crypto/rand"
	"os"
	"strings"
	"testing"
)

func testKey(t *testing.T) ed25519.PrivateKey {
	t.Helper()
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return priv
}

func TestSaveLoadEncrypted(t *testing.T) {
	dir := t.TempDir()
	store := NewStore(dir)
	priv := testKey(t)
	if store.Exists() {
		t.Fatal("new store should be empty")
	}
	if values, err := store.Load(priv); err != nil || len(values) != 0 {
		t.Fatalf("empty load = %v, %v", values, err)
	}

	values := map[string]string{
		"fullName": "  Alex Example  ",
		"email":    "alex@example.com",
		"address1": "Main street 1",
		"city":     "Bratislava",
		"phone":    "",
	}
	if err := store.Save(priv, values); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(store.Path())
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "Alex Example") || strings.Contains(string(raw), "alex@example.com") {
		t.Fatal("profile file contains plaintext")
	}

	loaded, err := store.Load(priv)
	if err != nil {
		t.Fatal(err)
	}
	if loaded["fullName"] != "Alex Example" || loaded["email"] != "alex@example.com" {
		t.Fatalf("unexpected values: %+v", loaded)
	}
	if _, exists := loaded["phone"]; exists {
		t.Fatal("empty fields should not be stored")
	}
	if _, err := store.Load(testKey(t)); err == nil {
		t.Fatal("a different key must not decrypt the profile")
	}
	if err := store.Delete(); err != nil {
		t.Fatal(err)
	}
	if store.Exists() {
		t.Fatal("profile should be deleted")
	}
}

func TestRequestedAndValues(t *testing.T) {
	values := map[string]string{"fullName": "Alex", "email": "alex@example.com"}
	fields := Requested(values, []string{"fullName", "email", "phone", "fullName", "unknown"})
	if len(fields) != 3 {
		t.Fatalf("fields = %d, want 3", len(fields))
	}
	if fields[0].Label != "Full name" || !fields[0].Available {
		t.Fatalf("unexpected first field: %+v", fields[0])
	}
	if fields[2].Key != "phone" || fields[2].Available {
		t.Fatalf("missing phone should be unavailable: %+v", fields[2])
	}
	signed := Values(fields)
	if len(signed) != 2 || signed["fullName"] != "Alex" {
		t.Fatalf("signed values = %+v", signed)
	}
}

func TestNormalizeRejectsBadFields(t *testing.T) {
	priv := testKey(t)
	store := NewStore(t.TempDir())
	for name, values := range map[string]map[string]string{
		"unknown":  {"password": "secret"},
		"too long": {"fullName": strings.Repeat("a", 121)},
		"newline":  {"address1": "line\nbreak"},
	} {
		if err := store.Save(priv, values); err == nil {
			t.Errorf("%s should be rejected", name)
		}
	}
}
