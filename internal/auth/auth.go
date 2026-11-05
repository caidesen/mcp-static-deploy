package auth

import (
	"context"
	"database/sql"
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

// AbilityPublish is the ability a session or API token needs to publish sites.
const AbilityPublish = "publish"

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
	// One connection keeps WAL writers from tripping over each other in-process.
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
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
	a, err := theauth.New(theauth.Config{
		CoreStorage: store,
		BaseURL:     opt.BaseURL,
		// Magic-link and password-reset mail is discarded. theauth's email.Noop
		// logs the message, including any link token, so it is not used.
		EmailSender:                   discardMail{},
		SecureCookie:                  secure,
		SuppressSecureCookieWarning:   !secure,
		SuppressTrustedProxiesWarning: true,
		Bootstrap: &theauth.BootstrapConfig{
			SetupToken: opt.SetupToken,
		},
		APITokens: &theauth.APITokensConfig{
			Abilities: []string{AbilityPublish},
			UserAbilities: func(context.Context, *theauth.User) ([]string, error) {
				return []string{AbilityPublish}, nil
			},
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

func sqliteDSN(path string) string {
	// modernc.org/sqlite applies _pragma on every connection. Foreign keys
	// must stay on or sqlitestore.New refuses the database.
	return "file:" + filepath.ToSlash(path) +
		"?_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_txlock=immediate"
}
