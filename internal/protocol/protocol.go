// Package protocol implements the DsxID sign-in assertion: a short-lived
// statement signed with the user's Ed25519 key.
//
// Compact form:
//
//	dsx1.<base64url(statement JSON)>.<base64url(signature)>
//
// The signature covers "dsxid-auth-v1." + the encoded statement. A verifier
// must also check the audience, the single-use challenge (nonce), and the
// validity window. Those checks live here for the audience/nonce/window part,
// while nonce issuance and replay protection belong to the service.
package protocol

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"dsxid/internal/identity"
)

const (
	// Prefix identifies the assertion format.
	Prefix = "dsx1"
	// Domain separates these signatures from other uses of the same key.
	Domain = "dsxid-auth-v1"
	// KindAuth is the only assertion kind currently defined.
	KindAuth = "auth"

	// MaxTTL bounds how long a signed statement may remain valid.
	MaxTTL = 2 * time.Minute
	// MaxClockSkew tolerates a verifier clock that is slightly behind.
	MaxClockSkew = 30 * time.Second
	// MaxPayloadBytes avoids parsing absurd inputs.
	MaxPayloadBytes = 4 * 1024
	// MaxProfileFields bounds the optional e-shop profile attached to a request.
	MaxProfileFields = 10
	// MaxProfileValueBytes bounds each profile value.
	MaxProfileValueBytes = 512
)

var (
	ErrMalformed     = errors.New("invalid signature format")
	ErrWrongKind     = errors.New("invalid signature type")
	ErrWrongAudience = errors.New("signature is for a different service")
	ErrWrongKey      = errors.New("signature does not belong to this key")
	ErrWrongNonce    = errors.New("challenge does not match")
	ErrExpired       = errors.New("signature has expired")
	ErrNotYetValid   = errors.New("signature is not valid yet")
	ErrBadSignature  = errors.New("signature could not be verified")
)

// Statement is the data a user's key signs.
type Statement struct {
	Version   int               `json:"v"`
	Kind      string            `json:"typ"`
	KeyID     string            `json:"kid"`
	Audience  string            `json:"aud"`
	Nonce     string            `json:"nonce"`
	IssuedAt  int64             `json:"iat"`
	ExpiresAt int64             `json:"exp"`
	Profile   map[string]string `json:"profile,omitempty"`
}

// NewChallenge returns a fresh, unguessable sign-in nonce.
func NewChallenge() (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}

// Sign creates a compact assertion for audience and nonce without a profile.
func Sign(priv ed25519.PrivateKey, keyID, audience, nonce string, now time.Time, ttl time.Duration) (string, Statement, error) {
	return SignWithProfile(priv, keyID, audience, nonce, nil, now, ttl)
}

// SignWithProfile signs the optional profile values requested by a website.
func SignWithProfile(priv ed25519.PrivateKey, keyID, audience, nonce string, profile map[string]string, now time.Time, ttl time.Duration) (string, Statement, error) {
	if len(priv) != ed25519.PrivateKeySize {
		return "", Statement{}, errors.New("invalid private key")
	}
	if err := validateProfile(profile); err != nil {
		return "", Statement{}, err
	}
	pub := priv.Public().(ed25519.PublicKey)
	if identity.DeriveID(pub) != keyID {
		return "", Statement{}, ErrWrongKey
	}
	if strings.TrimSpace(audience) == "" {
		return "", Statement{}, fmt.Errorf("%w: missing service", ErrMalformed)
	}
	if nonce == "" {
		return "", Statement{}, fmt.Errorf("%w: missing challenge", ErrMalformed)
	}
	if ttl <= 0 || ttl > MaxTTL {
		ttl = MaxTTL
	}
	now = now.UTC()
	st := Statement{
		Version:   1,
		Kind:      KindAuth,
		KeyID:     keyID,
		Audience:  audience,
		Nonce:     nonce,
		IssuedAt:  now.Unix(),
		ExpiresAt: now.Add(ttl).Unix(),
		Profile:   copyProfile(profile),
	}
	payload, err := json.Marshal(st)
	if err != nil {
		return "", Statement{}, err
	}
	sig := ed25519.Sign(priv, signingInput(payload))
	compact := Prefix + "." + base64.RawURLEncoding.EncodeToString(payload) + "." + base64.RawURLEncoding.EncodeToString(sig)
	return compact, st, nil
}

