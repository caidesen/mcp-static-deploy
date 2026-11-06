package web

import (
	"bytes"
	"context"
	"embed"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path"
	"strconv"
	"strings"

	"github.com/caidesen/mcp-static-deploy/internal/auth"
	"github.com/caidesen/mcp-static-deploy/internal/mcp"
	"github.com/caidesen/mcp-static-deploy/internal/sites"
	"github.com/glincker/theauth-go/mcpresource"
	"github.com/glincker/theauth-go/v2"
	"github.com/labstack/echo/v4"
	echomw "github.com/labstack/echo/v4/middleware"
)

//go:embed dist
var distFS embed.FS

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
		slog.Warn("baseDomain is empty; public sites are not served until config.json sets baseDomain")
	}
	cfg.BaseURL = strings.TrimSpace(cfg.BaseURL)
	if cfg.BaseURL == "" {
		cfg.BaseURL = "http://127.0.0.1:8080"
		slog.Warn("baseUrl is empty; using a loopback stand-in until config.json sets baseUrl", "baseUrl", cfg.BaseURL)
	}
	u, err := url.Parse(cfg.BaseURL)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return nil, errors.New("baseUrl must be an absolute http or https URL")
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
	dist       fs.FS
	baseDomain string
}

func newServer(cfg Config, app *auth.App, store *sites.Store) (http.Handler, error) {
	dist, err := fs.Sub(distFS, "dist")
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
		dist:       dist,
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

	e.GET("/", s.page)
	e.GET("/admin", s.page)
	e.GET("/admin/sites/:slug", s.page)
	e.GET("/assets/*", s.asset)
	e.GET("/lang", s.setLang)

	api := e.Group("/api")
	api.GET("/session", s.session)
	api.POST("/signup", s.signup)
	api.POST("/login", s.login)
	api.POST("/logout", s.logout)
	authed := api.Group("", s.requireAPI)
	authed.GET("/sites", s.listSites)
	authed.POST("/sites", s.createSite)
	authed.GET("/sites/:slug", s.getSite)
	authed.PATCH("/sites/:slug", s.updateSite)
	authed.POST("/sites/:slug/files", s.upload)
	authed.DELETE("/sites/:slug/files", s.deleteFile)

	// theauth mounts session routes under /auth/ and the authorization server
	// at /.well-known and /oauth. Echo forwards those paths unchanged.
	routes := echo.WrapHandler(app.Routes)
	e.Any("/auth/*", routes)
	e.Any("/oauth/*", routes)
	e.Any("/.well-known/*", routes)

	issuer := strings.TrimRight(cfg.BaseURL, "/")
	tokens := mcpresource.New(issuer+"/mcp", mcpresource.WithJWKS(issuer+"/oauth/jwks"))
	localDomain := cfg.BaseDomain == "" || cfg.BaseDomain == "localhost" || strings.HasSuffix(cfg.BaseDomain, ".localhost")
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

func (s *server) requireAPI(next echo.HandlerFunc) echo.HandlerFunc {
	return func(c echo.Context) error {
		if _, ok := theauth.UserFromContext(c.Request().Context()); !ok {
			return c.JSON(http.StatusUnauthorized, map[string]string{"error": "err_sign_in_failed"})
		}
		c.Response().Header().Set("Cache-Control", "no-store")
		return next(c)
	}
}

func (s *server) page(c echo.Context) error {
	next := safeOAuthNext(c.QueryParam("next"))
	_, signedIn := theauth.UserFromContext(c.Request().Context())
	if signedIn {
		if next != "" {
			return c.Redirect(http.StatusSeeOther, next)
		}
		if c.Request().URL.Path == "/" {
			return c.Redirect(http.StatusSeeOther, "/admin")
		}
	} else if strings.HasPrefix(c.Request().URL.Path, "/admin") {
		return c.Redirect(http.StatusSeeOther, "/")
	}
	b, err := fs.ReadFile(s.dist, "index.html")
	if err != nil {
		return err
	}
	c.Response().Header().Set("Cache-Control", "no-store")
	return c.HTMLBlob(http.StatusOK, b)
}

func (s *server) asset(c echo.Context) error {
	name := path.Clean(strings.TrimPrefix(c.Request().URL.Path, "/"))
	if name == "." || strings.HasPrefix(name, "../") || strings.Contains(name, "..") {
		return echo.ErrNotFound
	}
	f, err := s.dist.Open(name)
	if err != nil {
		return echo.ErrNotFound
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || info.IsDir() {
		return echo.ErrNotFound
	}
	c.Response().Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	http.ServeContent(c.Response(), c.Request(), info.Name(), info.ModTime(), f.(io.ReadSeeker))
	return nil
}

func (s *server) session(c echo.Context) error {
	lang := negotiateLang(c.Request())
	needs, err := s.needsSetup(c.Request().Context())
	if err != nil {
		return err
	}
	out := map[string]any{
		"authenticated": false,
		"email":         "",
		"needsSetup":    needs,
		"lang":          lang,
		"baseURL":       s.cfg.BaseURL,
		"messages":      messages[lang],
	}
	if user, ok := theauth.UserFromContext(c.Request().Context()); ok {
		out["authenticated"] = true
		out["email"] = user.Email
	}
	c.Response().Header().Set("Cache-Control", "no-store")
	return c.JSON(http.StatusOK, out)
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

type authBody struct {
	Email      string `json:"email"`
	Password   string `json:"password"`
	SetupToken string `json:"setupToken"`
	Next       string `json:"next"`
}

func (s *server) signup(c echo.Context) error {
	if err := limitBody(c, 1<<16); err != nil {
		return err
	}
	var body authBody
	if err := c.Bind(&body); err != nil {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "err_bad_request"})
	}
	rec, err := s.callAuth(c.Request(), http.MethodPost, "/auth/email-password/signup", map[string]string{
		"email":      strings.TrimSpace(body.Email),
		"password":   body.Password,
		"setupToken": body.SetupToken,
	}, http.Header{"X-Setup-Token": []string{body.SetupToken}})
	if err != nil {
		return err
	}
	return s.finishAuth(c, rec, body.Next)
}

func (s *server) login(c echo.Context) error {
	if err := limitBody(c, 1<<16); err != nil {
		return err
	}
	var body authBody
	if err := c.Bind(&body); err != nil {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "err_bad_request"})
	}
	rec, err := s.callAuth(c.Request(), http.MethodPost, "/auth/email-password/signin", map[string]string{
		"email":    strings.TrimSpace(body.Email),
		"password": body.Password,
	}, nil)
	if err != nil {
		return err
	}
	return s.finishAuth(c, rec, body.Next)
}

