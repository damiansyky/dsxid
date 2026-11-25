// Package identity manages the local DsxID key: an Ed25519 key pair whose
// private half is sealed on disk with a passphrase.
package identity

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base32"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"golang.org/x/crypto/argon2"
)

const (
	// FileVersion is the on-disk format version.
	FileVersion = 1
	// MinPassphraseLen is the minimum accepted passphrase length, in runes.
	MinPassphraseLen = 8
	// MaxLabelLen is the maximum label length, in runes.
	MaxLabelLen = 48

	fileName = "identity.json"

	argonTime    = 3
	argonMemory  = 64 * 1024 // KiB
	argonThreads = 2
	keyLength    = 32
)

var (
	// ErrNoIdentity means there is no identity file on this computer.
	ErrNoIdentity = errors.New("identity does not exist")
	// ErrIdentityExists means an identity file is already present.
	ErrIdentityExists = errors.New("an identity already exists")
	// ErrWrongPassphrase is returned when decryption fails.
	ErrWrongPassphrase = errors.New("incorrect passphrase")
	// ErrPassphraseTooShort is returned for weak input.
	ErrPassphraseTooShort = fmt.Errorf("passphrase must be at least %d characters", MinPassphraseLen)
	// ErrCorrupt means the identity file cannot be parsed or validated.
	ErrCorrupt = errors.New("identity file is corrupted")
)

var b64 = base64.RawURLEncoding

// Meta is the public, unsealed part of an identity.
type Meta struct {
	ID        string
	Label     string
	PublicKey ed25519.PublicKey
	CreatedAt time.Time
}

// Identity is an unlocked key pair. Call Close when it is no longer needed.
type Identity struct {
	Meta
	PrivateKey ed25519.PrivateKey
}

// Close wipes the private key from this process's memory.
func (i *Identity) Close() {
	if i == nil {
		return
	}
	for n := range i.PrivateKey {
		i.PrivateKey[n] = 0
	}
	i.PrivateKey = nil
}

// PublicKeyPEM returns the public key in the standard PEM format, ready to be
// pasted into a service that supports DsxID keys.
func (m Meta) PublicKeyPEM() string {
	der, err := x509.MarshalPKIXPublicKey(m.PublicKey)
	if err != nil {
		return ""
	}
	return string(pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: der}))
}

// PublicKeyCompact returns a short, copy-friendly representation.
func (m Meta) PublicKeyCompact() string {
	return "ed25519:" + b64.EncodeToString(m.PublicKey)
}

// DeriveID builds a human-readable fingerprint from a public key.
// It is an identifier, not a secret. 80 bits of a SHA-256 hash are plenty for
// display and lookup; signatures are always checked against the full key.
func DeriveID(pub ed25519.PublicKey) string {
	sum := sha256.Sum256(pub)
	enc := base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(sum[:10])
	return "DSX-" + enc[0:4] + "-" + enc[4:8] + "-" + enc[8:12] + "-" + enc[12:16]
}

// ValidID reports whether id has the shape produced by DeriveID.
func ValidID(id string) bool {
	parts := strings.Split(id, "-")
	if len(parts) != 5 || parts[0] != "DSX" {
		return false
	}
	for _, p := range parts[1:] {
		if len(p) != 4 {
			return false
		}
		for _, r := range p {
			if !(r >= 'A' && r <= 'Z') && !(r >= '2' && r <= '7') {
				return false
			}
		}
	}
	return true
}

type kdfParams struct {
	Name      string `json:"name"`
	Salt      string `json:"salt"`
	Time      uint32 `json:"time"`
	Memory    uint32 `json:"memory"`
	Threads   uint8  `json:"threads"`
	KeyLength uint32 `json:"key_length"`
}

type cipherParams struct {
	Name  string `json:"name"`
	Nonce string `json:"nonce"`
	Data  string `json:"data"`
}

type sealedFile struct {
	Version   int          `json:"version"`
	ID        string       `json:"id"`
	Label     string       `json:"label"`
	PublicKey string       `json:"public_key"`
	CreatedAt time.Time    `json:"created_at"`
	KDF       kdfParams    `json:"kdf"`
	Cipher    cipherParams `json:"cipher"`
}

// Store owns the identity file inside one application data directory.
type Store struct {
	dir  string
	path string
}

// NewStore returns a store rooted at dir. It does not create anything yet.
func NewStore(dir string) *Store {
	return &Store{dir: dir, path: filepath.Join(dir, fileName)}
}

// Path is the absolute path of the identity file.
func (s *Store) Path() string { return s.path }

// Exists reports whether an identity file is present.
func (s *Store) Exists() bool {
	st, err := os.Stat(s.path)
	return err == nil && !st.IsDir()
}