// ParseUnverified decodes a compact assertion. The signature is NOT checked;
// callers must use Verify before trusting any field.
func ParseUnverified(compact string) (Statement, error) {
	var st Statement
	parts := strings.Split(compact, ".")
	if len(parts) != 3 || parts[0] != Prefix {
		return st, ErrMalformed
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil || len(payload) == 0 || len(payload) > MaxPayloadBytes {
		return st, fmt.Errorf("%w: invalid data", ErrMalformed)
	}
	if err := json.Unmarshal(payload, &st); err != nil {
		return st, fmt.Errorf("%w: %v", ErrMalformed, err)
	}
	if err := validateStatement(st); err != nil {
		return st, err
	}
	return st, nil
}

// Verify checks the signature and all statement-level constraints: key id,
// audience, single-use nonce value, and time window.
func Verify(compact string, pub ed25519.PublicKey, audience, nonce string, now time.Time) (Statement, error) {
	st, err := ParseUnverified(compact)
	if err != nil {
		return st, err
	}
	parts := strings.Split(compact, ".")
	payload, _ := base64.RawURLEncoding.DecodeString(parts[1])
	sig, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil || len(sig) != ed25519.SignatureSize {
		return st, fmt.Errorf("%w: invalid signature length", ErrMalformed)
	}
	if len(pub) != ed25519.PublicKeySize {
		return st, ErrWrongKey
	}
	if identity.DeriveID(pub) != st.KeyID {
		return st, ErrWrongKey
	}
	if st.Audience != audience {
		return st, ErrWrongAudience
	}
	if nonce != "" && st.Nonce != nonce {
		return st, ErrWrongNonce
	}
	now = now.UTC()
	iat := time.Unix(st.IssuedAt, 0)
	exp := time.Unix(st.ExpiresAt, 0)
	if now.After(exp) {
		return st, ErrExpired
	}
	if iat.After(now.Add(MaxClockSkew)) {
		return st, ErrNotYetValid
	}
	if exp.Sub(iat) > MaxTTL+time.Second {
		return st, fmt.Errorf("%w: lifetime too long", ErrMalformed)
	}
	if !ed25519.Verify(pub, signingInput(payload), sig) {
		return st, ErrBadSignature
	}
	return st, nil
}

func validateStatement(st Statement) error {
	if st.Version != 1 {
		return fmt.Errorf("%w: unsupported version", ErrMalformed)
	}
	if st.Kind != KindAuth {
		return ErrWrongKind
	}
	if !identity.ValidID(st.KeyID) {
		return fmt.Errorf("%w: invalid fingerprint", ErrMalformed)
	}
	if st.Audience == "" || st.Nonce == "" {
		return fmt.Errorf("%w: missing service or challenge", ErrMalformed)
	}
	if st.IssuedAt <= 0 || st.ExpiresAt <= st.IssuedAt {
		return fmt.Errorf("%w: invalid time", ErrMalformed)
	}
	if st.ExpiresAt-st.IssuedAt > int64(MaxTTL/time.Second) {
		return fmt.Errorf("%w: lifetime too long", ErrMalformed)
	}
	if err := validateProfile(st.Profile); err != nil {
		return err
	}
	return nil
}

func validateProfile(profile map[string]string) error {
	if len(profile) > MaxProfileFields {
		return fmt.Errorf("%w: too many profile fields", ErrMalformed)
	}
	for key, value := range profile {
		if key == "" || len(key) > 32 {
			return fmt.Errorf("%w: invalid profile field name", ErrMalformed)
		}
		for _, r := range key {
			if (r < 'a' || r > 'z') && (r < 'A' || r > 'Z') && (r < '0' || r > '9') && r != '_' {
				return fmt.Errorf("%w: invalid profile field name", ErrMalformed)
			}
		}
		if len(value) > MaxProfileValueBytes {
			return fmt.Errorf("%w: profile value too long", ErrMalformed)
		}
		if strings.ContainsAny(value, "\x00\r\n") {
			return fmt.Errorf("%w: invalid profile value", ErrMalformed)
		}
	}
	return nil
}

func copyProfile(profile map[string]string) map[string]string {
	if len(profile) == 0 {
		return nil
	}
	out := make(map[string]string, len(profile))
	for key, value := range profile {
		out[key] = value
	}
	return out
}

func signingInput(payload []byte) []byte {
	out := make([]byte, 0, len(Domain)+1+len(payload))
	out = append(out, Domain...)
	out = append(out, '.')
	out = append(out, payload...)
	return out
}
