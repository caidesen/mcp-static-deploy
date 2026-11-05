package web

import (
	"bytes"
	"context"
	"crypto/rand"
	"embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"html/template"
	"io/fs"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/caidesen/mcp-static-deploy/internal/auth"
	"github.com/caidesen/mcp-static-deploy/internal/mcp"
	"github.com/caidesen/mcp-static-deploy/internal/sites"
	"github.com/glincker/theauth-go/v2"
	"github.com/labstack/echo/v4"
	echomw "github.com/labstack/echo/v4/middleware"
)

//go:embed templates/*.html static/*
var embedded embed.FS

// Config is the process configuration the HTTP server needs.
type Config struct {
	BaseURL    string
	BaseDomain string
	DataDir    string
	SetupToken string
}

// Runtime is a booted server.
type Runtime struct {
	Handler http.Handler
	Auth    *auth.App
	Sites   *sites.Store
	close   func()
}

// Close releases theauth and the database.
func (r *Runtime) Close() {
	if r != nil && r.close != nil {
		r.close()
	}
}

// Boot opens SQLite, migrates theauth and the sites table, and builds the handler.
func Boot(ctx context.Context, cfg Config) (*Runtime, error) {
	cfg.BaseDomain = sites.NormalizeDomain(cfg.BaseDomain)
	if cfg.BaseDomain == "" {
		return nil, errors.New("BASE_DOMAIN is empty")
	}
	u, err := url.Parse(cfg.BaseURL)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return nil, errors.New("BASE_URL must be an absolute http or https URL")
	}
	app, err := auth.Open(ctx, auth.Options{
		DataDir:    cfg.DataDir,
		BaseURL:    cfg.BaseURL,
		SetupToken: cfg.SetupToken,
	})
	if err != nil {
		return nil, err
	}
	if err := sites.Migrate(ctx, app.DB); err != nil {
		app.Close()
		return nil, err
	}
	store := sites.New(app.DB, cfg.DataDir)
	handler, err := newServer(cfg, app, store)
	if err != nil {
		app.Close()
		return nil, err
	}
	return &Runtime{
		Handler: handler,
		Auth:    app,
		Sites:   store,
		close:   func() { _ = app.Close() },
	}, nil
}

type server struct {
	echo       *echo.Echo
	auth       *auth.App
	sites      *sites.Store
	cfg        Config
	origin     string
	tmpl       *template.Template
	flashes    *flashBox
	baseDomain string
}

func newServer(cfg Config, app *auth.App, store *sites.Store) (http.Handler, error) {
	tmpl, err := template.ParseFS(embedded, "templates/*.html")
	if err != nil {
		return nil, err
	}
	static, err := fs.Sub(embedded, "static")
	if err != nil {
		return nil, err
	}
	e := echo.New()
	e.HideBanner = true
	e.HidePort = true
	s := &server{
		echo:       e,
		auth:       app,
		sites:      store,
		cfg:        cfg,
		origin:     auth.Origin(cfg.BaseURL),
		tmpl:       tmpl,
		flashes:    newFlashBox(),
		baseDomain: cfg.BaseDomain,
	}
	e.Use(echomw.Recover())
	e.Use(func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c echo.Context) error {
			c.Response().Header().Set("Referrer-Policy", "no-referrer")
			c.Response().Header().Set("X-Content-Type-Options", "nosniff")
			return next(c)
		}
	})
	e.Use(echo.WrapMiddleware(app.Auth.Authn()))

	e.GET("/", s.home)
	e.POST("/signup", s.signup)
	e.POST("/login", s.login)
	e.POST("/logout", s.logout)
	e.GET("/static/*", echo.WrapHandler(http.StripPrefix("/static/", http.FileServer(http.FS(static)))))

	admin := e.Group("/admin", s.requireUser)
	admin.GET("", s.admin)
	admin.POST("/sites", s.createSite)
	admin.GET("/sites/:slug", s.sitePage)
	admin.POST("/sites/:slug/files", s.upload)
	admin.POST("/sites/:slug/files/delete", s.deleteFile)
	admin.POST("/tokens", s.createToken)

	// Official echo example: mount theauth on chi, then echo.WrapHandler.
	// Mount registers routes under /auth/, and the request path is unchanged.
	e.Any("/auth/*", echo.WrapHandler(app.Routes))

	localDomain := cfg.BaseDomain == "localhost" || strings.HasSuffix(cfg.BaseDomain, ".localhost")
	mcpHandler := mcp.Handler(app.Auth, store, cfg.BaseURL, cfg.BaseDomain, !localDomain)
	protected := app.Auth.RequireAbility(auth.AbilityPublish)(mcpHandler)
	e.Any("/mcp", echo.WrapHandler(protected))

	return &appHandler{server: s}, nil
}

