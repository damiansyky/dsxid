package app

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/wailsapp/wails/v2/pkg/runtime"

	"dsxid/internal/approvals"
	"dsxid/internal/bridge"
	"dsxid/internal/identity"
	"dsxid/internal/profile"
	"dsxid/internal/protocol"
)

// Version is shown in the UI and included in backups' filename.
const Version = "0.1.0"

const approvalTimeout = 2 * time.Minute

// App is the bridge between the Wails window and the local key store.
type App struct {
	ctx context.Context

	mu            sync.Mutex
	dataDir       string
	store         *identity.Store
	meta          *identity.Meta
	id            *identity.Identity
	notice        string
	bridge        *bridge.Server
	bridgeErr     string
	sites         *approvals.Store
	profiles      *profile.Store
	closed        chan struct{}
	closeOnce     sync.Once
	approvals     map[string]*approval
	approvalOrder []string
}

type approval struct {
	ID        string
	Origin    string
	Challenge string
	Profile   []profile.Field
	CreatedAt time.Time
	Decision  chan bool
}

// IdentityView is the public identity data the UI is allowed to see.
type IdentityView struct {
	Label         string `json:"label"`
	ID            string `json:"id"`
	PublicKeyPEM  string `json:"publicKeyPem"`
	PublicKeyFull string `json:"publicKeyFull"`
	CreatedAt     string `json:"createdAt"`
	Algorithm     string `json:"algorithm"`
	KDF           string `json:"kdf"`
	Cipher        string `json:"cipher"`
}

// ApprovalView describes a pending website sign-in request.
type ApprovalView struct {
	ID        string          `json:"id"`
	Origin    string          `json:"origin"`
	Challenge string          `json:"challenge"`
	CreatedAt string          `json:"createdAt"`
	Profile   []profile.Field `json:"profile,omitempty"`
}

// ApprovedAccountView is one website in the local approval history.
type ApprovedAccountView struct {
	Origin          string `json:"origin"`
	Status          string `json:"status"`
	FirstApprovedAt string `json:"firstApprovedAt"`
	LastApprovedAt  string `json:"lastApprovedAt"`
	SignIns         int    `json:"signIns"`
}

// State is a complete snapshot for the frontend.
type State struct {
	HasIdentity      bool                  `json:"hasIdentity"`
	Unlocked         bool                  `json:"unlocked"`
	Identity         *IdentityView         `json:"identity,omitempty"`
	ApprovedAccounts []ApprovedAccountView `json:"approvedAccounts,omitempty"`
	Version          string                `json:"version"`
	DataDir          string                `json:"dataDir"`
	Notice           string                `json:"notice,omitempty"`
}

// NewApp prepares the app data directory and loads public metadata only.
func NewApp() (*App, error) {
	dir := strings.TrimSpace(os.Getenv("DSXID_DATA_DIR"))
	if dir == "" {
		base, err := os.UserConfigDir()
		if err != nil {
			return nil, fmt.Errorf("could not find the configuration directory: %w", err)
		}
		dir = filepath.Join(base, "DsxID")
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("could not create the application data directory: %w", err)
	}
	a := &App{
		dataDir:   dir,
		store:     identity.NewStore(dir),
		profiles:  profile.NewStore(dir),
		closed:    make(chan struct{}),
		approvals: make(map[string]*approval),
	}
	if a.store.Exists() {
		if m, err := a.store.Meta(); err == nil {
			a.meta = m
		} else {
			a.notice = "The existing identity file cannot be read. Try restoring a backup or remove the file from the data folder."
		}
	}
	var err error
	a.sites, err = approvals.Open(dir)
	if err != nil {
		log.Printf("approved accounts: %v", err)
	}
	a.bridge, err = bridge.New(a.signWebsite)
	if err != nil {
		a.bridgeErr = "The website bridge could not be started."
		log.Printf("website bridge: %v", err)
	}
	return a, nil
}

// DataDir is the application data directory used for identity and settings.
func (a *App) DataDir() string {
	return a.dataDir
}

// Startup is called by Wails once the window exists.
func (a *App) Startup(ctx context.Context) {
	a.ctx = ctx
	go setWindowIcon()
	if err := runtime.InitializeNotifications(ctx); err != nil {
		log.Printf("notifications: %v", err)
	}
	if err := startTray(a.ShowWindow, a.lockFromTray, a.quitFromTray); err != nil {
		log.Printf("tray: %v", err)
	}
}

// ShowWindow brings the main window back from the tray.
func (a *App) ShowWindow() {
	if a.ctx == nil {
		return
	}
	runtime.WindowShow(a.ctx)
	runtime.WindowUnminimise(a.ctx)
}

