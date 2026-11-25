package app

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"dsxid/internal/approvals"
	"dsxid/internal/bridge"
	"dsxid/internal/identity"
	"dsxid/internal/profile"
	"dsxid/internal/protocol"
)

const testPass = "test passphrase 123"

func newTestApp(t *testing.T) *App {
	t.Helper()
	dir := t.TempDir()
	sites, err := approvals.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	a := &App{
		dataDir:   dir,
		store:     identity.NewStore(dir),
		sites:     sites,
		profiles:  profile.NewStore(dir),
		closed:    make(chan struct{}),
		approvals: make(map[string]*approval),
	}
	t.Cleanup(func() { a.Shutdown(context.Background()) })
	return a
}

func TestAppLifecycle(t *testing.T) {
	a := newTestApp(t)
	st := a.GetState()
	if st.HasIdentity || st.Unlocked || st.Identity != nil {
		t.Fatalf("unexpected initial state: %+v", st)
	}
	if st.Version != Version {
		t.Fatalf("version = %q", st.Version)
	}

	st, err := a.CreateIdentity("  Alex  ", testPass)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if !st.HasIdentity || !st.Unlocked {
		t.Fatalf("state should be unlocked: %+v", st)
	}
	if st.Identity == nil || st.Identity.Label != "Alex" || st.Identity.ID == "" {
		t.Fatalf("bad identity: %+v", st.Identity)
	}
	if !strings.Contains(st.Identity.PublicKeyPEM, "BEGIN PUBLIC KEY") {
		t.Fatal("missing public key pem")
	}

	if _, err := a.CreateIdentity("Second", testPass); err == nil {
		t.Fatal("second identity should be rejected")
	}

	// Wrong passphrase must not unlock.
	locked := a.Lock()
	if !locked.HasIdentity || locked.Unlocked {
		t.Fatalf("lock failed: %+v", locked)
	}
	if _, err := a.Unlock("not the passphrase"); err == nil {
		t.Fatal("wrong passphrase should fail")
	}
	st, err = a.Unlock(testPass)
	if err != nil {
		t.Fatalf("unlock: %v", err)
	}
	if !st.Unlocked {
		t.Fatal("not unlocked")
	}

	oldID := st.Identity.ID
	if err := a.ChangePassphrase(testPass, "new passphrase 456"); err != nil {
		t.Fatalf("change passphrase: %v", err)
	}
	if got := a.stateLocked().Identity.ID; got != oldID {
		t.Fatalf("identity changed: %s -> %s", oldID, got)
	}
	a.Lock()
	if _, err := a.Unlock(testPass); err == nil {
		t.Fatal("old passphrase still works")
	}
	if _, err := a.Unlock("new passphrase 456"); err != nil {
		t.Fatalf("new passphrase failed: %v", err)
	}

	// Delete requires the passphrase and removes the local identity.
	if _, err := a.DeleteIdentity("wrong pass"); err == nil {
		t.Fatal("delete with wrong passphrase should fail")
	}
	st, err = a.DeleteIdentity("new passphrase 456")
	if err != nil {
		t.Fatalf("delete: %v", err)
	}
	if st.HasIdentity || st.Unlocked || st.Identity != nil {
		t.Fatalf("state after delete = %+v", st)
	}
	if _, err := os.Stat(filepath.Join(a.dataDir, "identity.json")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("identity file still exists: %v", err)
	}
}

func TestAppRejectsSecondIdentityWhenLocked(t *testing.T) {
	a := newTestApp(t)
	id, err := a.CreateIdentity("Alex", testPass)
	if err != nil {
		t.Fatal(err)
	}
	if id.Identity == nil {
		t.Fatal("missing identity")
	}
	a.Lock()
	if _, err := a.CreateIdentity("Second", testPass); err == nil {
		t.Fatal("second identity should be rejected when locked")
	}
}

func TestAppStateHasNoPrivateMaterial(t *testing.T) {
	a := newTestApp(t)
	st, err := a.CreateIdentity("Alex", testPass)
	if err != nil {
		t.Fatal(err)
	}
	if st.Identity == nil {
		t.Fatal("missing identity view")
	}
	if !strings.Contains(st.Identity.PublicKeyPEM, "PUBLIC KEY") || strings.Contains(st.Identity.PublicKeyPEM, "PRIVATE") {
		t.Fatal("view leaked a private key")
	}
	if st.Identity.ID == "" || !strings.HasPrefix(st.Identity.ID, "DSX-") {
		t.Fatalf("bad id: %q", st.Identity.ID)
	}
}

