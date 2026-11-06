package auth

import (
	"context"
	"crypto/rand"
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	sqlitestore "github.com/glincker/theauth-go/storage/sqlite"
	"github.com/glincker/theauth-go/v2"
	"github.com/go-chi/chi/v5"
	_ "modernc.org/sqlite"
)

// PublishScope is the OAuth scope an MCP access token needs to publish sites.
const PublishScope = "publish"

// App is the theauth instance and the chi routes mounted at /auth/.
type App struct {
	DB      *sql.DB
	Auth    *theauth.TheAuth
	Routes  http.Handler
	DataDir string
}

// Options configures SQLite and theauth.
type Options struct {
	DataDir    string
	BaseURL    string
	SetupToken string
	// CIMDHTTPClient, when set, fetches Client ID Metadata Documents.
	// Nil uses theauth's SSRF-guarded client. Tests point it at a local document.
	CIMDHTTPClient *http.Client
}

// Open migrates theauth's SQLite schema and mounts its routes on a chi router.
// The sites table is migrated separately.
func Open(ctx context.Context, opt Options) (*App, error) {
	if err := os.MkdirAll(opt.DataDir, 0o755); err != nil {
		return nil, err
	}
	dbPath := filepath.Join(opt.DataDir, "app.db")
	db, err := sql.Open("sqlite", sqliteDSN(dbPath))
	if err != nil {
		return nil, err
	}
	// A small pool: MCP validation fetches /oauth/jwks on this same process
	// while a request is in flight, and that handler also uses SQLite.
	db.SetMaxOpenConns(4)
	db.SetMaxIdleConns(4)
	if err := db.PingContext(ctx); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("sqlite ping: %w", err)
	}
	if err := sqlitestore.Migrate(ctx, db); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("theauth migrate: %w", err)
	}
	store, err := sqlitestore.New(db)
	if err != nil {
		_ = db.Close()
		return nil, err
	}
	secure := strings.HasPrefix(strings.ToLower(opt.BaseURL), "https://")
	key, err := encryptionKey(opt.DataDir)
	if err != nil {
		_ = db.Close()
		return nil, err
	}
	token := strings.TrimSpace(opt.SetupToken)
	if token == "" {
		_ = db.Close()
		return nil, errors.New("setup token is empty")
	}
	issuer := strings.TrimRight(opt.BaseURL, "/")
	a, err := theauth.New(theauth.Config{
		CoreStorage: store,
		BaseURL:     opt.BaseURL,
		// Magic-link and password-reset mail is discarded. theauth's email.Noop
		// logs the message, including any link token, so it is not used.
		EmailSender:                   discardMail{},
		SecureCookie:                  secure,
		SuppressSecureCookieWarning:   !secure,
		SuppressTrustedProxiesWarning: true,
		EncryptionKey:                 key,
		Bootstrap: &theauth.BootstrapConfig{
			SetupToken: token,
		},
		AuthorizationServer: &theauth.AuthorizationServerConfig{
			Issuer:                     issuer,
			AllowAnonymousRegistration: true,
			// The library default is one registration per minute per IP. A client
			// that discovers the endpoint and then registers, or retries a 400,
			// is rejected on the second call and reports that registration failed.
			RegistrationRateLimitPerMinute: 30,
			LoginURL:                       "/",
			ClientCompat: theauth.ClientCompatConfig{
				OptionalResource:  true,
				LoopbackAnyPort:   true,
				PathAudience:      true,
				DropUnknownScopes: true,
			},
			// Clients such as Cursor identify themselves with an HTTPS metadata
			// document and several redirect URIs, including cursor://. Anonymous
			// dynamic registration accepts only one loopback URI, so that add
			// path never gets a client_id. The default fetcher refuses non-public
			// addresses. CIMDHTTPClient is nil outside tests.
			CIMD: &theauth.CIMDConfig{
				TrustPolicy:            theauth.AllowAnyHTTPS(),
				AllowedRedirectSchemes: []string{"cursor"},
				HTTPClient:             opt.CIMDHTTPClient,
			},
			Resources: []theauth.ProtectedResource{{
				Identifier: issuer + "/mcp",
				Scopes:     []string{PublishScope},
			}},
		},
	})
	if err != nil {
		_ = db.Close()
		return nil, err
	}
	router := chi.NewRouter()
	a.Mount(router)
	return &App{DB: db, Auth: a, Routes: router, DataDir: opt.DataDir}, nil
}

// Close stops theauth background work and closes the database.
func (a *App) Close() error {
	if a.Auth != nil {
		a.Auth.Close()
	}
	if a.DB != nil {
		return a.DB.Close()
	}
	return nil
}

// Origin is scheme://host[:port] for BaseURL, used when proxying cookie calls.
func Origin(baseURL string) string {
	u, err := url.Parse(baseURL)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return ""
	}
	return strings.ToLower(u.Scheme) + "://" + strings.ToLower(u.Host)
}

// discardMail drops outbound mail. Magic links and password resets are not delivered.
type discardMail struct{}

func (discardMail) Send(context.Context, string, string, string) error { return nil }

// encryptionKey is the 32-byte key that seals the authorization server's
// signing key. It stays in the data directory and is created on first boot.
func encryptionKey(dir string) ([]byte, error) {
	path := filepath.Join(dir, "oauth-encryption.key")
	b, err := os.ReadFile(path)
	if err == nil {
		if len(b) != 32 {
			return nil, fmt.Errorf("oauth encryption key must be 32 bytes")
		}
		return b, nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	b = make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return nil, err
	}
	if err := os.WriteFile(path, b, 0o600); err != nil {
		return nil, err
	}
	return b, nil
}

func sqliteDSN(path string) string {
	// modernc.org/sqlite applies _pragma on every connection. Foreign keys
	// must stay on or sqlitestore.New refuses the database.
	return "file:" + filepath.ToSlash(path) +
		"?_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_txlock=immediate"
}