func (s *Store) read() (*sealedFile, error) {
	raw, err := os.ReadFile(s.path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, ErrNoIdentity
		}
		return nil, err
	}
	var f sealedFile
	if err := json.Unmarshal(raw, &f); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrCorrupt, err)
	}
	if err := validateSealed(&f); err != nil {
		return nil, err
	}
	return &f, nil
}

func validateSealed(f *sealedFile) error {
	if f.Version != FileVersion {
		return fmt.Errorf("%w: unsupported version %d", ErrCorrupt, f.Version)
	}
	pub, err := b64.DecodeString(f.PublicKey)
	if err != nil || len(pub) != ed25519.PublicKeySize {
		return fmt.Errorf("%w: invalid public key", ErrCorrupt)
	}
	if DeriveID(pub) != f.ID {
		return fmt.Errorf("%w: fingerprint does not match the public key", ErrCorrupt)
	}
	if f.KDF.Name != "argon2id" || f.KDF.KeyLength != keyLength {
		return fmt.Errorf("%w: unknown KDF", ErrCorrupt)
	}
	if f.KDF.Time == 0 || f.KDF.Time > 10 ||
		f.KDF.Memory < 8*1024 || f.KDF.Memory > 256*1024 ||
		f.KDF.Threads == 0 || f.KDF.Threads > 16 {
		return fmt.Errorf("%w: invalid KDF parameters", ErrCorrupt)
	}
	if salt, err := b64.DecodeString(f.KDF.Salt); err != nil || len(salt) < 16 {
		return fmt.Errorf("%w: invalid salt", ErrCorrupt)
	}
	if f.Cipher.Name != "aes-256-gcm" {
		return fmt.Errorf("%w: unknown cipher", ErrCorrupt)
	}
	if nonce, err := b64.DecodeString(f.Cipher.Nonce); err != nil || len(nonce) != 12 {
		return fmt.Errorf("%w: invalid nonce", ErrCorrupt)
	}
	if data, err := b64.DecodeString(f.Cipher.Data); err != nil || len(data) != ed25519.PrivateKeySize+16 {
		return fmt.Errorf("%w: invalid data", ErrCorrupt)
	}
	if f.CreatedAt.IsZero() {
		return fmt.Errorf("%w: missing creation time", ErrCorrupt)
	}
	return nil
}

// Meta loads and validates the public part of the identity without a
// passphrase. It never decrypts the private key.
func (s *Store) Meta() (*Meta, error) {
	f, err := s.read()
	if err != nil {
		return nil, err
	}
	pub, _ := b64.DecodeString(f.PublicKey)
	return &Meta{
		ID:        f.ID,
		Label:     f.Label,
		PublicKey: ed25519.PublicKey(pub),
		CreatedAt: f.CreatedAt.UTC(),
	}, nil
}

// Create generates a new key pair and writes it sealed with passphrase.
func (s *Store) Create(label, passphrase string) (*Identity, error) {
	if s.Exists() {
		return nil, ErrIdentityExists
	}
	if err := checkPassphrase(passphrase); err != nil {
		return nil, err
	}
	label, err := normalizeLabel(label)
	if err != nil {
		return nil, err
	}
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("failed to generate key: %w", err)
	}
	f, err := seal(priv, label, passphrase)
	if err != nil {
		for n := range priv {
			priv[n] = 0
		}
		return nil, err
	}
	if err := s.write(f); err != nil {
		for n := range priv {
			priv[n] = 0
		}
		return nil, err
	}
	return &Identity{
		Meta: Meta{
			ID:        f.ID,
			Label:     label,
			PublicKey: pub,
			CreatedAt: f.CreatedAt.UTC(),
		},
		PrivateKey: priv,
	}, nil
}

// Unlock decrypts the private key with passphrase.
func (s *Store) Unlock(passphrase string) (*Identity, error) {
	f, err := s.read()
	if err != nil {
		return nil, err
	}
	priv, err := open(f, passphrase)
	if err != nil {
		return nil, err
	}
	pub, _ := b64.DecodeString(f.PublicKey)
	return &Identity{
		Meta: Meta{
			ID:        f.ID,
			Label:     f.Label,
			PublicKey: ed25519.PublicKey(pub),
			CreatedAt: f.CreatedAt.UTC(),
		},
		PrivateKey: priv,
	}, nil
}

// ChangePassphrase verifies oldPassphrase and reseals the same key with
// newPassphrase using a fresh salt and nonce.
func (s *Store) ChangePassphrase(oldPassphrase, newPassphrase string) error {
	if err := checkPassphrase(newPassphrase); err != nil {
		return err
	}
	f, err := s.read()
	if err != nil {
		return err
	}
	priv, err := open(f, oldPassphrase)
	if err != nil {
		return err
	}
	defer func() {
		for n := range priv {
			priv[n] = 0
		}
	}()

	next, err := seal(priv, f.Label, newPassphrase)
	if err != nil {
		return err
	}
	next.CreatedAt = f.CreatedAt
	return s.write(next)
}

