package web

import (
	"bytes"
	"context"
	"embed"
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
	"unicode/utf8"

	"github.com/caidesen/mcp-static-deploy/internal/auth"
	"github.com/caidesen/mcp-static-deploy/internal/mcp"
	"github.com/caidesen/mcp-static-deploy/internal/sites"
	"github.com/glincker/theauth-go/mcpresource"
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
	e.GET("/lang", s.setLang)
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

	// theauth mounts session routes under /auth/ and the authorization server
	// at /.well-known and /oauth. Echo forwards those paths unchanged.
	routes := echo.WrapHandler(app.Routes)
	e.Any("/auth/*", routes)
	e.Any("/oauth/*", routes)
	e.Any("/.well-known/*", routes)

	issuer := strings.TrimRight(cfg.BaseURL, "/")
	tokens := mcpresource.New(issuer+"/mcp", mcpresource.WithJWKS(issuer+"/oauth/jwks"))
	localDomain := cfg.BaseDomain == "localhost" || strings.HasSuffix(cfg.BaseDomain, ".localhost")
	mcpHandler := mcp.Handler(store, cfg.BaseURL, cfg.BaseDomain, !localDomain, tokens)
	e.Any("/mcp", echo.WrapHandler(tokens.Middleware(mcpHandler)))

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
				http.Error(w, text(negotiateLang(r), "err_internal"), http.StatusInternalServerError)
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
	next := safeOAuthNext(c.QueryParam("next"))
	if _, ok := theauth.UserFromContext(c.Request().Context()); ok {
		if next != "" {
			return c.Redirect(http.StatusSeeOther, next)
		}
		return c.Redirect(http.StatusSeeOther, "/admin")
	}
	needs, err := s.needsSetup(c.Request().Context())
	if err != nil {
		return err
	}
	return s.render(c, http.StatusOK, "layout", viewData{
		View:       "login",
		NeedsSetup: needs,
		BaseURL:    s.cfg.BaseURL,
		Next:       next,
	})
}