func (a *App) lockFromTray() {
	_ = a.Lock()
	if a.ctx != nil {
		runtime.EventsEmit(a.ctx, "dsxid:state")
	}
}

func (a *App) quitFromTray() {
	if a.ctx != nil {
		runtime.Quit(a.ctx)
	}
}

// Shutdown wipes secrets before the process exits.
func (a *App) Shutdown(ctx context.Context) {
	stopTray()
	a.closeOnce.Do(func() { close(a.closed) })
	if a.bridge != nil {
		_ = a.bridge.Close()
		a.bridge = nil
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.id != nil {
		a.id.Close()
		a.id = nil
	}
}

// GetState returns the current public state. It is safe to call at any time.
func (a *App) GetState() State {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.stateLocked()
}

func (a *App) stateLocked() State {
	st := State{
		Version: Version,
		DataDir: a.dataDir,
		Notice:  a.notice,
	}
	if a.meta == nil && a.store.Exists() {
		if m, err := a.store.Meta(); err == nil {
			a.meta = m
		} else {
			st.Notice = "The identity file cannot be read. Try restoring a backup."
		}
	}
	if a.meta != nil {
		st.HasIdentity = true
		st.Identity = viewOf(a.meta)
	}
	st.Unlocked = a.id != nil
	if a.meta != nil && a.sites != nil {
		entries := a.sites.List(a.meta.ID)
		st.ApprovedAccounts = make([]ApprovedAccountView, 0, len(entries))
		for _, entry := range entries {
			st.ApprovedAccounts = append(st.ApprovedAccounts, ApprovedAccountView{
				Origin:          entry.Origin,
				Status:          "approved",
				FirstApprovedAt: entry.FirstApprovedAt.UTC().Format(time.RFC3339),
				LastApprovedAt:  entry.LastApprovedAt.UTC().Format(time.RFC3339),
				SignIns:         entry.SignIns,
			})
		}
	}
	return st
}

func viewOf(m *identity.Meta) *IdentityView {
	return &IdentityView{
		Label:         m.Label,
		ID:            m.ID,
		PublicKeyPEM:  m.PublicKeyPEM(),
		PublicKeyFull: m.PublicKeyCompact(),
		CreatedAt:     m.CreatedAt.UTC().Format(time.RFC3339),
		Algorithm:     "Ed25519",
		KDF:           "Argon2id",
		Cipher:        "AES-256-GCM",
	}
}

// CreateIdentity generates a fresh key pair and seals it with passphrase.
func (a *App) CreateIdentity(label, passphrase string) (State, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.id != nil {
		return a.stateLocked(), errors.New("Identity is already unlocked.")
	}
	if a.store.Exists() {
		return a.stateLocked(), identity.ErrIdentityExists
	}
	id, err := a.store.Create(label, passphrase)
	if err != nil {
		return a.stateLocked(), err
	}
	a.id = id
	a.meta = &id.Meta
	a.notice = ""
	if a.profiles != nil {
		_ = a.profiles.Delete()
	}
	return a.stateLocked(), nil
}

// Unlock decrypts the local identity with the passphrase.
func (a *App) Unlock(passphrase string) (State, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.id != nil {
		return a.stateLocked(), nil
	}
	if !a.store.Exists() {
		return a.stateLocked(), identity.ErrNoIdentity
	}
	id, err := a.store.Unlock(passphrase)
	if err != nil {
		if errors.Is(err, identity.ErrWrongPassphrase) {
			return a.stateLocked(), errors.New("Incorrect passphrase. Try again.")
		}
		return a.stateLocked(), err
	}
	a.id = id
	a.meta = &id.Meta
	a.notice = ""
	return a.stateLocked(), nil
}

// Lock forgets the private key in memory.
func (a *App) Lock() State {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.id != nil {
		a.id.Close()
		a.id = nil
	}
	return a.stateLocked()
}

// ChangePassphrase reseals the same key with a new passphrase.
func (a *App) ChangePassphrase(oldPassphrase, newPassphrase string) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.id == nil {
		return errors.New("Unlock the identity first.")
	}
	if oldPassphrase == newPassphrase {
		return errors.New("The new passphrase must be different from the current one.")
	}
	if err := a.store.ChangePassphrase(oldPassphrase, newPassphrase); err != nil {
		if errors.Is(err, identity.ErrWrongPassphrase) {
			return errors.New("The current passphrase is not correct.")
		}
		return err
	}
	return nil
}

