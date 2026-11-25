package approvals

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestRecordAndList(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	if err := s.Record("DSX-AAAA-AAAA-AAAA-AAAA", "http://a.example", now); err != nil {
		t.Fatal(err)
	}
	if err := s.Record("DSX-AAAA-AAAA-AAAA-AAAA", "http://a.example", now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if err := s.Record("DSX-AAAA-AAAA-AAAA-AAAA", "https://b.example", now.Add(2*time.Minute)); err != nil {
		t.Fatal(err)
	}
	list := s.List("DSX-AAAA-AAAA-AAAA-AAAA")
	if len(list) != 2 {
		t.Fatalf("entries = %d, want 2", len(list))
	}
	if list[0].Origin != "https://b.example" {
		t.Fatalf("newest = %q", list[0].Origin)
	}
	if list[1].SignIns != 2 {
		t.Fatalf("sign-ins = %d, want 2", list[1].SignIns)
	}
	if len(s.List("DSX-BBBB-BBBB-BBBB-BBBB")) != 0 {
		t.Fatal("other identity should have no entries")
	}

	reopened, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got := len(reopened.List("DSX-AAAA-AAAA-AAAA-AAAA")); got != 2 {
		t.Fatalf("persisted entries = %d", got)
	}

	if err := reopened.ForgetKey("DSX-AAAA-AAAA-AAAA-AAAA"); err != nil {
		t.Fatal(err)
	}
	if got := len(reopened.List("DSX-AAAA-AAAA-AAAA-AAAA")); got != 0 {
		t.Fatalf("entries after forget = %d", got)
	}
}

func TestCorruptStoreIsRejected(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, fileName), []byte("{not-json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(dir); err == nil {
		t.Fatal("expected corrupt file error")
	}
}

func TestMissingStoreIsEmpty(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if entries := s.List("anything"); len(entries) != 0 {
		t.Fatalf("entries = %d", len(entries))
	}
}

func TestRecordRequiresFields(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Record("", "http://a.example", time.Now()); err == nil {
		t.Fatal("missing key should fail")
	}
	if err := s.Record("DSX-AAAA-AAAA-AAAA-AAAA", "", time.Now()); err == nil {
		t.Fatal("missing origin should fail")
	}
}
