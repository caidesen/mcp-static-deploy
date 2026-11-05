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

	"github.com/glincker/theauth-go/v2/crypto"
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
	if strings.Contains(body, "Create API token") || strings.Contains(body, "API token") {
		t.Fatalf("token form still on admin page")
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

	meta, err := http.Get(base + "/.well-known/oauth-protected-resource/mcp")
	if err != nil {
		t.Fatal(err)
	}
	metaBody := readAll(t, meta)
	if meta.StatusCode != http.StatusOK || !strings.Contains(metaBody, `"authorization_servers"`) || !strings.Contains(metaBody, base) {
		t.Fatalf("protected resource metadata: %d %s", meta.StatusCode, metaBody)
	}
	as, err := http.Get(base + "/.well-known/oauth-authorization-server")
	if err != nil {
		t.Fatal(err)
	}
	asBody := readAll(t, as)
	if as.StatusCode != http.StatusOK || !strings.Contains(asBody, "/oauth/token") || !strings.Contains(asBody, "/oauth/register") {
		t.Fatalf("authorization server metadata: %d %s", as.StatusCode, asBody)
	}

	token := accessToken(t, client, base)

	anon, err := http.Post(base+"/mcp", "application/json", strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`))
	if err != nil {
		t.Fatal(err)
	}
	anonBody := readAll(t, anon)
	if anon.StatusCode != http.StatusUnauthorized || !strings.Contains(anon.Header.Get("WWW-Authenticate"), "resource_metadata") {
		t.Fatalf("anonymous mcp: %d %s %q", anon.StatusCode, anonBody, anon.Header.Get("WWW-Authenticate"))
	}
	cookieReq, err := http.NewRequest(http.MethodPost, base+"/mcp", strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`))
	if err != nil {
		t.Fatal(err)
	}
	cookieReq.Header.Set("Content-Type", "application/json")
	cookieRes, err := client.Do(cookieReq)
	if err != nil {
		t.Fatal(err)
	}
	cookieBody := readAll(t, cookieRes)
	if cookieRes.StatusCode != http.StatusUnauthorized {
		t.Fatalf("session cookie mcp: %d %s", cookieRes.StatusCode, cookieBody)
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

func accessToken(t *testing.T, client *http.Client, base string) string {
	t.Helper()
	const redirectURI = "http://127.0.0.1:1/callback"
	regBody := `{"client_name":"test","redirect_uris":["` + redirectURI + `"],"grant_types":["authorization_code","refresh_token"],"response_types":["code"],"token_endpoint_auth_method":"none"}`
	req, err := http.NewRequest(http.MethodPost, base+"/oauth/register", strings.NewReader(regBody))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	res, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	body := readAll(t, res)
	if res.StatusCode != http.StatusCreated {
		t.Fatalf("register: %d %s", res.StatusCode, body)
	}
	var reg struct {
		ClientID string `json:"client_id"`
	}
	if err := json.Unmarshal([]byte(body), &reg); err != nil || reg.ClientID == "" {
		t.Fatalf("register body: %s", body)
	}
	verifier, err := crypto.NewCodeVerifier()
	if err != nil {
		t.Fatal(err)
	}
	q := url.Values{
		"response_type":         {"code"},
		"client_id":             {reg.ClientID},
		"redirect_uri":          {redirectURI},
		"scope":                 {"publish"},
		"state":                 {"st"},
		"resource":              {base + "/mcp"},
		"code_challenge":        {crypto.CodeChallenge(verifier)},
		"code_challenge_method": {"S256"},
	}
	res, err = client.Get(base + "/oauth/authorize?" + q.Encode())
	if err != nil {
		t.Fatal(err)
	}
	locHeader := res.Header.Get("Location")
	res.Body.Close()
	if res.StatusCode != http.StatusFound {
		t.Fatalf("authorize: %d location %q", res.StatusCode, locHeader)
	}
	loc, err := url.Parse(locHeader)
	if err != nil {
		t.Fatal(err)
	}
	code := loc.Query().Get("code")
	if code == "" || loc.Query().Get("state") != "st" {
		t.Fatalf("authorize location %s", locHeader)
	}
	res = postForm(t, client, base+"/oauth/token", url.Values{
		"grant_type":    {"authorization_code"},
		"client_id":     {reg.ClientID},
		"code":          {code},
		"code_verifier": {verifier},
		"redirect_uri":  {redirectURI},
		"resource":      {base + "/mcp"},
	})
	body = readAll(t, res)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("token: %d %s", res.StatusCode, body)
	}
	var tok struct {
		AccessToken string `json:"access_token"`
	}
	if err := json.Unmarshal([]byte(body), &tok); err != nil || tok.AccessToken == "" {
		t.Fatalf("token body: %s", body)
	}
	return tok.AccessToken
}

