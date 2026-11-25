package identity

import (
	"bytes"
	"crypto/ed25519"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const testPass = "correct horse battery"

func newTestStore(t *testing.T) *Store {
	t.Helper()
	return NewStore(t.TempDir())
}

func TestCreateAndUnlock(t *testing.T) {
	s := newTestStore(t)
	if s.Exists() {
		t.Fatal("new store should be empty")
	}
	id, err := s.Create("  Alex  ", testPass)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	defer id.Close()
	if id.Label != "Alex" {
		t.Fatalf("label = %q", id.Label)
	}
	if id.ID != DeriveID(id.PublicKey) {
		t.Fatalf("id does not match public key")
	}
	if !ValidID(id.ID) {
		t.Fatalf("invalid id shape: %q", id.ID)
	}
	if len(id.PrivateKey) != ed25519.PrivateKeySize {
		t.Fatalf("bad private key size")
	}
	if !s.Exists() {
		t.Fatal("file should exist")
	}

	meta, err := s.Meta()
	if err != nil {
		t.Fatalf("meta: %v", err)
	}
	if meta.ID != id.ID || meta.Label != "Alex" || !meta.CreatedAt.Equal(id.CreatedAt) {
		t.Fatalf("meta mismatch: %+v vs %+v", meta, id.Meta)
	}
	if !strings.Contains(meta.PublicKeyPEM(), "BEGIN PUBLIC KEY") {
		t.Fatal("missing PEM public key")
	}

	unlocked, err := s.Unlock(testPass)
	if err != nil {
		t.Fatalf("unlock: %v", err)
	}
	defer unlocked.Close()
	if !bytes.Equal(unlocked.PrivateKey, id.PrivateKey) {
		t.Fatal("unlocked key differs")
	}

	if _, err := s.Unlock("wrong passphrase"); !errors.Is(err, ErrWrongPassphrase) {
		t.Fatalf("wrong passphrase error = %v", err)
	}
}

func TestCreateAlreadyExists(t *testing.T) {
	s := newTestStore(t)
	id, err := s.Create("", testPass)
	if err != nil {
		t.Fatal(err)
	}
	id.Close()
	if _, err := s.Create("", testPass); !errors.Is(err, ErrIdentityExists) {
		t.Fatalf("want ErrIdentityExists, got %v", err)
	}
}

func TestPassphraseRules(t *testing.T) {
	s := newTestStore(t)
	if _, err := s.Create("A", "short"); !errors.Is(err, ErrPassphraseTooShort) {
		t.Fatalf("want short error, got %v", err)
	}
	if s.Exists() {
		t.Fatal("failed create must not leave a file")
	}
	id, err := s.Create("A", testPass)
	if err != nil {
		t.Fatal(err)
	}
	id.Close()
	if err := s.ChangePassphrase(testPass, "tiny"); !errors.Is(err, ErrPassphraseTooShort) {
		t.Fatalf("want short error, got %v", err)
	}
}

func TestChangePassphrase(t *testing.T) {
	s := newTestStore(t)
	id, err := s.Create("Alex", testPass)
	if err != nil {
		t.Fatal(err)
	}
	origPub := append(ed25519.PublicKey(nil), id.PublicKey...)
	origCreated := id.CreatedAt
	id.Close()

	if err := s.ChangePassphrase("wrong old one", "new passphrase 123"); !errors.Is(err, ErrWrongPassphrase) {
		t.Fatalf("want wrong passphrase, got %v", err)
	}
	if err := s.ChangePassphrase(testPass, "new passphrase 123"); err != nil {
		t.Fatalf("change: %v", err)
	}
	if _, err := s.Unlock(testPass); !errors.Is(err, ErrWrongPassphrase) {
		t.Fatalf("old passphrase should fail, got %v", err)
	}
	unlocked, err := s.Unlock("new passphrase 123")
	if err != nil {
		t.Fatalf("unlock new: %v", err)
	}
	defer unlocked.Close()
	if !bytes.Equal(unlocked.PublicKey, origPub) {
		t.Fatal("key changed during passphrase change")
	}
	if !unlocked.CreatedAt.Equal(origCreated) {
		t.Fatal("creation time changed")
	}
}

func TestExportImport(t *testing.T) {
	s := newTestStore(t)
	id, err := s.Create("Backup", testPass)
	if err != nil {
		t.Fatal(err)
	}
	id.Close()
	backup := filepath.Join(t.TempDir(), "backup.json")
	if err := s.Export(backup); err != nil {
		t.Fatalf("export: %v", err)
	}
	if st, err := os.Stat(backup); err != nil || st.Size() == 0 {
		t.Fatalf("backup missing: %v", err)
	}

	s2 := newTestStore(t)
	if err := s2.Import(backup); err != nil {
		t.Fatalf("import: %v", err)
	}
	got, err := s2.Unlock(testPass)
	if err != nil {
		t.Fatalf("unlock imported: %v", err)
	}
	defer got.Close()
	if got.ID != id.ID {
		t.Fatalf("imported id = %s, want %s", got.ID, id.ID)
	}
	if err := s2.Import(backup); !errors.Is(err, ErrIdentityExists) {
		t.Fatalf("import over existing should fail, got %v", err)
	}
}

func TestImportRejectsGarbage(t *testing.T) {
	s := newTestStore(t)
	path := filepath.Join(t.TempDir(), "bad.json")
	if err := os.WriteFile(path, []byte(`{"version":1,"id":"nope"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := s.Import(path); !errors.Is(err, ErrCorrupt) {
		t.Fatalf("want corrupt error, got %v", err)
	}
}

func TestTamperedFileIsRejected(t *testing.T) {
	s := newTestStore(t)
	id, err := s.Create("Alex", testPass)
	if err != nil {
		t.Fatal(err)
	}
	id.Close()

	raw, err := os.ReadFile(s.Path())
	if err != nil {
		t.Fatal(err)
	}
	var f map[string]any
	if err := json.Unmarshal(raw, &f); err != nil {
		t.Fatal(err)
	}
	pub := f["public_key"].(string)
	f["public_key"] = "A" + pub[1:]
	raw, _ = json.Marshal(f)
	if err := os.WriteFile(s.Path(), raw, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Meta(); !errors.Is(err, ErrCorrupt) {
		t.Fatalf("want corrupt, got %v", err)
	}
	if _, err := s.Unlock(testPass); !errors.Is(err, ErrCorrupt) {
		t.Fatalf("want corrupt, got %v", err)
	}
}

func TestDelete(t *testing.T) {
	s := newTestStore(t)
	id, err := s.Create("", testPass)
	if err != nil {
		t.Fatal(err)
	}
	id.Close()
	if err := s.Delete(); err != nil {
		t.Fatal(err)
	}
	if s.Exists() {
		t.Fatal("file still exists")
	}
	if _, err := s.Meta(); !errors.Is(err, ErrNoIdentity) {
		t.Fatalf("want no identity, got %v", err)
	}
	if err := s.Delete(); err != nil {
		t.Fatalf("second delete: %v", err)
	}
}

func TestDeriveIDIsStable(t *testing.T) {
	seed := bytes.Repeat([]byte{7}, ed25519.SeedSize)
	priv := ed25519.NewKeyFromSeed(seed)
	pub := priv.Public().(ed25519.PublicKey)
	a, b := DeriveID(pub), DeriveID(pub)
	if a != b || !ValidID(a) {
		t.Fatalf("unstable or invalid id: %q %q", a, b)
	}
	for _, bad := range []string{"", "DSX-AAAA", "dsx-AAAA-BBBB-CCCC-DDDD", "DSX-AAAA-BBBB-CCCC-DDD0"} {
		if ValidID(bad) {
			t.Fatalf("ValidID accepted %q", bad)
		}
	}
}