func (s *server) logout(c echo.Context) error {
	rec, err := s.callAuth(c.Request(), http.MethodDelete, "/auth/sessions/current", nil, nil)
	if err != nil {
		return err
	}
	copyCookies(c.Response(), rec.Result())
	return c.JSON(http.StatusOK, map[string]bool{"ok": true})
}

func (s *server) finishAuth(c echo.Context, rec *httptest.ResponseRecorder, next string) error {
	copyCookies(c.Response(), rec.Result())
	if rec.Code/100 == 2 {
		dest := safeOAuthNext(next)
		if dest == "" {
			dest = "/admin"
		}
		return c.JSON(http.StatusOK, map[string]any{"ok": true, "next": dest})
	}
	status := rec.Code
	if status < 400 {
		status = http.StatusBadRequest
	}
	return c.JSON(status, map[string]string{"error": authErrorKey(rec.Body.Bytes())})
}

func (s *server) listSites(c echo.Context) error {
	rows, err := s.siteRows(c)
	if err != nil {
		return err
	}
	return c.JSON(http.StatusOK, rows)
}

func (s *server) createSite(c echo.Context) error {
	user, _ := theauth.UserFromContext(c.Request().Context())
	note, ok := readNote(c)
	if !ok {
		return nil
	}
	site, err := s.sites.Create(c.Request().Context(), user.ID.String(), note)
	if err != nil {
		if status, key, handled := noteStatus(err); handled {
			return c.JSON(status, map[string]string{"error": key})
		}
		return err
	}
	row, err := s.siteCard(c, site)
	if err != nil {
		return err
	}
	return c.JSON(http.StatusCreated, row)
}

func (s *server) updateSite(c echo.Context) error {
	user, _ := theauth.UserFromContext(c.Request().Context())
	note, ok := readNote(c)
	if !ok {
		return nil
	}
	if _, err := s.sites.SetNote(c.Request().Context(), user.ID.String(), c.Param("slug"), note); err != nil {
		if status, key, handled := noteStatus(err); handled {
			return c.JSON(status, map[string]string{"error": key})
		}
		return err
	}
	detail, err := s.siteDetail(c, c.Param("slug"))
	if err != nil {
		return err
	}
	return c.JSON(http.StatusOK, detail)
}

func readNote(c echo.Context) (string, bool) {
	var body struct {
		Note string `json:"note"`
	}
	if c.Request().Body != nil && c.Request().ContentLength != 0 {
		if err := json.NewDecoder(c.Request().Body).Decode(&body); err != nil && !errors.Is(err, io.EOF) {
			_ = c.JSON(http.StatusBadRequest, map[string]string{"error": "err_bad_request"})
			return "", false
		}
	}
	note, err := sites.CleanNote(body.Note)
	if err != nil {
		status, key, _ := noteStatus(err)
		_ = c.JSON(status, map[string]string{"error": key})
		return "", false
	}
	return note, true
}

