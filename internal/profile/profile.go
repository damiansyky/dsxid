// Package profile stores optional e-shop contact and delivery details. The
// profile is sealed with a key derived from the unlocked Ed25519 private key,
// so it can only be read while the identity is unlocked and never leaves the
// computer unless the user approves a website request.
package profile

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const (
	fileName  = "profile.json"
	version   = 1
	maxFields = 12
)

var b64 = base64.RawURLEncoding

// Fields maps stable API keys to human labels. The order is used by the UI.
var Fields = map[string]string{
	"fullName":   "Full name",
	"email":      "Email",
	"phone":      "Phone",
	"address1":   "Address line 1",
	"address2":   "Address line 2",
	"city":       "City",
	"postalCode": "Postal code",
	"country":    "Country",
	"company":    "Company",
	"vatId":      "VAT ID",
}

var fieldLimits = map[string]int{
	"fullName":   120,
	"email":      254,
	"phone":      40,
	"address1":   160,
	"address2":   160,
	"city":       80,
	"postalCode": 24,
	"country":    80,
	"company":    120,
	"vatId":      40,
}

// Field is one profile value requested by a website.
type Field struct {
	Key       string `json:"key"`
	Label     string `json:"label"`
	Value     string `json:"value"`
	Available bool   `json:"available"`
}

// Store owns the encrypted profile file in the application data directory.
type Store struct {
	path string
}

// NewStore returns a profile store rooted in dir.
func NewStore(dir string) *Store {
	return &Store{path: filepath.Join(dir, fileName)}
}

// Path is the absolute profile file path.
func (s *Store) Path() string { return s.path }

// Exists reports whether a profile file exists.
func (s *Store) Exists() bool {
	st, err := os.Stat(s.path)
	return err == nil && !st.IsDir()
}

// Load decrypts the profile with a key derived from the unlocked private key.
// A missing profile is an empty profile, not an error.
func (s *Store) Load(priv ed25519.PrivateKey) (map[string]string, error) {
	empty := map[string]string{}
	if len(priv) != ed25519.PrivateKeySize {
		return empty, errors.New("invalid private key")
	}
	raw, err := os.ReadFile(s.path)
	if errors.Is(err, os.ErrNotExist) {
		return empty, nil
	}
	if err != nil {
		return empty, err
	}
	var f sealedFile
	if err := json.Unmarshal(raw, &f); err != nil || f.Version != version {
		return empty, errors.New("the profile file is corrupted")
	}
	nonce, err := b64.DecodeString(f.Nonce)
	if err != nil || len(nonce) != 12 {
		return empty, errors.New("the profile file is corrupted")
	}
	data, err := b64.DecodeString(f.Data)
	if err != nil {
		return empty, errors.New("the profile file is corrupted")
	}
	key := keyFor(priv)
	defer wipe(key)
	aead, err := newAEAD(key)
	if err != nil {
		return empty, err
	}
	plain, err := aead.Open(nil, nonce, data, []byte("dsxid/profile/v1"))
	if err != nil {
		return empty, errors.New("the profile could not be decrypted")
	}
	var values map[string]string
	if err := json.Unmarshal(plain, &values); err != nil {
		return empty, errors.New("the profile file is corrupted")
	}
	return normalize(values)
}

// Save encrypts and writes the profile. Empty fields are removed.
func (s *Store) Save(priv ed25519.PrivateKey, values map[string]string) error {
	if len(priv) != ed25519.PrivateKeySize {
		return errors.New("invalid private key")
	}
	values, err := normalize(values)
	if err != nil {
		return err
	}
	plain, err := json.Marshal(values)
	if err != nil {
		return err
	}
	key := keyFor(priv)
	defer wipe(key)
	aead, err := newAEAD(key)
	if err != nil {
		return err
	}
	nonce := make([]byte, 12)
	if _, err := rand.Read(nonce); err != nil {
		return err
	}
	data := aead.Seal(nil, nonce, plain, []byte("dsxid/profile/v1"))
	f := sealedFile{
		Version: version,
		Nonce:   b64.EncodeToString(nonce),
		Data:    b64.EncodeToString(data),
	}
	raw, err := json.MarshalIndent(f, "", "  ")
	if err != nil {
		return err
	}
	return writeFile(s.path, raw)
}

// Delete removes the encrypted profile file.
func (s *Store) Delete() error {
	if err := os.Remove(s.path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

// Requested builds the fields a website asked for. Unknown keys are ignored.
func Requested(values map[string]string, keys []string) []Field {
	if len(keys) == 0 {
		return nil
	}
	seen := make(map[string]bool, len(keys))
	fields := make([]Field, 0, len(keys))
	for _, key := range keys {
		key = strings.TrimSpace(key)
		if seen[key] || Fields[key] == "" {
			continue
		}
		seen[key] = true
		value := strings.TrimSpace(values[key])
		fields = append(fields, Field{
			Key:       key,
			Label:     Fields[key],
			Value:     value,
			Available: value != "",
		})
		if len(fields) == maxFields {
			break
		}
	}
	return fields
}

// Values returns only the available fields, ready for signing.
func Values(fields []Field) map[string]string {
	out := make(map[string]string, len(fields))
	for _, field := range fields {
		if field.Available && field.Value != "" {
			out[field.Key] = field.Value
		}
	}
	return out
}

type sealedFile struct {
	Version int    `json:"version"`
	Nonce   string `json:"nonce"`
	Data    string `json:"data"`
}

func normalize(values map[string]string) (map[string]string, error) {
	out := make(map[string]string, len(values))
	for key, value := range values {
		limit, known := fieldLimits[key]
		if !known {
			return nil, fmt.Errorf("unknown profile field %q", key)
		}
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if len(value) > limit {
			return nil, fmt.Errorf("profile field %q is too long", key)
		}
		if strings.ContainsAny(value, "\x00\r\n") {
			return nil, fmt.Errorf("profile field %q contains invalid characters", key)
		}
		out[key] = value
	}
	return out, nil
}

func keyFor(priv ed25519.PrivateKey) []byte {
	sum := sha256.New()
	sum.Write([]byte("dsxid/profile/v1"))
	sum.Write(priv.Seed())
	return sum.Sum(nil)
}

func newAEAD(key []byte) (cipher.AEAD, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}

func wipe(buf []byte) {
	for i := range buf {
		buf[i] = 0
	}
}

func writeFile(path string, data []byte) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".profile-*.tmp")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
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
	return os.Rename(tmp.Name(), path)
}