// DeleteIdentity removes the sealed key from this computer after verifying the
// passphrase.
func (a *App) DeleteIdentity(passphrase string) (State, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if !a.store.Exists() {
		return a.stateLocked(), errors.New("The identity does not exist.")
	}
	check, err := a.store.Unlock(passphrase)
	if err != nil {
		if errors.Is(err, identity.ErrWrongPassphrase) {
			return a.stateLocked(), errors.New("Incorrect passphrase. The identity was not changed.")
		}
		return a.stateLocked(), err
	}
	check.Close()
	keyID := ""
	if a.meta != nil {
		keyID = a.meta.ID
	}
	if err := a.store.Delete(); err != nil {
		return a.stateLocked(), fmt.Errorf("could not remove the identity: %w", err)
	}
	if a.id != nil {
		a.id.Close()
		a.id = nil
	}
	if keyID != "" && a.sites != nil {
		if err := a.sites.ForgetKey(keyID); err != nil {
			log.Printf("approved accounts: %v", err)
		}
	}
	if a.profiles != nil {
		if err := a.profiles.Delete(); err != nil {
			log.Printf("profile: %v", err)
		}
	}
	a.meta = nil
	a.notice = ""
	return a.stateLocked(), nil
}

// CopyToClipboard is used for public keys and fingerprints.
func (a *App) CopyToClipboard(text string) error {
	if a.ctx == nil {
		return errors.New("the application is not ready yet")
	}
	if len(text) == 0 || len(text) > 64*1024 {
		return errors.New("invalid clipboard text")
	}
	return runtime.ClipboardSetText(a.ctx, text)
}

