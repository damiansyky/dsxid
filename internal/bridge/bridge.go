// Package bridge exposes a tiny localhost-only HTTP bridge that lets websites
// request a DsxID signature. Every request is validated and then approved by the
// user in the desktop app. The bridge itself never touches the private key.
package bridge

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"mime"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
)

const (
	// ListenAddress is the fixed loopback address used by the JavaScript SDK.
	ListenAddress = "127.0.0.1:41991"
	maxBodyBytes  = 8 * 1024
)

var (
	// ErrLocked means DsxID is locked and cannot sign.
	ErrLocked = errors.New("DsxID is locked")
	// ErrNoIdentity means there is no identity on this computer.
	ErrNoIdentity = errors.New("no identity is available")
	// ErrDenied means the user denied the request.
	ErrDenied = errors.New("the request was denied in DsxID")
	// ErrTimeout means the user did not answer in time.
	ErrTimeout = errors.New("the request timed out")
	// ErrBusy means too many sign-in requests are waiting.
	ErrBusy = errors.New("too many pending sign-in requests")
)

var (
	challengePattern  = regexp.MustCompile(`^[A-Za-z0-9_-]{16,512}$`)
	profileKeyPattern = regexp.MustCompile(`^[A-Za-z0-9_]{1,32}$`)
	maxProfileFields  = 10
)

// Response is a signed assertion returned to the website.
type Response struct {
	Assertion    string `json:"assertion"`
	PublicKeyPEM string `json:"publicKeyPem"`
	KeyID        string `json:"keyId"`
	Label        string `json:"label"`
}

// SignFunc asks DsxID to approve and sign a challenge for the given origin.
// Profile keys are the optional e-shop fields the website wants to receive.
type SignFunc func(origin, challenge string, profile []string) (Response, error)

// Server is the localhost bridge.
type Server struct {
	server *http.Server
	url    string
	sign   SignFunc
}

// New starts the bridge on the standard port.
func New(sign SignFunc) (*Server, error) {
	return newServer(sign, ListenAddress)
}

func newServer(sign SignFunc, address string) (*Server, error) {
	if sign == nil {
		return nil, errors.New("bridge requires a signer")
	}
	ln, err := net.Listen("tcp", address)
	if err != nil {
		return nil, fmt.Errorf("listen for the web bridge: %w", err)
	}
	s := &Server{
		sign: sign,
		url:  "http://" + ln.Addr().String() + "/",
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/health", s.handleHealth)
	mux.HandleFunc("/v1/sign", s.handleSign)
	s.server = &http.Server{
		Handler:           securityHeaders(mux),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		// A request waits for the user to approve it in the desktop app.
		WriteTimeout: 3 * time.Minute,
		IdleTimeout:  60 * time.Second,
	}
	go func() {
		if err := s.server.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Printf("web bridge stopped: %v", err)
		}
	}()
	return s, nil
}

// URL returns the bridge address (for diagnostics).
func (s *Server) URL() string { return s.url }

// Close stops the bridge.
func (s *Server) Close() error {
	return s.server.Close()
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodOptions {
		if !s.applyCORS(w, r) {
			return
		}
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if r.Method != http.MethodGet {
		methodNotAllowed(w, http.MethodGet)
		return
	}
	if !s.applyCORS(w, r) {
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "app": "DsxID"})
}

func (s *Server) handleSign(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodOptions {
		if !s.applyCORS(w, r) {
			return
		}
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if r.Method != http.MethodPost {
		methodNotAllowed(w, http.MethodPost)
		return
	}
	if !s.applyCORS(w, r) {
		return
	}
	if contentType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type")); err != nil || contentType != "application/json" {
		writeError(w, http.StatusUnsupportedMediaType, "Content-Type must be application/json.")
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)
	var input struct {
		Challenge string   `json:"challenge"`
		Profile   []string `json:"profile,omitempty"`
	}
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil {
		writeError(w, http.StatusBadRequest, "Invalid JSON request.")
		return
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		writeError(w, http.StatusBadRequest, "The request must contain one JSON object.")
		return
	}
	challenge := strings.TrimSpace(input.Challenge)
	if !challengePattern.MatchString(challenge) {
		writeError(w, http.StatusBadRequest, "Invalid challenge.")
		return
	}

	profile, err := normalizeProfileFields(input.Profile)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	origin := r.Header.Get("Origin")
	response, err := s.sign(origin, challenge, profile)
	if err != nil {
		switch {
		case errors.Is(err, ErrLocked), errors.Is(err, ErrNoIdentity):
			writeError(w, http.StatusLocked, err.Error())
		case errors.Is(err, ErrDenied):
			writeError(w, http.StatusForbidden, err.Error())
		case errors.Is(err, ErrTimeout):
			writeError(w, http.StatusGatewayTimeout, err.Error())
		case errors.Is(err, ErrBusy):
			writeError(w, http.StatusTooManyRequests, err.Error())
		default:
			log.Printf("web bridge signing failed: %v", err)
			writeError(w, http.StatusInternalServerError, "The signature could not be created.")
		}
		return
	}
	writeJSON(w, http.StatusOK, response)
}

func (s *Server) applyCORS(w http.ResponseWriter, r *http.Request) bool {
	origin := r.Header.Get("Origin")
	if !validOrigin(origin) {
		writeError(w, http.StatusForbidden, "A valid website origin is required.")
		return false
	}
	w.Header().Set("Access-Control-Allow-Origin", origin)
	w.Header().Add("Vary", "Origin")
	w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
	w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
	w.Header().Set("Access-Control-Max-Age", "600")
	if strings.EqualFold(r.Header.Get("Access-Control-Request-Private-Network"), "true") {
		w.Header().Set("Access-Control-Allow-Private-Network", "true")
	}
	return true
}

func normalizeProfileFields(fields []string) ([]string, error) {
	if len(fields) > maxProfileFields {
		return nil, fmt.Errorf("at most %d profile fields are allowed", maxProfileFields)
	}
	seen := make(map[string]bool, len(fields))
	out := make([]string, 0, len(fields))
	for _, field := range fields {
		field = strings.TrimSpace(field)
		if field == "" || !profileKeyPattern.MatchString(field) {
			return nil, errors.New("invalid profile field name")
		}
		if seen[field] {
			continue
		}
		seen[field] = true
		out = append(out, field)
	}
	return out, nil
}

func validOrigin(origin string) bool {
	if origin == "" || origin == "null" {
		return false
	}
	origin = strings.TrimRight(origin, "/")
	if strings.ContainsAny(origin, " \t\r\n") {
		return false
	}
	u, err := url.Parse(origin)
	if err != nil || u.Host == "" || u.User != nil || u.Fragment != "" || u.RawQuery != "" {
		return false
	}
	if u.Path != "" && u.Path != "/" {
		return false
	}
	switch strings.ToLower(u.Scheme) {
	case "https":
		return true
	case "http":
		// Insecure HTTP is allowed only for local development. A website served
		// over HTTP can be modified by a network attacker before the user
		// approves the sign-in, so it must never receive a signature.
		return isLoopbackHost(u.Hostname())
	default:
		return false
	}
}

func isLoopbackHost(host string) bool {
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "no-referrer")
		next.ServeHTTP(w, r)
	})
}

func methodNotAllowed(w http.ResponseWriter, allow string) {
	w.Header().Set("Allow", allow)
	http.Error(w, "Method not allowed.", http.StatusMethodNotAllowed)
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"error": message})
}
