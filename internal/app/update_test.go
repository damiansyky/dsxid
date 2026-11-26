package app

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestParseVersion(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want [3]int
		ok   bool
	}{
		{"release", "0.1.0", [3]int{0, 1, 0}, true},
		{"v prefix", "v0.1.1", [3]int{0, 1, 1}, true},
		{"uppercase v", "V1.2.3", [3]int{1, 2, 3}, true},
		{"two parts", "0.2", [3]int{0, 2, 0}, true},
		{"one part", "2", [3]int{2, 0, 0}, true},
		{"prerelease", "0.2.0-beta.1", [3]int{0, 2, 0}, true},
		{"build metadata", "0.2.0+abc", [3]int{0, 2, 0}, true},
		{"empty", "", [3]int{}, false},
		{"four parts", "1.2.3.4", [3]int{}, false},
		{"text", "latest", [3]int{}, false},
		{"negative", "-1.0.0", [3]int{}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := parseVersion(tc.in)
			if ok != tc.ok || got != tc.want {
				t.Fatalf("parseVersion(%q) = %v, %v; want %v, %v", tc.in, got, ok, tc.want, tc.ok)
			}
		})
	}
}

func TestCompareVersions(t *testing.T) {
	cases := []struct {
		a, b string
		want int
	}{
		{"0.1.0", "0.1.0", 0},
		{"0.1.1", "0.1.0", 1},
		{"v0.1.0", "0.2.0", -1},
		{"1.0.0", "0.9.9", 1},
		{"0.1.0", "bad", 0},
	}
	for _, tc := range cases {
		if got := compareVersions(tc.a, tc.b); got != tc.want {
			t.Errorf("compareVersions(%q, %q) = %d; want %d", tc.a, tc.b, got, tc.want)
		}
	}
}

func TestCheckForUpdates(t *testing.T) {
	t.Run("newer release", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if got := r.Header.Get("User-Agent"); got != "DsxID/0.1.0" {
				t.Errorf("User-Agent = %q", got)
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"tag_name":"v0.1.1","html_url":"https://github.com/damiansyky/dsxid/releases/tag/0.1.1"}`))
		}))
		defer server.Close()

		info, err := checkForUpdates(context.Background(), server.Client(), server.URL, "0.1.0")
		if err != nil {
			t.Fatalf("checkForUpdates: %v", err)
		}
		if !info.Available || info.Latest != "0.1.1" || info.Current != "0.1.0" {
			t.Fatalf("unexpected info: %+v", info)
		}
		if info.URL != "https://github.com/damiansyky/dsxid/releases/tag/0.1.1" {
			t.Fatalf("URL = %q", info.URL)
		}
	})

	t.Run("same release", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, _ = w.Write([]byte(`{"tag_name":"0.1.0","html_url":"https://github.com/damiansyky/dsxid/releases/tag/0.1.0"}`))
		}))
		defer server.Close()

		info, err := checkForUpdates(context.Background(), server.Client(), server.URL, "0.1.0")
		if err != nil {
			t.Fatalf("checkForUpdates: %v", err)
		}
		if info.Available {
			t.Fatalf("same version reported as available: %+v", info)
		}
	})

	t.Run("untrusted release URL", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, _ = w.Write([]byte(`{"tag_name":"0.2.0","html_url":"https://example.com/fake"}`))
		}))
		defer server.Close()

		info, err := checkForUpdates(context.Background(), server.Client(), server.URL, "0.1.0")
		if err != nil {
			t.Fatalf("checkForUpdates: %v", err)
		}
		if info.URL != releasesPageURL {
			t.Fatalf("URL = %q; want fallback %q", info.URL, releasesPageURL)
		}
	})

	t.Run("http error", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			http.Error(w, "rate limited", http.StatusForbidden)
		}))
		defer server.Close()

		if _, err := checkForUpdates(context.Background(), server.Client(), server.URL, "0.1.0"); err == nil {
			t.Fatal("expected an error")
		}
	})

	t.Run("invalid tag", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, _ = w.Write([]byte(`{"tag_name":"not-a-version"}`))
		}))
		defer server.Close()

		if _, err := checkForUpdates(context.Background(), server.Client(), server.URL, "0.1.0"); err == nil {
			t.Fatal("expected an error")
		}
	})
}