// appHandler serves site files by Host before the apex admin and MCP routes.
type appHandler struct {
	server *server
}

func (h *appHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	defer func() {
		if rec := recover(); rec != nil {
			slog.Error("panic", "err", rec)
			if !headerWritten(w) {
				http.Error(w, "internal error", http.StatusInternalServerError)
			}
		}
	}()
	if slug, ok := sites.SlugFromHost(r.Host, h.server.baseDomain); ok {
		h.server.sites.Serve(w, r, slug)
		return
	}
	h.server.echo.ServeHTTP(w, r)
}

func headerWritten(w http.ResponseWriter) bool {
	if rw, ok := w.(interface{ Written() bool }); ok {
		return rw.Written()
	}
	return false
}

func (s *server) requireUser(next echo.HandlerFunc) echo.HandlerFunc {
	return func(c echo.Context) error {
		if _, ok := theauth.UserFromContext(c.Request().Context()); !ok {
			if c.Request().Header.Get("HX-Request") == "true" {
				c.Response().Header().Set("HX-Redirect", "/")
				return c.NoContent(http.StatusUnauthorized)
			}
			return c.Redirect(http.StatusSeeOther, "/")
		}
		c.Response().Header().Set("Cache-Control", "no-store")
		return next(c)
	}
}

func (s *server) home(c echo.Context) error {
	if _, ok := theauth.UserFromContext(c.Request().Context()); ok {
		return c.Redirect(http.StatusSeeOther, "/admin")
	}
	needs, err := s.needsSetup(c.Request().Context())
	if err != nil {
		return err
	}
	title := "Sign in"
	if needs {
		title = "Create the first account"
	}
	return s.render(c, http.StatusOK, "layout", viewData{
		Title:      title,
		View:       "login",
		NeedsSetup: needs,
		BaseURL:    s.cfg.BaseURL,
	})
}

func (s *server) signup(c echo.Context) error {
	if err := limitBody(c, 1<<16); err != nil {
		return err
	}
	rec, err := s.callAuth(c.Request(), http.MethodPost, "/auth/email-password/signup", map[string]string{
		"email":      strings.TrimSpace(c.FormValue("email")),
		"password":   c.FormValue("password"),
		"setupToken": c.FormValue("setup_token"),
	}, http.Header{"X-Setup-Token": []string{c.FormValue("setup_token")}})
	if err != nil {
		return err
	}
	return s.finishAuth(c, rec, true)
}

func (s *server) login(c echo.Context) error {
	if err := limitBody(c, 1<<16); err != nil {
		return err
	}
	rec, err := s.callAuth(c.Request(), http.MethodPost, "/auth/email-password/signin", map[string]string{
		"email":    strings.TrimSpace(c.FormValue("email")),
		"password": c.FormValue("password"),
	}, nil)
	if err != nil {
		return err
	}
	return s.finishAuth(c, rec, false)
}

func (s *server) logout(c echo.Context) error {
	rec, err := s.callAuth(c.Request(), http.MethodDelete, "/auth/sessions/current", nil, nil)
	if err != nil {
		return err
	}
	copyCookies(c.Response(), rec.Result())
	return c.Redirect(http.StatusSeeOther, "/")
}

