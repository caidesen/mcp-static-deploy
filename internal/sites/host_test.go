package sites

import "testing"

func TestCleanRelPath(t *testing.T) {
	ok := []struct{ in, want string }{
		{"index.html", "index.html"},
		{"css/app.css", "css/app.css"},
		{"foo/./bar", "foo/bar"},
		{"foo/../bar", "bar"},
		{`css\app.css`, "css/app.css"},
		{"a/b/../../c", "c"},
	}
	for _, tc := range ok {
		got, err := CleanRelPath(tc.in)
		if err != nil {
			t.Fatalf("CleanRelPath(%q) error: %v", tc.in, err)
		}
		if got != tc.want {
			t.Fatalf("CleanRelPath(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
	bad := []string{
		"",
		".",
		"..",
		"../etc/passwd",
		`..\secret`,
		"/etc/passwd",
		`\windows\system32`,
		"foo/../../etc/passwd",
		"C:/windows/notepad.exe",
		"C:foo",
		"//etc/passwd",
		"foo/../../../etc/passwd",
	}
	for _, in := range bad {
		if _, err := CleanRelPath(in); err == nil {
			t.Fatalf("CleanRelPath(%q) accepted a bad path", in)
		}
	}
}

func TestRandomSlug(t *testing.T) {
	seen := map[string]bool{}
	for range 50 {
		slug, err := RandomSlug()
		if err != nil {
			t.Fatal(err)
		}
		if !ValidSlug(slug) {
			t.Fatalf("slug %q is not 6 lowercase letters", slug)
		}
		seen[slug] = true
	}
	if len(seen) < 40 {
		t.Fatalf("slugs collided too often: %d unique", len(seen))
	}
}

func TestSlugFromHost(t *testing.T) {
	base := "example.com"
	cases := []struct {
		host string
		slug string
		ok   bool
	}{
		{"alksgi.example.com", "alksgi", true},
		{"ALKSgi.example.com", "alksgi", true},
		{"alksgi.example.com:443", "alksgi", true},
		{"alksgi.example.com.", "alksgi", true},
		{"example.com", "", false},
		{"example.com:443", "", false},
		{"EXAMPLE.COM", "", false},
		{"www.example.com", "", false},
		{"foo.bar.example.com", "", false},
		{"notexample.com", "", false},
		{"example.com.evil.com", "", false},
		{"evil.com", "", false},
		{"alksgi.localhost", "", false},
	}
	for _, tc := range cases {
		slug, ok := SlugFromHost(tc.host, base)
		if ok != tc.ok || slug != tc.slug {
			t.Fatalf("SlugFromHost(%q, %q) = (%q, %v), want (%q, %v)", tc.host, base, slug, ok, tc.slug, tc.ok)
		}
	}

	local := []struct {
		host string
		slug string
		ok   bool
	}{
		{"alksgi.localhost", "alksgi", true},
		{"alksgi.localhost:8080", "alksgi", true},
		{"localhost", "", false},
		{"localhost:8080", "", false},
		{"foo.bar.localhost", "", false},
	}
	for _, tc := range local {
		slug, ok := SlugFromHost(tc.host, "localhost")
		if ok != tc.ok || slug != tc.slug {
			t.Fatalf("SlugFromHost(%q, localhost) = (%q, %v), want (%q, %v)", tc.host, slug, ok, tc.slug, tc.ok)
		}
	}
}

func TestPublicURL(t *testing.T) {
	if got := PublicURL("https://example.com", "example.com", "alksgi"); got != "https://alksgi.example.com/" {
		t.Fatal(got)
	}
	if got := PublicURL("http://localhost:8080", "localhost", "alksgi"); got != "http://alksgi.localhost:8080/" {
		t.Fatal(got)
	}
}