func TestWebsiteApprovalSignsChallenge(t *testing.T) {
	a := newTestApp(t)
	if _, err := a.CreateIdentity("Alex", testPass); err != nil {
		t.Fatal(err)
	}
	challenge := "abcdefghijklmnopqrstuvwxyz123456"
	origin := "http://localhost:3000"

	type result struct {
		response bridge.Response
		err      error
	}
	resultCh := make(chan result, 1)
	go func() {
		response, err := a.signWebsite(origin, challenge, nil)
		resultCh <- result{response: response, err: err}
	}()

	request := waitForApproval(t, a)
	if request.Origin != origin || request.Challenge != challenge {
		t.Fatalf("unexpected approval request: %+v", request)
	}
	if err := a.ResolveApproval(request.ID, true); err != nil {
		t.Fatalf("resolve approval: %v", err)
	}
	res := <-resultCh
	if res.err != nil {
		t.Fatalf("signWebsite: %v", res.err)
	}
	if res.response.KeyID == "" || !strings.Contains(res.response.PublicKeyPEM, "BEGIN PUBLIC KEY") {
		t.Fatalf("bad response: %+v", res.response)
	}
	pub := a.id.PublicKey
	if _, err := protocol.Verify(res.response.Assertion, pub, origin, challenge, time.Now()); err != nil {
		t.Fatalf("assertion does not verify: %v", err)
	}
	state := a.GetState()
	if len(state.ApprovedAccounts) != 1 {
		t.Fatalf("approved accounts = %d, want 1", len(state.ApprovedAccounts))
	}
	if state.ApprovedAccounts[0].Origin != origin || state.ApprovedAccounts[0].SignIns != 1 || state.ApprovedAccounts[0].Status != "approved" {
		t.Fatalf("unexpected approved account: %+v", state.ApprovedAccounts[0])
	}
}

func TestWebsiteApprovalSharesProfile(t *testing.T) {
	a := newTestApp(t)
	if _, err := a.CreateIdentity("Alex", testPass); err != nil {
		t.Fatal(err)
	}
	if err := a.SaveProfile(map[string]string{
		"fullName": "Alex Example",
		"email":    "alex@example.com",
		"address1": "Main street 1",
		"city":     "Bratislava",
	}); err != nil {
		t.Fatal(err)
	}
	challenge := "abcdefghijklmnopqrstuvwxyz123456"
	origin := "http://localhost:3000"

	type result struct {
		response bridge.Response
		err      error
	}
	resultCh := make(chan result, 1)
	go func() {
		response, err := a.signWebsite(origin, challenge, []string{"fullName", "email", "phone"})
		resultCh <- result{response: response, err: err}
	}()

	request := waitForApproval(t, a)
	if len(request.Profile) != 3 {
		t.Fatalf("profile fields = %+v", request.Profile)
	}
	if request.Profile[0].Value != "Alex Example" || !request.Profile[0].Available {
		t.Fatalf("unexpected full name field: %+v", request.Profile[0])
	}
	if request.Profile[2].Available {
		t.Fatalf("missing phone should not be available: %+v", request.Profile[2])
	}
	if err := a.ResolveApproval(request.ID, true); err != nil {
		t.Fatal(err)
	}
	res := <-resultCh
	if res.err != nil {
		t.Fatal(res.err)
	}
	statement, err := protocol.Verify(res.response.Assertion, a.id.PublicKey, origin, challenge, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if statement.Profile["fullName"] != "Alex Example" || statement.Profile["email"] != "alex@example.com" {
		t.Fatalf("signed profile = %+v", statement.Profile)
	}
	if _, exists := statement.Profile["phone"]; exists {
		t.Fatal("empty profile fields must not be signed")
	}
}

func TestWebsiteApprovalCanBeDenied(t *testing.T) {
	a := newTestApp(t)
	if _, err := a.CreateIdentity("Alex", testPass); err != nil {
		t.Fatal(err)
	}
	challenge := "abcdefghijklmnopqrstuvwxyz123456"
	resultCh := make(chan error, 1)
	go func() {
		_, err := a.signWebsite("http://localhost:3000", challenge, nil)
		resultCh <- err
	}()
	request := waitForApproval(t, a)
	if err := a.ResolveApproval(request.ID, false); err != nil {
		t.Fatalf("resolve approval: %v", err)
	}
	if err := <-resultCh; !errors.Is(err, bridge.ErrDenied) {
		t.Fatalf("want denied, got %v", err)
	}
}

func TestWebsiteSigningRequiresUnlockedIdentity(t *testing.T) {
	a := newTestApp(t)
	if _, err := a.signWebsite("http://localhost:3000", "abcdefghijklmnopqrstuvwxyz123456", nil); !errors.Is(err, bridge.ErrNoIdentity) {
		t.Fatalf("want no identity, got %v", err)
	}
	if _, err := a.CreateIdentity("Alex", testPass); err != nil {
		t.Fatal(err)
	}
	a.Lock()
	if _, err := a.signWebsite("http://localhost:3000", "abcdefghijklmnopqrstuvwxyz123456", nil); !errors.Is(err, bridge.ErrLocked) {
		t.Fatalf("want locked, got %v", err)
	}
}

func waitForApproval(t *testing.T, a *App) *ApprovalView {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if request := a.GetPendingApproval(); request != nil {
			return request
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("approval request did not arrive")
	return nil
}