func (s *server) finishAuth(c echo.Context, rec *httptest.ResponseRecorder, setup bool) error {
	copyCookies(c.Response(), rec.Result())
	if rec.Code/100 == 2 {
		return c.Redirect(http.StatusSeeOther, "/admin")
	}
	needs := setup
	if !setup {
		var err error
		needs, err = s.needsSetup(c.Request().Context())
		if err != nil {
			return err
		}
	}
	title := "Sign in"
	if needs {
		title = "Create the first account"
	}
	return s.render(c, rec.Code, "layout", viewData{
		Title:      title,
		View:       "login",
		NeedsSetup: needs,
		Error:      authError(rec.Body.Bytes()),
		BaseURL:    s.cfg.BaseURL,
	})
}

func (s *server) admin(c echo.Context) error {
	data, err := s.adminData(c)
	if err != nil {
		return err
	}
	if id := c.QueryParam("issued"); id != "" {
		if user, ok := theauth.UserFromContext(c.Request().Context()); ok {
			data.NewToken = s.flashes.take(id, user.ID.String())
		}
	}
	return s.render(c, http.StatusOK, "layout", data)
}

func (s *server) createSite(c echo.Context) error {
	user, _ := theauth.UserFromContext(c.Request().Context())
	if _, err := s.sites.Create(c.Request().Context(), user.ID.String()); err != nil {
		return err
	}
	if fragment(c) {
		data, err := s.adminData(c)
		if err != nil {
			return err
		}
		return s.render(c, http.StatusOK, "site-list", data)
	}
	return c.Redirect(http.StatusSeeOther, "/admin")
}

func (s *server) sitePage(c echo.Context) error {
	data, err := s.siteData(c, c.Param("slug"))
	if err != nil {
		if errors.Is(err, sites.ErrNotFound) {
			return c.String(http.StatusNotFound, "site not found")
		}
		return err
	}
	return s.render(c, http.StatusOK, "layout", data)
}

func (s *server) upload(c echo.Context) error {
	if err := limitBody(c, sites.MaxFileBytes+(1<<20)); err != nil {
		return s.siteError(c, c.Param("slug"), "upload is too large")
	}
	user, _ := theauth.UserFromContext(c.Request().Context())
	slug := c.Param("slug")
	fh, err := c.FormFile("file")
	if err != nil {
		return s.siteError(c, slug, "choose a file")
	}
	rel := strings.TrimSpace(c.FormValue("path"))
	if rel == "" {
		rel = fh.Filename
	}
	f, err := fh.Open()
	if err != nil {
		return err
	}
	defer f.Close()
	if err := s.sites.PutFile(c.Request().Context(), user.ID.String(), slug, rel, f); err != nil {
		return s.siteError(c, slug, fileErr(err))
	}
	if fragment(c) {
		data, err := s.siteData(c, slug)
		if err != nil {
			return err
		}
		return s.render(c, http.StatusOK, "file-list", data)
	}
	return c.Redirect(http.StatusSeeOther, "/admin/sites/"+url.PathEscape(slug))
}

func (s *server) deleteFile(c echo.Context) error {
	user, _ := theauth.UserFromContext(c.Request().Context())
	slug := c.Param("slug")
	if err := s.sites.DeleteFile(c.Request().Context(), user.ID.String(), slug, c.FormValue("path")); err != nil {
		return s.siteError(c, slug, fileErr(err))
	}
	if fragment(c) {
		data, err := s.siteData(c, slug)
		if err != nil {
			return err
		}
		return s.render(c, http.StatusOK, "file-list", data)
	}
	return c.Redirect(http.StatusSeeOther, "/admin/sites/"+url.PathEscape(slug))
}

