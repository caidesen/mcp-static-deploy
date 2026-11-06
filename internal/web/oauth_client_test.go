package web

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/glincker/theauth-go/v2/crypto"
	"github.com/modelcontextprotocol/go-sdk/auth"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// cursorRedirectURI is the loopback callback Cursor's published client
// metadata document lists for the desktop app.
const cursorRedirectURI = "http://localhost:8787/callback"

// TestCursorClientCanAddMCP is the path Cursor uses to add an MCP server.
// The authorization server must advertise Client ID Metadata Documents.
// Without that, the client falls back to dynamic registration, and that
// registration is rejected: Cursor sends several redirect URIs, including
// cursor://, and anonymous registration allows one http loopback URI.
func TestCursorClientCanAddMCP(t *testing.T) {
	meta := publishCursorMetadata(t)
	base, _ := startServerCfg(t, Config{CIMDHTTPClient: meta.client})

	res, err := http.Get(base + "/.well-known/oauth-authorization-server")
	if err != nil {
		t.Fatal(err)
	}
	body := readAll(t, res)
	var asm struct {
		CIMD bool `json:"client_id_metadata_document_supported"`
	}
	if err := json.Unmarshal([]byte(body), &asm); err != nil {
		t.Fatalf("authorization server metadata: %s", body)
	}
	if !asm.CIMD {
		t.Fatal("authorization server does not advertise client_id_metadata_document_supported; a CIMD client cannot add this server")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	handler, err := auth.NewAuthorizationCodeHandler(&auth.AuthorizationCodeHandlerConfig{
		ClientIDMetadataDocumentConfig: &auth.ClientIDMetadataDocumentConfig{URL: meta.url},
		RedirectURL:                    cursorRedirectURI,
		AuthorizationCodeFetcher:       signInAndApprove(t, base),
	})
	if err != nil {
		t.Fatal(err)
	}
	client := mcp.NewClient(&mcp.Implementation{Name: "cursor", Version: "0"}, nil)
	session, err := client.Connect(ctx, &mcp.StreamableClientTransport{
		Endpoint:             base + "/mcp",
		DisableStandaloneSSE: true,
		MaxRetries:           -1,
		OAuthHandler:         handler,
	}, nil)
	if err != nil {
		t.Fatalf("client add: %v", err)
	}
	defer session.Close()
	if session.InitializeResult() == nil || session.InitializeResult().ProtocolVersion == "" {
		t.Fatalf("initialize result %#v", session.InitializeResult())
	}
}

// TestAuthorizeDropsUnknownScopes covers clients that send vendor scopes
// (offline_access, claudeai) next to publish. Strict scope checks reject the
// whole authorize request, so the browser step of adding the server fails.
func TestAuthorizeDropsUnknownScopes(t *testing.T) {
	base, _ := startServer(t)
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
	res := postJSON(t, client, base+"/api/signup", map[string]string{
		"email":      "owner@example.com",
		"password":   "a long passphrase",
		"setupToken": "test-setup-token",
	})
	if res.StatusCode != http.StatusOK {
		t.Fatalf("signup: %d %s", res.StatusCode, readAll(t, res))
	}
	res.Body.Close()

	const redirectURI = "http://127.0.0.1:1/callback"
	reg := postJSON(t, client, base+"/oauth/register", map[string]any{
		"client_name":                "claude",
		"redirect_uris":              []string{redirectURI},
		"grant_types":                []string{"authorization_code"},
		"response_types":             []string{"code"},
		"token_endpoint_auth_method": "none",
	})
	regBody := readAll(t, reg)
	if reg.StatusCode != http.StatusCreated {
		t.Fatalf("register: %d %s", reg.StatusCode, regBody)
	}
	var created struct {
		ClientID string `json:"client_id"`
	}
	if err := json.Unmarshal([]byte(regBody), &created); err != nil || created.ClientID == "" {
		t.Fatalf("register body: %s", regBody)
	}
	verifier, err := crypto.NewCodeVerifier()
	if err != nil {
		t.Fatal(err)
	}
	q := url.Values{
		"response_type":         {"code"},
		"client_id":             {created.ClientID},
		"redirect_uri":          {redirectURI},
		"scope":                 {"publish offline_access claudeai"},
		"state":                 {"st"},
		"resource":              {base + "/mcp"},
		"code_challenge":        {crypto.CodeChallenge(verifier)},
		"code_challenge_method": {"S256"},
	}
	authRes, err := client.Get(base + "/oauth/authorize?" + q.Encode())
	if err != nil {
		t.Fatal(err)
	}
	locHeader := authRes.Header.Get("Location")
	authRes.Body.Close()
	loc, err := url.Parse(locHeader)
	if err != nil {
		t.Fatal(err)
	}
	if loc.Query().Get("error") == "invalid_scope" || loc.Query().Get("code") == "" {
		t.Fatalf("authorize with vendor scopes: %d %s", authRes.StatusCode, locHeader)
	}
}

// publishCursorMetadata serves the shape of https://cursor.com/oauth/mcp-client.json
// on a local TLS server. The document also lists cursor://, which clients still send.
func publishCursorMetadata(t *testing.T) struct {
	url    string
	client *http.Client
} {
	t.Helper()
	var docURL string
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/oauth/mcp-client.json" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"client_id":   docURL,
			"client_name": "Cursor",
			"redirect_uris": []string{
				"https://www.cursor.com/agents/mcp/oauth/callback",
				cursorRedirectURI,
				"http://127.0.0.1:8787/callback",
				"cursor://anysphere.cursor-mcp/oauth/callback",
			},
			"grant_types":                []string{"authorization_code", "refresh_token"},
			"response_types":             []string{"code"},
			"token_endpoint_auth_method": "none",
		})
	}))
	t.Cleanup(srv.Close)
	docURL = srv.URL + "/oauth/mcp-client.json"
	client := srv.Client()
	client.Timeout = 3 * time.Second
	return struct {
		url    string
		client *http.Client
	}{url: docURL, client: client}
}

