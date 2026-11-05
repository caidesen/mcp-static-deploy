package web

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestCatalogKeysMatch(t *testing.T) {
	if len(messages[langEN]) != len(messages[langZH]) {
		t.Fatalf("en %d zh %d", len(messages[langEN]), len(messages[langZH]))
	}
	for key, en := range messages[langEN] {
		zh, ok := messages[langZH][key]
		if !ok || en == "" || zh == "" || en == zh {
			t.Fatalf("key %q en %q zh %q", key, en, zh)
		}
	}
}

func TestNegotiateLang(t *testing.T) {
	cases := []struct {
		cookie string
		accept string
		want   string
	}{
		{"", "", langEN},
		{"", "zh-CN,zh;q=0.9,en;q=0.8", langZH},
		{"", "zh-TW,en;q=0.5", langZH},
		{"", "en-US,en;q=0.9,zh;q=0.2", langEN},
		{"", "fr-FR,fr;q=0.9,zh;q=0.4", langEN},
		{"", "zh;q=0,en;q=0.8", langEN},
		{"zh", "en-US", langZH},
		{"en", "zh-CN", langEN},
		{"nope", "zh-CN", langZH},
	}
	for _, tc := range cases {
		r := httptest.NewRequest(http.MethodGet, "/", nil)
		if tc.cookie != "" {
			r.AddCookie(&http.Cookie{Name: langCookie, Value: tc.cookie})
		}
		if tc.accept != "" {
			r.Header.Set("Accept-Language", tc.accept)
		}
		if got := negotiateLang(r); got != tc.want {
			t.Fatalf("cookie %q accept %q = %s, want %s", tc.cookie, tc.accept, got, tc.want)
		}
	}
}

func TestLangRedirect(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "http://127.0.0.1:8080/lang?set=zh", nil)
	r.Host = "127.0.0.1:8080"
	r.Header.Set("Referer", "http://127.0.0.1:8080/admin/sites/abcdef")
	if got := langRedirect(r); got != "/admin/sites/abcdef" {
		t.Fatalf("same host: %s", got)
	}
	r.Header.Set("Referer", "https://example.com/admin")
	if got := langRedirect(r); got != "/" {
		t.Fatalf("other host: %s", got)
	}
}