func (s *server) createToken(c echo.Context) error {
	user, _ := theauth.UserFromContext(c.Request().Context())
	name := strings.TrimSpace(c.FormValue("name"))
	if name == "" {
		name = "agent"
	}
	secret, _, err := s.auth.Auth.MintAPIToken(c.Request().Context(), theauth.MintAPITokenInput{
		OwnerID:   user.ID,
		OwnerKind: theauth.OwnerKindUser,
		Name:      name,
		Abilities: []string{auth.AbilityPublish},
		TTL:       90 * 24 * time.Hour,
	})
	if err != nil {
		data, derr := s.adminData(c)
		if derr != nil {
			return derr
		}
		data.Error = err.Error()
		return s.render(c, http.StatusBadRequest, "layout", data)
	}
	id := s.flashes.put(user.ID.String(), secret)
	return c.Redirect(http.StatusSeeOther, "/admin?issued="+id)
}

func (s *server) siteError(c echo.Context, slug, msg string) error {
	data, err := s.siteData(c, slug)
	if err != nil {
		return c.String(http.StatusBadRequest, msg)
	}
	data.Error = msg
	if fragment(c) {
		return s.render(c, http.StatusUnprocessableEntity, "file-list", data)
	}
	return s.render(c, http.StatusUnprocessableEntity, "layout", data)
}

func (s *server) adminData(c echo.Context) (viewData, error) {
	user, _ := theauth.UserFromContext(c.Request().Context())
	list, err := s.sites.ListByOwner(c.Request().Context(), user.ID.String())
	if err != nil {
		return viewData{}, err
	}
	rows := make([]siteRow, 0, len(list))
	for _, site := range list {
		rows = append(rows, siteRow{
			Slug:    site.Slug,
			URL:     sites.PublicURL(s.cfg.BaseURL, s.baseDomain, site.Slug),
			Created: site.CreatedAt.Format("2006-01-02 15:04 UTC"),
		})
	}
	tokens, err := s.auth.Auth.ListAPITokens(c.Request().Context(), user.ID)
	if err != nil {
		return viewData{}, err
	}
	now := time.Now()
	var tokenRows []tokenRow
	for _, tok := range tokens {
		if !tok.Usable(now) {
			continue
		}
		exp := ""
		if tok.ExpiresAt != nil {
			exp = tok.ExpiresAt.UTC().Format("2006-01-02")
		}
		tokenRows = append(tokenRows, tokenRow{Name: tok.Name, Hint: tok.Hint, Expires: exp})
	}
	if tokenRows == nil {
		tokenRows = []tokenRow{}
	}
	return viewData{
		Title:   "Sites",
		View:    "admin",
		Email:   user.Email,
		BaseURL: s.cfg.BaseURL,
		MCPURL:  strings.TrimRight(s.cfg.BaseURL, "/") + "/mcp",
		Sites:   rows,
		Tokens:  tokenRows,
	}, nil
}

func (s *server) siteData(c echo.Context, slug string) (viewData, error) {
	user, _ := theauth.UserFromContext(c.Request().Context())
	site, err := s.sites.GetOwned(c.Request().Context(), user.ID.String(), slug)
	if err != nil {
		return viewData{}, err
	}
	files, err := s.sites.ListFiles(c.Request().Context(), user.ID.String(), slug)
	if err != nil {
		return viewData{}, err
	}
	fileRows := make([]fileRow, 0, len(files))
	for _, f := range files {
		fileRows = append(fileRows, fileRow{Path: f.Path, Size: f.Size})
	}
	pub := sites.PublicURL(s.cfg.BaseURL, s.baseDomain, site.Slug)
	host := site.Slug + "." + s.baseDomain
	return viewData{
		Title:   site.Slug,
		View:    "site",
		Email:   user.Email,
		BaseURL: s.cfg.BaseURL,
		HasSite: true,
		Site: siteRow{
			Slug:    site.Slug,
			URL:     pub,
			Created: site.CreatedAt.Format("2006-01-02 15:04 UTC"),
		},
		Files: fileRows,
		Curl:  "curl -H " + strconv.Quote("Host: "+host) + " " + strings.TrimRight(s.cfg.BaseURL, "/") + "/",
	}, nil
}