// ExportBackup lets the user save the sealed key file.
func (a *App) ExportBackup() (string, error) {
	a.mu.Lock()
	exists := a.store.Exists()
	a.mu.Unlock()
	if !exists {
		return "", identity.ErrNoIdentity
	}
	name := "dsxid-backup-" + time.Now().Format("2006-01-02") + ".json"
	path, err := runtime.SaveFileDialog(a.ctx, runtime.SaveDialogOptions{
		Title:           "Save encrypted backup",
		DefaultFilename: name,
		Filters: []runtime.FileFilter{
			{DisplayName: "DsxID backup (*.json)", Pattern: "*.json"},
		},
	})
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(path) == "" {
		return "", nil
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if err := a.store.Export(path); err != nil {
		return "", fmt.Errorf("could not save the backup: %w", err)
	}
	return path, nil
}

// RestoreBackup installs a backup when no identity exists on this computer.
func (a *App) RestoreBackup() (State, error) {
	a.mu.Lock()
	exists := a.store.Exists()
	a.mu.Unlock()
	if exists {
		return a.GetState(), errors.New("an identity already exists on this computer")
	}
	path, err := runtime.OpenFileDialog(a.ctx, runtime.OpenDialogOptions{
		Title: "Select a DsxID backup",
		Filters: []runtime.FileFilter{
			{DisplayName: "DsxID backup (*.json)", Pattern: "*.json"},
		},
	})
	if err != nil {
		return a.GetState(), err
	}
	if strings.TrimSpace(path) == "" {
		return a.GetState(), nil
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if err := a.store.Import(path); err != nil {
		if errors.Is(err, identity.ErrCorrupt) {
			return a.stateLocked(), errors.New("The selected file is not a valid DsxID backup.")
		}
		return a.stateLocked(), fmt.Errorf("could not load the backup: %w", err)
	}
	m, err := a.store.Meta()
	if err != nil {
		return a.stateLocked(), err
	}
	a.meta = m
	a.notice = ""
	if a.profiles != nil {
		_ = a.profiles.Delete()
	}
	return a.stateLocked(), nil
}

// GetProfile returns the encrypted e-shop profile for the unlocked identity.
// The profile is readable only while the identity is unlocked.
func (a *App) GetProfile() (map[string]string, error) {
	a.mu.Lock()
	id := a.id
	profiles := a.profiles
	a.mu.Unlock()
	if id == nil {
		return nil, errors.New("Unlock the identity first.")
	}
	if profiles == nil {
		return map[string]string{}, nil
	}
	return profiles.Load(id.PrivateKey)
}

// SaveProfile encrypts and saves the e-shop profile for the unlocked identity.
func (a *App) SaveProfile(values map[string]string) error {
	a.mu.Lock()
	id := a.id
	profiles := a.profiles
	a.mu.Unlock()
	if id == nil {
		return errors.New("Unlock the identity first.")
	}
	if profiles == nil {
		return errors.New("Profile storage is not available.")
	}
	return profiles.Save(id.PrivateKey, values)
}

// signWebsite is called by the localhost bridge for every website request. It
// blocks until the user approves or denies the request in the desktop app.
func (a *App) signWebsite(origin, challenge string, requested []string) (bridge.Response, error) {
	a.mu.Lock()
	if a.id == nil {
		locked := a.store.Exists()
		a.mu.Unlock()
		if locked {
			return bridge.Response{}, bridge.ErrLocked
		}
		return bridge.Response{}, bridge.ErrNoIdentity
	}
	if len(a.approvals) >= 8 {
		a.mu.Unlock()
		return bridge.Response{}, bridge.ErrBusy
	}
	var profileFields []profile.Field
	if len(requested) > 0 && a.profiles != nil {
		values, err := a.profiles.Load(a.id.PrivateKey)
		if err != nil {
			log.Printf("profile: %v", err)
		}
		profileFields = profile.Requested(values, requested)
	}
	requestID, err := randomID()
	if err != nil {
		a.mu.Unlock()
		return bridge.Response{}, err
	}
	request := &approval{
		ID:        requestID,
		Origin:    origin,
		Challenge: challenge,
		Profile:   profileFields,
		CreatedAt: time.Now().UTC(),
		Decision:  make(chan bool, 1),
	}
	a.approvals[requestID] = request
	a.approvalOrder = append(a.approvalOrder, requestID)
	ctx := a.ctx
	a.mu.Unlock()

	if ctx != nil {
		if err := runtime.SendNotification(ctx, runtime.NotificationOptions{
			ID:    "approval-" + requestID,
			Title: "DsxID sign-in request",
			Body:  fmt.Sprintf("%s wants to sign in. Open DsxID to approve or deny.", origin),
		}); err != nil {
			log.Printf("notification: %v", err)
		}
		runtime.WindowShow(ctx)
		runtime.WindowUnminimise(ctx)
	}

	timer := time.NewTimer(approvalTimeout)
	defer timer.Stop()
	select {
	case approved := <-request.Decision:
		if !approved {
			return bridge.Response{}, bridge.ErrDenied
		}
	case <-timer.C:
		a.removeApproval(requestID)
		return bridge.Response{}, bridge.ErrTimeout
	case <-a.closed:
		a.removeApproval(requestID)
		return bridge.Response{}, bridge.ErrLocked
	}

	a.mu.Lock()
	defer a.mu.Unlock()
	if a.id == nil {
		return bridge.Response{}, bridge.ErrLocked
	}
	assertion, _, err := protocol.SignWithProfile(a.id.PrivateKey, a.id.ID, origin, challenge, profile.Values(request.Profile), time.Now(), 60*time.Second)
	if err != nil {
		return bridge.Response{}, err
	}
	if a.sites != nil {
		if err := a.sites.Record(a.id.ID, origin, time.Now()); err != nil {
			log.Printf("approved accounts: %v", err)
		}
	}
	return bridge.Response{
		Assertion:    assertion,
		PublicKeyPEM: a.id.PublicKeyPEM(),
		KeyID:        a.id.ID,
		Label:        a.id.Label,
	}, nil
}

// GetPendingApproval returns the oldest website request awaiting consent.
func (a *App) GetPendingApproval() *ApprovalView {
	a.mu.Lock()
	defer a.mu.Unlock()
	for _, id := range a.approvalOrder {
		request, ok := a.approvals[id]
		if !ok {
			continue
		}
		return &ApprovalView{
			ID:        request.ID,
			Origin:    request.Origin,
			Challenge: request.Challenge,
			CreatedAt: request.CreatedAt.Format(time.RFC3339),
			Profile:   request.Profile,
		}
	}
	return nil
}

// ResolveApproval records the user's decision for a pending website request.
func (a *App) ResolveApproval(id string, approve bool) error {
	a.mu.Lock()
	request, ok := a.approvals[id]
	if !ok {
		a.mu.Unlock()
		return errors.New("The sign-in request is no longer available.")
	}
	delete(a.approvals, id)
	for i, item := range a.approvalOrder {
		if item == id {
			a.approvalOrder = append(a.approvalOrder[:i], a.approvalOrder[i+1:]...)
			break
		}
	}
	a.mu.Unlock()

	select {
	case request.Decision <- approve:
	default:
	}
	return nil
}

func (a *App) removeApproval(id string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	delete(a.approvals, id)
	for i, item := range a.approvalOrder {
		if item == id {
			a.approvalOrder = append(a.approvalOrder[:i], a.approvalOrder[i+1:]...)
			break
		}
	}
}

func randomID() (string, error) {
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf), nil
}