func TestSafeOAuthNext(t *testing.T) {
	if got := safeOAuthNext("/oauth/authorize?client_id=a"); got != "/oauth/authorize?client_id=a" {
		t.Fatalf("next %q", got)
	}
	for _, bad := range []string{"https://evil.example/oauth/authorize", "//evil.example/oauth/authorize", "/admin", "/oauth/../admin", ""} {
		if got := safeOAuthNext(bad); got != "" {
			t.Fatalf("%q -> %q", bad, got)
		}
	}
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

func TestLanguage(t *testing.T) {
	base, _ := startServer(t)

	zhReq, err := http.NewRequest(http.MethodGet, base+"/", nil)
	if err != nil {
		t.Fatal(err)
	}
	zhReq.Header.Set("Accept-Language", "zh-CN,zh;q=0.9,en;q=0.8")
	zhRes, err := http.DefaultClient.Do(zhReq)
	if err != nil {
		t.Fatal(err)
	}
	zhBody := readAll(t, zhRes)
	if zhRes.StatusCode != http.StatusOK || !strings.Contains(zhBody, "创建第一个账号") || strings.Contains(zhBody, "Create the first account") {
		t.Fatalf("chinese signup: %d %s", zhRes.StatusCode, zhBody)
	}
	if !strings.Contains(zhBody, `href="/lang?set=zh"`) || !strings.Contains(zhBody, `aria-current="true"`) {
		t.Fatalf("language switch missing: %s", zhBody)
	}

	enReq, err := http.NewRequest(http.MethodGet, base+"/", nil)
	if err != nil {
		t.Fatal(err)
	}
	enReq.Header.Set("Accept-Language", "fr-FR,fr;q=0.9,zh;q=0.4")
	enRes, err := http.DefaultClient.Do(enReq)
	if err != nil {
		t.Fatal(err)
	}
	enBody := readAll(t, enRes)
	if !strings.Contains(enBody, "Create the first account") || strings.Contains(enBody, "创建第一个账号") {
		t.Fatalf("non-chinese accept-language should be English: %s", enBody)
	}

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
	switchReq, err := http.NewRequest(http.MethodGet, base+"/lang?set=en", nil)
	if err != nil {
		t.Fatal(err)
	}
	switchReq.Header.Set("Referer", base+"/")
	switchReq.Header.Set("Accept-Language", "zh-CN")
	switchRes, err := client.Do(switchReq)
	if err != nil {
		t.Fatal(err)
	}
	switchRes.Body.Close()
	if switchRes.StatusCode != http.StatusSeeOther || switchRes.Header.Get("Location") != "/" {
		t.Fatalf("lang redirect: %d %s", switchRes.StatusCode, switchRes.Header.Get("Location"))
	}
	var saw bool
	for _, c := range switchRes.Cookies() {
		if c.Name == "lang" && c.Value == "en" {
			saw = true
		}
	}
	if !saw {
		t.Fatalf("lang cookie not set: %v", switchRes.Cookies())
	}
	after, err := client.Get(base + "/")
	if err != nil {
		t.Fatal(err)
	}
	afterBody := readAll(t, after)
	if !strings.Contains(afterBody, "Create the first account") {
		t.Fatalf("cookie should override Accept-Language: %s", afterBody)
	}
}

func unmarshalResult(res *mcp.CallToolResult, dest any) error {
	b, err := json.Marshal(res.StructuredContent)
	if err != nil {
		return err
	}
	return json.Unmarshal(b, dest)
}