// Delete removes the sealed identity from disk.
func (s *Store) Delete() error {
	err := os.Remove(s.path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}

// Export copies the sealed identity file to dst.
func (s *Store) Export(dst string) error {
	raw, err := os.ReadFile(s.path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return ErrNoIdentity
		}
		return err
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(dst), ".dsxid-export-*.tmp")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(raw); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), dst)
}

// Import validates a backup and installs it as the identity file. It refuses
// to overwrite an existing identity.
func (s *Store) Import(src string) error {
	if s.Exists() {
		return ErrIdentityExists
	}
	raw, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	var f sealedFile
	if err := json.Unmarshal(raw, &f); err != nil {
		return fmt.Errorf("%w: %v", ErrCorrupt, err)
	}
	if err := validateSealed(&f); err != nil {
		return err
	}
	return s.write(&f)
}

func (s *Store) write(f *sealedFile) error {
	if err := os.MkdirAll(s.dir, 0o700); err != nil {
		return err
	}
	raw, err := json.MarshalIndent(f, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(s.dir, ".identity-*.tmp")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(raw); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), s.path)
}

func seal(priv ed25519.PrivateKey, label, passphrase string) (*sealedFile, error) {
	if len(priv) != ed25519.PrivateKeySize {
		return nil, errors.New("invalid private key")
	}
	pub := priv.Public().(ed25519.PublicKey)
	salt := make([]byte, 16)
	nonce := make([]byte, 12)
	if _, err := rand.Read(salt); err != nil {
		return nil, err
	}
	if _, err := rand.Read(nonce); err != nil {
		return nil, err
	}
	key := deriveKey(passphrase, salt, argonTime, argonMemory, argonThreads)
	defer wipe(key)

	aead, err := newAEAD(key)
	if err != nil {
		return nil, err
	}
	f := &sealedFile{
		Version:   FileVersion,
		ID:        DeriveID(pub),
		Label:     label,
		PublicKey: b64.EncodeToString(pub),
		CreatedAt: time.Now().UTC(),
		KDF: kdfParams{
			Name:      "argon2id",
			Salt:      b64.EncodeToString(salt),
			Time:      argonTime,
			Memory:    argonMemory,
			Threads:   argonThreads,
			KeyLength: keyLength,
		},
		Cipher: cipherParams{
			Name:  "aes-256-gcm",
			Nonce: b64.EncodeToString(nonce),
		},
	}
	f.Cipher.Data = b64.EncodeToString(aead.Seal(nil, nonce, priv, aad(f)))
	return f, nil
}

func open(f *sealedFile, passphrase string) (ed25519.PrivateKey, error) {
	salt, err := b64.DecodeString(f.KDF.Salt)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrCorrupt, err)
	}
	nonce, err := b64.DecodeString(f.Cipher.Nonce)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrCorrupt, err)
	}
	data, err := b64.DecodeString(f.Cipher.Data)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrCorrupt, err)
	}
	key := deriveKey(passphrase, salt, f.KDF.Time, f.KDF.Memory, f.KDF.Threads)
	defer wipe(key)
	aead, err := newAEAD(key)
	if err != nil {
		return nil, err
	}
	plain, err := aead.Open(nil, nonce, data, aad(f))
	if err != nil {
		return nil, ErrWrongPassphrase
	}
	if len(plain) != ed25519.PrivateKeySize {
		wipe(plain)
		return nil, ErrCorrupt
	}
	return ed25519.PrivateKey(plain), nil
}

func deriveKey(passphrase string, salt []byte, t, m uint32, threads uint8) []byte {
	return argon2.IDKey([]byte(passphrase), salt, t, m, threads, keyLength)
}

func newAEAD(key []byte) (cipher.AEAD, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}

func aad(f *sealedFile) []byte {
	return []byte("dsxid/identity/v1|" + f.ID + "|" + f.PublicKey)
}

func checkPassphrase(p string) error {
	if utf8.RuneCountInString(p) < MinPassphraseLen {
		return ErrPassphraseTooShort
	}
	return nil
}

func normalizeLabel(label string) (string, error) {
	label = strings.TrimSpace(label)
	if label == "" {
		label = "My identity"
	}
	if utf8.RuneCountInString(label) > MaxLabelLen {
		return "", fmt.Errorf("name may be at most %d characters", MaxLabelLen)
	}
	for _, r := range label {
		if unicode.IsControl(r) {
			return "", errors.New("name contains unsupported characters")
		}
	}
	return label, nil
}

func wipe(b []byte) {
	for n := range b {
		b[n] = 0
	}
}
