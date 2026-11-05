package web

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"mime/multipart"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestPublishFlow(t *testing.T) {
	base, rt := startServer(t)
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	client := &http.Client{
		Jar: jar,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}

	res, err := client.Get(base + "/admin")
	if err != nil {
		t.Fatal(err)
	}
	body := readAll(t, res)
	if res.StatusCode != http.StatusSeeOther {
		t.Fatalf("admin without login: status %d body %s", res.StatusCode, body)
	}
	if !strings.Contains(body, "Sign out") && res.Header.Get("Location") != "/" {
		t.Fatalf("admin without login location %q", res.Header.Get("Location"))
	}

	res, err = client.Get(base + "/")
	if err != nil {
		t.Fatal(err)
	}
	body = readAll(t, res)
	if res.StatusCode != http.StatusOK || !strings.Contains(body, `name="setup_token"`) {
		t.Fatalf("setup page: %d %s", res.StatusCode, body)
	}

	res, err = client.Get(base + "/auth/bootstrap/status")
	if err != nil {
		t.Fatal(err)
	}
	body = readAll(t, res)
	if res.StatusCode != http.StatusOK || !strings.Contains(body, `"needsSetup":true`) {
		t.Fatalf("bootstrap status: %d %s", res.StatusCode, body)
	}

	res = postForm(t, client, base+"/signup", url.Values{
		"email":       {"admin@example.com"},
		"password":    {"correct horse battery"},
		"setup_token": {"test-setup-token"},
	})
	if res.StatusCode != http.StatusSeeOther {
		t.Fatalf("signup: %d %s", res.StatusCode, readAll(t, res))
	}
	res.Body.Close()

	res, err = client.Get(base + "/admin")
	if err != nil {
		t.Fatal(err)
	}
	body = readAll(t, res)
	if res.StatusCode != http.StatusOK || !strings.Contains(body, "Create site") {
		t.Fatalf("admin: %d %s", res.StatusCode, body)
	}

	res = postForm(t, client, base+"/admin/sites", nil)
	if res.StatusCode != http.StatusSeeOther {
		t.Fatalf("create site: %d %s", res.StatusCode, readAll(t, res))
	}
	res.Body.Close()

	res, err = client.Get(base + "/admin")
	if err != nil {
		t.Fatal(err)
	}
	body = readAll(t, res)
	m := regexp.MustCompile(`/admin/sites/([a-z]{6})`).FindStringSubmatch(body)
	if m == nil {
		t.Fatalf("no site slug in %s", body)
	}
	slug := m[1]

	res = postFile(t, client, base+"/admin/sites/"+slug+"/files", "index.html", "<h1>from-web</h1>")
	if res.StatusCode != http.StatusSeeOther {
		t.Fatalf("upload: %d %s", res.StatusCode, readAll(t, res))
	}
	res.Body.Close()

	if code, got := getHost(t, base+"/", slug+".localhost"); code != http.StatusOK || got != "<h1>from-web</h1>" {
		t.Fatalf("public file %d %q", code, got)
	}
	if code, apex := getHost(t, base+"/", ""); code == http.StatusOK && strings.Contains(apex, "from-web") {
		t.Fatalf("apex served site file: %q", apex)
	}
	if _, adminOnSite := getHost(t, base+"/admin", slug+".localhost"); strings.Contains(adminOnSite, "Sign out") {
		t.Fatalf("site host served admin: %q", adminOnSite)
	}

	res = postForm(t, client, base+"/admin/tokens", url.Values{"name": {"ci"}})
	if res.StatusCode != http.StatusSeeOther {
		t.Fatalf("token: %d %s", res.StatusCode, readAll(t, res))
	}
	loc := res.Header.Get("Location")
	res.Body.Close()
	res, err = client.Get(base + loc)
	if err != nil {
		t.Fatal(err)
	}
	body = readAll(t, res)
	tm := regexp.MustCompile(`id="new-token"[^>]*value="([^"]+)"`).FindStringSubmatch(body)
	if tm == nil {
		t.Fatalf("token not shown: %s", body)
	}
	token := tm[1]

	anon, err := http.Post(base+"/mcp", "application/json", strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`))
	if err != nil {
		t.Fatal(err)
	}
	anonBody := readAll(t, anon)
	if anon.StatusCode != http.StatusUnauthorized {
		t.Fatalf("anonymous mcp: %d %s", anon.StatusCode, anonBody)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	session := connectMCP(t, ctx, base+"/mcp", &http.Client{Transport: bearerTransport{base: http.DefaultTransport, token: token}})
	defer session.Close()

	created := callTool(t, ctx, session, "create_site", map[string]any{})
	var site struct {
		Slug string `json:"slug"`
		URL  string `json:"url"`
	}
	if err := created.Unmarshal(&site); err != nil {
		t.Fatal(err)
	}
	if !regexp.MustCompile(`^[a-z]{6}$`).MatchString(site.Slug) {
		t.Fatalf("mcp slug %q", site.Slug)
	}
	if !strings.Contains(site.URL, site.Slug+".localhost") {
		t.Fatalf("mcp url %q", site.URL)
	}
	callTool(t, ctx, session, "put_file", map[string]any{
		"slug":    site.Slug,
		"path":    "index.html",
		"content": "<h1>from-mcp</h1>",
	})
	if code, got := getHost(t, base+"/", site.Slug+".localhost"); code != http.StatusOK || got != "<h1>from-mcp</h1>" {
		t.Fatalf("mcp public file %d %q", code, got)
	}
	bad := callToolRaw(t, ctx, session, "put_file", map[string]any{
		"slug":    site.Slug,
		"path":    "../outside.txt",
		"content": "nope",
	})
	if !bad.IsError {
		t.Fatalf("traversal tool succeeded: %+v", bad)
	}
	if _, err := os.Stat(filepath.Join(rt.Auth.DataDir, "outside.txt")); !os.IsNotExist(err) {
		t.Fatalf("outside file: %v", err)
	}

	cookieSession := connectMCP(t, ctx, base+"/mcp", &http.Client{Jar: jar})
	defer cookieSession.Close()
	callTool(t, ctx, cookieSession, "list_files", map[string]any{"slug": site.Slug})
}

type toolResult struct {
	raw *mcp.CallToolResult
}

func (r toolResult) Unmarshal(dest any) error {
	return unmarshalResult(r.raw, dest)
}

func TestBootRequiresBaseDomain(t *testing.T) {
	_, err := Boot(context.Background(), Config{
		BaseURL: "http://localhost:8080",
		DataDir: t.TempDir(),
	})
	if err == nil || !strings.Contains(err.Error(), "BASE_DOMAIN") {
		t.Fatalf("Boot() error = %v", err)
	}
	_, err = Boot(context.Background(), Config{
		BaseURL:    "http://localhost:8080",
		BaseDomain: "   ",
		DataDir:    t.TempDir(),
	})
	if err == nil || !strings.Contains(err.Error(), "BASE_DOMAIN") {
		t.Fatalf("Boot(whitespace) error = %v", err)
	}
}

func startServer(t *testing.T) (string, *Runtime) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	base := "http://" + ln.Addr().String()
	rt, err := Boot(context.Background(), Config{
		BaseURL:    base,
		BaseDomain: "localhost",
		DataDir:    t.TempDir(),
		SetupToken: "test-setup-token",
	})
	if err != nil {
		ln.Close()
		t.Fatal(err)
	}
	srv := &http.Server{Handler: rt.Handler}
	go func() { _ = srv.Serve(ln) }()
	t.Cleanup(func() {
		_ = srv.Close()
		rt.Close()
	})
	return base, rt
}

func postForm(t *testing.T, client *http.Client, rawURL string, form url.Values) *http.Response {
	t.Helper()
	var body io.Reader
	if form != nil {
		body = strings.NewReader(form.Encode())
	}
	req, err := http.NewRequest(http.MethodPost, rawURL, body)
	if err != nil {
		t.Fatal(err)
	}
	if form != nil {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	res, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	return res
}

func postFile(t *testing.T, client *http.Client, rawURL, name, content string) *http.Response {
	t.Helper()
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	part, err := w.CreateFormFile("file", name)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := part.Write([]byte(content)); err != nil {
		t.Fatal(err)
	}
	if err := w.WriteField("path", name); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	req, err := http.NewRequest(http.MethodPost, rawURL, &buf)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", w.FormDataContentType())
	res, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	return res
}

func getHost(t *testing.T, rawURL, host string) (int, string) {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, rawURL, nil)
	if err != nil {
		t.Fatal(err)
	}
	if host != "" {
		req.Host = host
	}
	// Don't follow redirects: FileServer sends /index.html to /, and a
	// followed redirect would drop the site Host.
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}}
	res, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	return res.StatusCode, readAll(t, res)
}

func readAll(t *testing.T, res *http.Response) string {
	t.Helper()
	defer res.Body.Close()
	b, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

type bearerTransport struct {
	base  http.RoundTripper
	token string
}

func (b bearerTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context())
	r.Header.Set("Authorization", "Bearer "+b.token)
	base := b.base
	if base == nil {
		base = http.DefaultTransport
	}
	return base.RoundTrip(r)
}

func connectMCP(t *testing.T, ctx context.Context, endpoint string, httpClient *http.Client) *mcp.ClientSession {
	t.Helper()
	client := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "0"}, nil)
	session, err := client.Connect(ctx, &mcp.StreamableClientTransport{
		Endpoint:             endpoint,
		HTTPClient:           httpClient,
		DisableStandaloneSSE: true,
		MaxRetries:           -1,
	}, nil)
	if err != nil {
		t.Fatalf("mcp connect: %v", err)
	}
	return session
}

func callTool(t *testing.T, ctx context.Context, session *mcp.ClientSession, name string, args map[string]any) toolResult {
	t.Helper()
	res := callToolRaw(t, ctx, session, name, args)
	if res.IsError {
		t.Fatalf("tool %s error: %+v", name, res)
	}
	return toolResult{raw: res}
}

func callToolRaw(t *testing.T, ctx context.Context, session *mcp.ClientSession, name string, args map[string]any) *mcp.CallToolResult {
	t.Helper()
	res, err := session.CallTool(ctx, &mcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		t.Fatalf("tool %s: %v", name, err)
	}
	return res
}

func unmarshalResult(res *mcp.CallToolResult, dest any) error {
	b, err := json.Marshal(res.StructuredContent)
	if err != nil {
		return err
	}
	return json.Unmarshal(b, dest)
}
