package mcp

import (
	"context"
	"encoding/base64"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"time"

	"github.com/caidesen/mcp-static-deploy/internal/sites"
	"github.com/glincker/theauth-go/mcpresource"
	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// Handler is the Streamable HTTP MCP endpoint. Callers must wrap it with
// the OAuth bearer validator so anonymous requests never reach the tools.
func Handler(st *sites.Store, baseURL, baseDomain string, disableLocalhost bool, tokens *mcpresource.Validator) http.Handler {
	server := mcpsdk.NewServer(&mcpsdk.Implementation{
		Name:    "static-publish",
		Version: "0.1.0",
	}, nil)
	svc := &tools{tokens: tokens, sites: st, baseURL: baseURL, baseDomain: baseDomain}
	mcpsdk.AddTool(server, &mcpsdk.Tool{
		Name:        "create_site",
		Description: "Create a static site you own. An optional note is a short description of what the site contains. Returns a random slug, the note, and the public URL.",
	}, svc.createSite)
	mcpsdk.AddTool(server, &mcpsdk.Tool{
		Name:        "put_file",
		Description: "Create or replace a file in a site you own. path is relative to the site root. Absolute paths and '..' are rejected.",
	}, svc.putFile)
	mcpsdk.AddTool(server, &mcpsdk.Tool{
		Name:        "delete_file",
		Description: "Delete one file from a site you own.",
	}, svc.deleteFile)
	mcpsdk.AddTool(server, &mcpsdk.Tool{
		Name:        "list_files",
		Description: "List files in a site you own by reading its directory.",
	}, svc.listFiles)

	return mcpsdk.NewStreamableHTTPHandler(func(*http.Request) *mcpsdk.Server {
		return server
	}, &mcpsdk.StreamableHTTPOptions{
		// A local reverse proxy connects to 127.0.0.1 while Host is the public
		// apex. The SDK would otherwise reject that. Localhost base domains
		// keep the check, because their Host is a loopback name.
		DisableLocalhostProtection: disableLocalhost,
		SessionTimeout:             30 * time.Minute,
		MaxRequestBodyBytes:        sites.MaxFileBytes + (1 << 20),
		Logger:                     slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
}

type tools struct {
	tokens     *mcpresource.Validator
	sites      *sites.Store
	baseURL    string
	baseDomain string
}

type createIn struct {
	Note string `json:"note,omitempty" jsonschema:"optional short description of what the site contains"`
}

type siteOut struct {
	Slug string `json:"slug"`
	URL  string `json:"url"`
	Note string `json:"note,omitempty"`
}

type putIn struct {
	Slug     string `json:"slug" jsonschema:"site slug"`
	Path     string `json:"path" jsonschema:"relative file path, for example index.html or css/app.css"`
	Content  string `json:"content" jsonschema:"file contents as UTF-8 text, or base64 when encoding is base64"`
	Encoding string `json:"encoding,omitempty" jsonschema:"utf-8 (default) or base64"`
}

type pathIn struct {
	Slug string `json:"slug" jsonschema:"site slug"`
	Path string `json:"path" jsonschema:"relative file path"`
}

type okOut struct {
	OK   bool   `json:"ok"`
	Path string `json:"path"`
}

type listIn struct {
	Slug string `json:"slug" jsonschema:"site slug"`
}

type fileOut struct {
	Path string `json:"path"`
	Size int64  `json:"size"`
}

type listOut struct {
	Files []fileOut `json:"files"`
}

func (t *tools) createSite(ctx context.Context, req *mcpsdk.CallToolRequest, in createIn) (*mcpsdk.CallToolResult, siteOut, error) {
	owner, err := t.owner(ctx, req)
	if err != nil {
		return nil, siteOut{}, err
	}
	site, err := t.sites.Create(ctx, owner, in.Note)
	if err != nil {
		return nil, siteOut{}, err
	}
	return nil, siteOut{Slug: site.Slug, URL: sites.PublicURL(t.baseURL, t.baseDomain, site.Slug), Note: site.Note}, nil
}

func (t *tools) putFile(ctx context.Context, req *mcpsdk.CallToolRequest, in putIn) (*mcpsdk.CallToolResult, okOut, error) {
	owner, err := t.owner(ctx, req)
	if err != nil {
		return nil, okOut{}, err
	}
	body, err := decodeContent(in.Content, in.Encoding)
	if err != nil {
		return nil, okOut{}, err
	}
	if err := t.sites.PutFile(ctx, owner, in.Slug, in.Path, strings.NewReader(string(body))); err != nil {
		return nil, okOut{}, err
	}
	cleaned, err := sites.CleanRelPath(in.Path)
	if err != nil {
		return nil, okOut{}, err
	}
	return nil, okOut{OK: true, Path: cleaned}, nil
}

func (t *tools) deleteFile(ctx context.Context, req *mcpsdk.CallToolRequest, in pathIn) (*mcpsdk.CallToolResult, okOut, error) {
	owner, err := t.owner(ctx, req)
	if err != nil {
		return nil, okOut{}, err
	}
	if err := t.sites.DeleteFile(ctx, owner, in.Slug, in.Path); err != nil {
		return nil, okOut{}, err
	}
	cleaned, err := sites.CleanRelPath(in.Path)
	if err != nil {
		return nil, okOut{}, err
	}
	return nil, okOut{OK: true, Path: cleaned}, nil
}

func (t *tools) listFiles(ctx context.Context, req *mcpsdk.CallToolRequest, in listIn) (*mcpsdk.CallToolResult, listOut, error) {
	owner, err := t.owner(ctx, req)
	if err != nil {
		return nil, listOut{}, err
	}
	files, err := t.sites.ListFiles(ctx, owner, in.Slug)
	if err != nil {
		return nil, listOut{}, err
	}
	out := listOut{Files: make([]fileOut, 0, len(files))}
	for _, f := range files {
		out.Files = append(out.Files, fileOut{Path: f.Path, Size: f.Size})
	}
	return nil, out, nil
}

// owner re-reads the bearer token on this HTTP request. The middleware already
// rejected anonymous callers; this binds the write to the token subject
// rather than to whoever opened the MCP session.
func (t *tools) owner(ctx context.Context, req *mcpsdk.CallToolRequest) (string, error) {
	if req != nil && req.Extra != nil && len(req.Extra.Header) > 0 && t.tokens != nil {
		httpReq := &http.Request{
			Method:     http.MethodPost,
			URL:        &url.URL{Path: "/mcp"},
			Header:     req.Extra.Header.Clone(),
			Host:       req.Extra.Header.Get("Host"),
			RemoteAddr: "127.0.0.1:1",
		}
		rec := httptest.NewRecorder()
		var subject string
		t.tokens.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if p, ok := mcpresource.PrincipalFromContext(r.Context()); ok {
				subject = p.Subject
			}
			w.WriteHeader(http.StatusNoContent)
		})).ServeHTTP(rec, httpReq)
		if subject != "" && rec.Code == http.StatusNoContent {
			return subject, nil
		}
		return "", errors.New("authentication required")
	}
	if p, ok := mcpresource.PrincipalFromContext(ctx); ok && p.Subject != "" {
		return p.Subject, nil
	}
	return "", errors.New("authentication required")
}

func decodeContent(content, encoding string) ([]byte, error) {
	switch strings.ToLower(strings.TrimSpace(encoding)) {
	case "", "utf-8", "utf8":
		return []byte(content), nil
	case "base64":
		raw := strings.TrimSpace(content)
		if b, err := base64.StdEncoding.DecodeString(raw); err == nil {
			return b, nil
		}
		if b, err := base64.RawStdEncoding.DecodeString(raw); err == nil {
			return b, nil
		}
		return nil, errors.New("content is not valid base64")
	default:
		return nil, errors.New("encoding must be utf-8 or base64")
	}
}