func (s *server) needsSetup(ctx context.Context) (bool, error) {
	n, err := s.auth.Auth.UserCount(ctx)
	if err != nil {
		return false, err
	}
	return n == 0, nil
}

func (s *server) render(c echo.Context, status int, name string, data viewData) error {
	var buf bytes.Buffer
	if err := s.tmpl.ExecuteTemplate(&buf, name, data); err != nil {
		return err
	}
	return c.HTML(status, buf.String())
}

func (s *server) callAuth(r *http.Request, method, path string, body any, extra http.Header) (*httptest.ResponseRecorder, error) {
	var buf bytes.Buffer
	if body != nil {
		if err := json.NewEncoder(&buf).Encode(body); err != nil {
			return nil, err
		}
	}
	req := httptest.NewRequest(method, path, &buf)
	req.Header.Set("Content-Type", "application/json")
	req.RemoteAddr = r.RemoteAddr
	if req.RemoteAddr == "" {
		req.RemoteAddr = "127.0.0.1:1"
	}
	req.Header.Set("User-Agent", r.UserAgent())
	if cookie := r.Header.Get("Cookie"); cookie != "" {
		req.Header.Set("Cookie", cookie)
		if s.origin != "" {
			req.Header.Set("Origin", s.origin)
		}
	}
	for k, vs := range extra {
		for _, v := range vs {
			req.Header.Set(k, v)
		}
	}
	rec := httptest.NewRecorder()
	s.auth.Routes.ServeHTTP(rec, req)
	return rec, nil
}

func limitBody(c echo.Context, n int64) error {
	c.Request().Body = http.MaxBytesReader(c.Response(), c.Request().Body, n)
	return nil
}

func fragment(c echo.Context) bool {
	return c.Request().Header.Get("HX-Request") == "true"
}

func copyCookies(dst http.ResponseWriter, src *http.Response) {
	for _, c := range src.Cookies() {
		http.SetCookie(dst, c)
	}
}

func authError(body []byte) string {
	var msg struct {
		Detail  string `json:"detail"`
		Message string `json:"message"`
		Title   string `json:"title"`
	}
	if json.Unmarshal(body, &msg) == nil {
		switch {
		case msg.Message != "":
			return msg.Message
		case msg.Detail != "":
			return msg.Detail
		case msg.Title != "":
			return msg.Title
		}
	}
	s := strings.TrimSpace(string(body))
	if s == "" {
		return "sign-in failed"
	}
	if len(s) > 300 {
		s = s[:300]
	}
	return s
}

func fileErr(err error) string {
	switch {
	case errors.Is(err, sites.ErrBadPath):
		return "that path is not allowed"
	case errors.Is(err, sites.ErrNotFound):
		return "file or site not found"
	case errors.Is(err, sites.ErrTooLarge):
		return "file is larger than 8 MiB"
	default:
		return "could not update the file"
	}
}

type viewData struct {
	Title      string
	View       string
	Email      string
	NeedsSetup bool
	Error      string
	BaseURL    string
	MCPURL     string
	Sites      []siteRow
	Site       siteRow
	HasSite    bool
	Files      []fileRow
	Tokens     []tokenRow
	NewToken   string
	Curl       string
}

type siteRow struct {
	Slug    string
	URL     string
	Created string
}

type fileRow struct {
	Path string
	Size int64
}

type tokenRow struct {
	Name    string
	Hint    string
	Expires string
}

type flashItem struct {
	UserID string
	Secret string
}

type flashBox struct {
	mu sync.Mutex
	m  map[string]flashItem
}

func newFlashBox() *flashBox {
	return &flashBox{m: map[string]flashItem{}}
}

func (f *flashBox) put(userID, secret string) string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	id := hex.EncodeToString(b[:])
	f.mu.Lock()
	f.m[id] = flashItem{UserID: userID, Secret: secret}
	f.mu.Unlock()
	return id
}

func (f *flashBox) take(id, userID string) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	item, ok := f.m[id]
	if !ok || item.UserID != userID {
		return ""
	}
	delete(f.m, id)
	return item.Secret
}