func noteStatus(err error) (int, string, bool) {
	switch {
	case errors.Is(err, sites.ErrNoteTooLong):
		return http.StatusUnprocessableEntity, "err_note_too_long", true
	case errors.Is(err, sites.ErrNotFound):
		return http.StatusNotFound, "err_not_found", true
	default:
		return http.StatusInternalServerError, "err_internal", false
	}
}

func (s *server) getSite(c echo.Context) error {
	detail, err := s.siteDetail(c, c.Param("slug"))
	if err != nil {
		if errors.Is(err, sites.ErrNotFound) {
			return c.JSON(http.StatusNotFound, map[string]string{"error": "err_not_found"})
		}
		return err
	}
	return c.JSON(http.StatusOK, detail)
}

func (s *server) upload(c echo.Context) error {
	if err := limitBody(c, sites.MaxFileBytes+(1<<20)); err != nil {
		return c.JSON(http.StatusRequestEntityTooLarge, map[string]string{"error": "err_upload_too_large"})
	}
	user, _ := theauth.UserFromContext(c.Request().Context())
	slug := c.Param("slug")
	fh, err := c.FormFile("file")
	if err != nil {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "err_choose_file"})
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
		return c.JSON(http.StatusUnprocessableEntity, map[string]string{"error": fileErrKey(err)})
	}
	detail, err := s.siteDetail(c, slug)
	if err != nil {
		return err
	}
	return c.JSON(http.StatusOK, detail)
}

func (s *server) deleteFile(c echo.Context) error {
	user, _ := theauth.UserFromContext(c.Request().Context())
	slug := c.Param("slug")
	if err := s.sites.DeleteFile(c.Request().Context(), user.ID.String(), slug, c.QueryParam("path")); err != nil {
		return c.JSON(http.StatusUnprocessableEntity, map[string]string{"error": fileErrKey(err)})
	}
	detail, err := s.siteDetail(c, slug)
	if err != nil {
		return err
	}
	return c.JSON(http.StatusOK, detail)
}

func (s *server) siteRows(c echo.Context) ([]siteJSON, error) {
	user, _ := theauth.UserFromContext(c.Request().Context())
	list, err := s.sites.ListByOwner(c.Request().Context(), user.ID.String())
	if err != nil {
		return nil, err
	}
	rows := make([]siteJSON, 0, len(list))
	for _, site := range list {
		row, err := s.siteCard(c, site)
		if err != nil {
			return nil, err
		}
		rows = append(rows, row)
	}
	return rows, nil
}

func (s *server) siteDetail(c echo.Context, slug string) (siteDetailJSON, error) {
	user, _ := theauth.UserFromContext(c.Request().Context())
	site, err := s.sites.GetOwned(c.Request().Context(), user.ID.String(), slug)
	if err != nil {
		return siteDetailJSON{}, err
	}
	files, err := s.sites.ListFiles(c.Request().Context(), user.ID.String(), slug)
	if err != nil {
		return siteDetailJSON{}, err
	}
	card, err := s.siteCard(c, site)
	if err != nil {
		return siteDetailJSON{}, err
	}
	fileRows := make([]fileJSON, 0, len(files))
	for _, f := range files {
		fileRows = append(fileRows, fileJSON{Path: f.Path, Size: f.Size})
	}
	host := site.Slug + "." + s.baseDomain
	return siteDetailJSON{
		Site:  card,
		Files: fileRows,
		Curl:  "curl -H " + strconv.Quote("Host: "+host) + " " + strings.TrimRight(s.cfg.BaseURL, "/") + "/",
	}, nil
}

func (s *server) siteCard(c echo.Context, site sites.Site) (siteJSON, error) {
	files, err := s.sites.ListFiles(c.Request().Context(), site.OwnerUserID, site.Slug)
	if err != nil {
		return siteJSON{}, err
	}
	pub := sites.PublicURL(s.cfg.BaseURL, s.baseDomain, site.Slug)
	return siteJSON{
		Slug:  site.Slug,
		URL:   pub,
		Host:  publicHost(pub),
		Note:  site.Note,
		Files: len(files),
		Count: fileCountText(negotiateLang(c.Request()), len(files)),
	}, nil
}

func (s *server) needsSetup(ctx context.Context) (bool, error) {
	n, err := s.auth.Auth.UserCount(ctx)
	if err != nil {
		return false, err
	}
	return n == 0, nil
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

type siteJSON struct {
	Slug  string `json:"slug"`
	URL   string `json:"url"`
	Host  string `json:"host"`
	Note  string `json:"note"`
	Files int    `json:"files"`
	Count string `json:"count"`
}

type fileJSON struct {
	Path string `json:"path"`
	Size int64  `json:"size"`
}

type siteDetailJSON struct {
	Site  siteJSON   `json:"site"`
	Files []fileJSON `json:"files"`
	Curl  string     `json:"curl"`
}

func publicHost(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return raw
	}
	return u.Host
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
