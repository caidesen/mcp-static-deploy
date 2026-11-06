package web

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"

	"github.com/labstack/echo/v4"
)

const maxRegistrationRedirects = 8

// discoveryRoute serves authorization-server metadata with CORS. A browser
// client reads registration_endpoint from this response.
func (s *server) discoveryRoute(c echo.Context) error {
	w, r := c.Response(), c.Request()
	setCORS(w.Header())
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusNoContent)
		return nil
	}
	s.auth.Routes.ServeHTTP(w, r)
	return nil
}

// oauthRoute forwards the authorization server, except registration, which a
// browser preflight and a multi-redirect client both have to get through.
func (s *server) oauthRoute(c echo.Context) error {
	w, r := c.Response(), c.Request()
	if r.URL.Path == "/oauth/register" {
		s.dynamicRegister(w, r)
		return nil
	}
	setCORS(w.Header())
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusNoContent)
		return nil
	}
	s.auth.Routes.ServeHTTP(w, r)
	return nil
}

func setCORS(h http.Header) {
	h.Set("Access-Control-Allow-Origin", "*")
	h.Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
	h.Set("Access-Control-Allow-Headers", "Content-Type, Authorization")
	h.Set("Access-Control-Max-Age", "600")
}

// dynamicRegister answers POST /oauth/register. theauth allows one redirect
// URI on an anonymous registration and rejects cursor://. Clients send several
// URIs, including that scheme, so the handler lets theauth mint the client
// for one acceptable URI and then stores the full set.
func (s *server) dynamicRegister(w http.ResponseWriter, r *http.Request) {
	setCORS(w.Header())
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if r.Method != http.MethodPost {
		s.auth.Routes.ServeHTTP(w, r)
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<16+1))
	if err != nil || len(body) > 1<<16 {
		http.Error(w, "request body is too large", http.StatusRequestEntityTooLarge)
		return
	}
	uris, ok := registrationRedirects(body)
	if !ok || theauthAcceptsRedirects(uris) || !redirectsAllowed(uris) {
		forwardRegister(w, r, s.auth.Routes, body)
		return
	}
	standIn := standInRedirect(uris)
	narrowed, err := withRedirects(body, []string{standIn})
	if err != nil {
		forwardRegister(w, r, s.auth.Routes, body)
		return
	}
	rec := httptest.NewRecorder()
	forwardRegister(rec, r, s.auth.Routes, narrowed)
	if rec.Code != http.StatusCreated {
		writeRecorded(w, rec, rec.Body.Bytes())
		return
	}
	var created struct {
		ClientID string `json:"client_id"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil || created.ClientID == "" {
		writeRecorded(w, rec, rec.Body.Bytes())
		return
	}
	encoded, err := json.Marshal(uris)
	if err != nil {
		_, _ = s.auth.DB.ExecContext(r.Context(), `DELETE FROM theauth_oauth_clients WHERE client_id = ?`, created.ClientID)
		http.Error(w, "registration failed", http.StatusInternalServerError)
		return
	}
	res, err := s.auth.DB.ExecContext(r.Context(),
		`UPDATE theauth_oauth_clients SET redirect_uris = ? WHERE client_id = ?`,
		string(encoded), created.ClientID)
	if err != nil {
		_, _ = s.auth.DB.ExecContext(r.Context(), `DELETE FROM theauth_oauth_clients WHERE client_id = ?`, created.ClientID)
		http.Error(w, "registration failed", http.StatusInternalServerError)
		return
	}
	n, _ := res.RowsAffected()
	if n != 1 {
		_, _ = s.auth.DB.ExecContext(r.Context(), `DELETE FROM theauth_oauth_clients WHERE client_id = ?`, created.ClientID)
		http.Error(w, "registration failed", http.StatusInternalServerError)
		return
	}
	var doc map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &doc); err != nil {
		http.Error(w, "registration failed", http.StatusInternalServerError)
		return
	}
	doc["redirect_uris"] = uris
	out, err := json.Marshal(doc)
	if err != nil {
		http.Error(w, "registration failed", http.StatusInternalServerError)
		return
	}
	writeRecorded(w, rec, out)
}

func forwardRegister(w http.ResponseWriter, r *http.Request, h http.Handler, body []byte) {
	req := r.Clone(r.Context())
	req.Body = io.NopCloser(bytes.NewReader(body))
	req.ContentLength = int64(len(body))
	req.GetBody = func() (io.ReadCloser, error) {
		return io.NopCloser(bytes.NewReader(body)), nil
	}
	h.ServeHTTP(w, req)
}

func writeRecorded(w http.ResponseWriter, rec *httptest.ResponseRecorder, body []byte) {
	for k, vals := range rec.Header() {
		w.Header()[k] = append([]string(nil), vals...)
	}
	setCORS(w.Header())
	w.Header().Del("Content-Length")
	code := rec.Code
	if code == 0 {
		code = http.StatusOK
	}
	w.WriteHeader(code)
	_, _ = w.Write(body)
}

func registrationRedirects(body []byte) ([]string, bool) {
	var doc struct {
		RedirectURIs []string `json:"redirect_uris"`
	}
	if err := json.Unmarshal(body, &doc); err != nil || doc.RedirectURIs == nil {
		return nil, false
	}
	return doc.RedirectURIs, true
}

func withRedirects(body []byte, uris []string) ([]byte, error) {
	var doc map[string]any
	if err := json.Unmarshal(body, &doc); err != nil {
		return nil, err
	}
	doc["redirect_uris"] = uris
	return json.Marshal(doc)
}

// theauthAcceptsRedirects reports whether anonymous registration will store
// the URIs unchanged. One https, loopback http, or reverse-DNS URI passes.
// cursor:// and a list do not.
func theauthAcceptsRedirects(uris []string) bool {
	if len(uris) != 1 {
		return false
	}
	u, err := url.Parse(uris[0])
	if err != nil {
		return false
	}
	if strings.EqualFold(u.Scheme, "cursor") {
		return false
	}
	return redirectAllowed(uris[0])
}

func redirectsAllowed(uris []string) bool {
	if len(uris) == 0 || len(uris) > maxRegistrationRedirects {
		return false
	}
	for _, raw := range uris {
		if !redirectAllowed(raw) {
			return false
		}
	}
	return true
}

func standInRedirect(uris []string) string {
	for _, raw := range uris {
		u, err := url.Parse(raw)
		if err != nil || strings.EqualFold(u.Scheme, "cursor") {
			continue
		}
		if redirectAllowed(raw) {
			return raw
		}
	}
	return "http://127.0.0.1/callback"
}

func redirectAllowed(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme == "" || u.Fragment != "" || u.User != nil {
		return false
	}
	switch strings.ToLower(u.Scheme) {
	case "https":
		return u.Hostname() != ""
	case "http":
		switch strings.ToLower(u.Hostname()) {
		case "localhost", "127.0.0.1", "::1":
			return true
		}
		return false
	case "cursor":
		return u.Hostname() != "" || u.Path != ""
	case "javascript", "data", "file", "vbscript", "blob", "about", "ftp":
		return false
	default:
		return reverseDNSScheme(u.Scheme)
	}
}

func reverseDNSScheme(scheme string) bool {
	scheme = strings.ToLower(scheme)
	labels := strings.Split(scheme, ".")
	if len(labels) < 2 {
		return false
	}
	for _, label := range labels {
		if label == "" {
			return false
		}
		for _, r := range label {
			if !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '-') {
				return false
			}
		}
	}
	return true
}