func (s *server) setLang(c echo.Context) error {
	if lang, ok := knownLang(c.QueryParam("set")); ok {
		c.SetCookie(&http.Cookie{
			Name:     langCookie,
			Value:    lang,
			Path:     "/",
			MaxAge:   60 * 60 * 24 * 365,
			HttpOnly: true,
			SameSite: http.SameSiteLaxMode,
			Secure:   c.Request().TLS != nil,
		})
	}
	return c.Redirect(http.StatusSeeOther, langRedirect(c.Request()))
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
	next := safeOAuthNext(c.FormValue("next"))
	if rec.Code/100 == 2 {
		if next != "" {
			return c.Redirect(http.StatusSeeOther, next)
		}
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
	return s.render(c, rec.Code, "layout", viewData{
		View:       "login",
		NeedsSetup: needs,
		Error:      authErrorKey(rec.Body.Bytes()),
		BaseURL:    s.cfg.BaseURL,
		Next:       next,
	})
}

func (s *server) admin(c echo.Context) error {
	data, err := s.adminData(c)
	if err != nil {
		return err
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
		return s.siteError(c, c.Param("slug"), "err_upload_too_large")
	}
	user, _ := theauth.UserFromContext(c.Request().Context())
	slug := c.Param("slug")
	fh, err := c.FormFile("file")
	if err != nil {
		return s.siteError(c, slug, "err_choose_file")
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
		return s.siteError(c, slug, fileErrKey(err))
	}
	if fragment(c) {
		data, err := s.siteData(c, slug)
		if err != nil {
			return err
		}
		data.Fragment = true
		return s.render(c, http.StatusOK, "file-list", data)
	}
	return c.Redirect(http.StatusSeeOther, "/admin/sites/"+url.PathEscape(slug))
}

func (s *server) deleteFile(c echo.Context) error {
	user, _ := theauth.UserFromContext(c.Request().Context())
	slug := c.Param("slug")
	if err := s.sites.DeleteFile(c.Request().Context(), user.ID.String(), slug, c.FormValue("path")); err != nil {
		return s.siteError(c, slug, fileErrKey(err))
	}
	if fragment(c) {
		data, err := s.siteData(c, slug)
		if err != nil {
			return err
		}
		data.Fragment = true
		return s.render(c, http.StatusOK, "file-list", data)
	}
	return c.Redirect(http.StatusSeeOther, "/admin/sites/"+url.PathEscape(slug))
}

func (s *server) siteError(c echo.Context, slug, key string) error {
	data, err := s.siteData(c, slug)
	if err != nil {
		return c.String(http.StatusBadRequest, text(negotiateLang(c.Request()), key))
	}
	data.Error = key
	if fragment(c) {
		data.Fragment = true
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
		row, err := s.siteCard(c, site)
		if err != nil {
			return viewData{}, err
		}
		rows = append(rows, row)
	}
	return viewData{
		View:    "admin",
		Email:   user.Email,
		BaseURL: s.cfg.BaseURL,
		Sites:   rows,
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
	host := site.Slug + "." + s.baseDomain
	card, err := s.siteCard(c, site)
	if err != nil {
		return viewData{}, err
	}
	return viewData{
		View:    "site",
		Email:   user.Email,
		BaseURL: s.cfg.BaseURL,
		HasSite: true,
		Site:    card,
		Files:   fileRows,
		Curl:    "curl -H " + strconv.Quote("Host: "+host) + " " + strings.TrimRight(s.cfg.BaseURL, "/") + "/",
	}, nil
}

func (s *server) siteCard(c echo.Context, site sites.Site) (siteRow, error) {
	files, err := s.sites.ListFiles(c.Request().Context(), site.OwnerUserID, site.Slug)
	if err != nil {
		return siteRow{}, err
	}
	lang := negotiateLang(c.Request())
	pub := sites.PublicURL(s.cfg.BaseURL, s.baseDomain, site.Slug)
	return siteRow{
		Slug:  site.Slug,
		URL:   pub,
		Host:  publicHost(pub),
		Files: len(files),
		When:  relativeTime(lang, site.CreatedAt),
		Mark:  slugMark(site.Slug),
		Icon:  iconClass(site.Slug),
		Count: fileCountText(lang, len(files)),
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
	data.Lang = negotiateLang(c.Request())
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

func fileErrKey(err error) string {
	switch {
	case errors.Is(err, sites.ErrBadPath):
		return "err_bad_path"
	case errors.Is(err, sites.ErrNotFound):
		return "err_not_found"
	case errors.Is(err, sites.ErrTooLarge):
		return "err_too_large"
	default:
		return "err_update_file"
	}
}

type viewData struct {
	Lang       string
	View       string
	Email      string
	NeedsSetup bool
	Error      string
	BaseURL    string
	Next       string
	Sites      []siteRow
	Site       siteRow
	HasSite    bool
	Files      []fileRow
	Curl       string
	Fragment   bool
}

func (v viewData) Initial() string {
	email := strings.TrimSpace(v.Email)
	if email == "" {
		return "?"
	}
	r, _ := utf8.DecodeRuneInString(email)
	if r == utf8.RuneError {
		return "?"
	}
	return strings.ToUpper(string(r))
}

type siteRow struct {
	Slug  string
	URL   string
	Host  string
	Files int
	When  string
	Mark  string
	Icon  string
	Count string
}

func publicHost(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return raw
	}
	return u.Host
}

func slugMark(slug string) string {
	r, _ := utf8.DecodeRuneInString(slug)
	if r == utf8.RuneError {
		return "?"
	}
	return strings.ToUpper(string(r))
}

func iconClass(slug string) string {
	if slug == "" {
		return "icon-0"
	}
	return "icon-" + strconv.Itoa(int(slug[0])%6)
}

type fileRow struct {
	Path string
	Size int64
}

// safeOAuthNext allows the browser to resume /oauth/authorize after sign-in.
// Anything else, including other hosts, is dropped.
func safeOAuthNext(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" || strings.ContainsAny(raw, "\\\r\n") {
		return ""
	}
	u, err := url.Parse(raw)
	if err != nil || u.IsAbs() || u.Host != "" || u.User != nil {
		return ""
	}
	if !strings.HasPrefix(u.Path, "/oauth/") || strings.Contains(u.Path, "..") || strings.HasPrefix(u.Path, "//") {
		return ""
	}
	if u.RawQuery == "" {
		return u.Path
	}
	return u.Path + "?" + u.RawQuery
}