// signInAndApprove is the browser half of adding the server. The user is not
// signed in when the client opens the authorization URL.
func signInAndApprove(t *testing.T, base string) auth.AuthorizationCodeFetcher {
	t.Helper()
	return func(ctx context.Context, args *auth.AuthorizationArgs) (*auth.AuthorizationResult, error) {
		jar, err := cookiejar.New(nil)
		if err != nil {
			return nil, err
		}
		browser := &http.Client{
			Jar: jar,
			CheckRedirect: func(*http.Request, []*http.Request) error {
				return http.ErrUseLastResponse
			},
		}
		loc := args.URL
		for range 6 {
			req, err := http.NewRequestWithContext(ctx, http.MethodGet, loc, nil)
			if err != nil {
				return nil, err
			}
			res, err := browser.Do(req)
			if err != nil {
				return nil, err
			}
			next := res.Header.Get("Location")
			body, _ := io.ReadAll(res.Body)
			res.Body.Close()
			t.Logf("browser %s -> %d %s", loc, res.StatusCode, next)
			if res.StatusCode/100 != 3 || next == "" {
				return nil, errString("authorize did not redirect: " + res.Status + " " + string(body))
			}
			nextURL, err := url.Parse(next)
			if err != nil {
				return nil, err
			}
			if !nextURL.IsAbs() {
				baseURL, _ := url.Parse(loc)
				nextURL = baseURL.ResolveReference(nextURL)
			}
			if code := nextURL.Query().Get("code"); code != "" {
				return &auth.AuthorizationResult{
					Code:  code,
					State: nextURL.Query().Get("state"),
					Iss:   nextURL.Query().Get("iss"),
				}, nil
			}
			if strings.HasPrefix(nextURL.Path, "/oauth/") {
				loc = nextURL.String()
				continue
			}
			if err := browserSignIn(ctx, browser, base); err != nil {
				return nil, err
			}
			loc = nextURL.String()
		}
		return nil, errString("authorize did not return a code")
	}
}

func browserSignIn(ctx context.Context, browser *http.Client, base string) error {
	body := `{"email":"owner@example.com","password":"a long passphrase","setupToken":"test-setup-token"}`
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, base+"/api/signup", strings.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	res, err := browser.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	raw, _ := io.ReadAll(res.Body)
	if res.StatusCode != http.StatusOK {
		return errString("signup " + res.Status + " " + string(raw))
	}
	return nil
}

type errString string

func (e errString) Error() string { return string(e) }
