package bridge

import (
	"errors"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
)

const testChallenge = "abcdefghijklmnopqrstuvwxyz123456"

func newTestBridge(t *testing.T, sign SignFunc) *Server {
	t.Helper()
	s, err := newServer(sign, "127.0.0.1:0")
	if err != nil {
		t.Fatalf("newServer: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func doRequest(t *testing.T, method, url, origin, body string) *http.Response {
	t.Helper()
	req, err := http.NewRequest(method, url, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	if origin != "" {
		req.Header.Set("Origin", origin)
	}
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	return resp
}

func readBody(t *testing.T, resp *http.Response) string {
	t.Helper()
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

func TestHealthCORS(t *testing.T) {
	b := newTestBridge(t, func(origin, challenge string, _ []string) (Response, error) {
		return Response{}, nil
	})
	resp := doRequest(t, "GET", b.URL()+"v1/health", "http://localhost:3000", "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	if got := resp.Header.Get("Access-Control-Allow-Origin"); got != "http://localhost:3000" {
		t.Fatalf("allow origin = %q", got)
	}
	if body := readBody(t, resp); !strings.Contains(body, `"ok":true`) {
		t.Fatalf("body = %s", body)
	}
}

func TestPreflight(t *testing.T) {
	b := newTestBridge(t, func(origin, challenge string, _ []string) (Response, error) {
		return Response{}, nil
	})
	req, err := http.NewRequest("OPTIONS", b.URL()+"v1/sign", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Origin", "https://example.com")
	req.Header.Set("Access-Control-Request-Method", "POST")
	req.Header.Set("Access-Control-Request-Private-Network", "true")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	if got := resp.Header.Get("Access-Control-Allow-Origin"); got != "https://example.com" {
		t.Fatalf("allow origin = %q", got)
	}
	if got := resp.Header.Get("Access-Control-Allow-Private-Network"); got != "true" {
		t.Fatalf("private network header = %q", got)
	}
}

func TestSignRequest(t *testing.T) {
	var calls atomic.Int32
	b := newTestBridge(t, func(origin, challenge string, _ []string) (Response, error) {
		calls.Add(1)
		if origin != "http://localhost:3000" || challenge != testChallenge {
			t.Fatalf("unexpected input: %q %q", origin, challenge)
		}
		return Response{Assertion: "dsx1.test.sig", PublicKeyPEM: "pem", KeyID: "DSX-AAAA-AAAA-AAAA-AAAA", Label: "Alex"}, nil
	})
	resp := doRequest(t, "POST", b.URL()+"v1/sign", "http://localhost:3000", `{"challenge":"`+testChallenge+`"}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d body=%s", resp.StatusCode, readBody(t, resp))
	}
	body := readBody(t, resp)
	for _, want := range []string{`"assertion":"dsx1.test.sig"`, `"keyId":"DSX-AAAA-AAAA-AAAA-AAAA"`} {
		if !strings.Contains(body, want) {
			t.Fatalf("body missing %q: %s", want, body)
		}
	}
	if calls.Load() != 1 {
		t.Fatalf("sign calls = %d", calls.Load())
	}
}

func TestSignRequestProfile(t *testing.T) {
	var got []string
	b := newTestBridge(t, func(origin, challenge string, profile []string) (Response, error) {
		got = profile
		return Response{Assertion: "dsx1.test.sig", PublicKeyPEM: "pem", KeyID: "DSX-AAAA-AAAA-AAAA-AAAA"}, nil
	})
	resp := doRequest(t, "POST", b.URL()+"v1/sign", "https://shop.example", `{"challenge":"`+testChallenge+`","profile":["fullName","email","fullName"]}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d body=%s", resp.StatusCode, readBody(t, resp))
	}
	readBody(t, resp)
	if len(got) != 2 || got[0] != "fullName" || got[1] != "email" {
		t.Fatalf("profile = %#v", got)
	}
}

func TestSignRejectsInvalidProfile(t *testing.T) {
	var called atomic.Bool
	b := newTestBridge(t, func(origin, challenge string, profile []string) (Response, error) {
		called.Store(true)
		return Response{}, nil
	})
	resp := doRequest(t, "POST", b.URL()+"v1/sign", "https://shop.example", `{"challenge":"`+testChallenge+`","profile":["bad field"]}`)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	if called.Load() {
		t.Fatal("signer must not be called for an invalid profile request")
	}
	readBody(t, resp)
}

func TestSignRejectsMissingOrigin(t *testing.T) {
	b := newTestBridge(t, func(origin, challenge string, _ []string) (Response, error) {
		return Response{}, errors.New("must not be called")
	})
	resp := doRequest(t, "POST", b.URL()+"v1/sign", "", `{"challenge":"`+testChallenge+`"}`)
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	readBody(t, resp)
}

func TestSignRejectsInvalidChallenge(t *testing.T) {
	var called atomic.Bool
	b := newTestBridge(t, func(origin, challenge string, _ []string) (Response, error) {
		called.Store(true)
		return Response{}, nil
	})
	resp := doRequest(t, "POST", b.URL()+"v1/sign", "http://localhost:3000", `{"challenge":"too-short"}`)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	if called.Load() {
		t.Fatal("signer should not be called for an invalid challenge")
	}
	readBody(t, resp)
}

func TestSignMapsLockedError(t *testing.T) {
	b := newTestBridge(t, func(origin, challenge string, _ []string) (Response, error) {
		return Response{}, ErrLocked
	})
	resp := doRequest(t, "POST", b.URL()+"v1/sign", "http://localhost:3000", `{"challenge":"`+testChallenge+`"}`)
	if resp.StatusCode != http.StatusLocked {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	if body := readBody(t, resp); !strings.Contains(body, "locked") {
		t.Fatalf("body = %s", body)
	}
}

func TestValidOrigin(t *testing.T) {
	valid := []string{
		"https://example.com",
		"https://example.com:8443",
		"http://localhost:3000",
		"http://127.0.0.1:3000",
		"http://[::1]:3000",
	}
	for _, origin := range valid {
		if !validOrigin(origin) {
			t.Errorf("expected %q to be valid", origin)
		}
	}
	invalid := []string{
		"",
		"null",
		"http://example.com",
		"http://localhost.evil",
		"ftp://example.com",
		"https://user:pass@example.com",
		"https://example.com/path",
		"https://example.com?query=1",
		"https://example.com#fragment",
		"javascript:alert(1)",
	}
	for _, origin := range invalid {
		if validOrigin(origin) {
			t.Errorf("expected %q to be rejected", origin)
		}
	}
}

func TestSignRejectsInsecureHTTPOrigin(t *testing.T) {
	var called atomic.Bool
	b := newTestBridge(t, func(origin, challenge string, _ []string) (Response, error) {
		called.Store(true)
		return Response{}, nil
	})
	resp := doRequest(t, "POST", b.URL()+"v1/sign", "http://example.com", `{"challenge":"`+testChallenge+`"}`)
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	if called.Load() {
		t.Fatal("signer must not be called for an insecure HTTP origin")
	}
	readBody(t, resp)
}

func TestSignRejectsWrongContentType(t *testing.T) {
	b := newTestBridge(t, func(origin, challenge string, _ []string) (Response, error) {
		return Response{}, nil
	})
	req, err := http.NewRequest("POST", b.URL()+"v1/sign", strings.NewReader(`{"challenge":"`+testChallenge+`"}`))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Origin", "http://localhost:3000")
	req.Header.Set("Content-Type", "text/plain")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusUnsupportedMediaType {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	readBody(t, resp)
}
