# mcp-static-deploy

使用 MCP 快速发布静态资源。

One Go binary serves three things:

- an HTMX admin UI on the apex host
- public static files when `Host` is `{slug}.{BASE_DOMAIN}`
- an MCP Streamable HTTP endpoint at `/mcp`

Reads of a published site are public. Creating a site and writing files require a signed-in user.

## Requirements

Go 1.26 or newer. `github.com/glincker/theauth-go/storage/sqlite` declares `go 1.26.0` because it uses `modernc.org/sqlite` (pure Go, no cgo).

```sh
CGO_ENABLED=0 go test ./...
CGO_ENABLED=0 go build -o server ./cmd/server
```

## Run

```sh
ADDR=:8080 \
BASE_URL=http://localhost:8080 \
BASE_DOMAIN=localhost \
DATA_DIR=./data \
SETUP_TOKEN=dev-setup-token \
./server
```

| Variable | Default | Role |
| --- | --- | --- |
| `ADDR` | `:8080` | Listen address |
| `BASE_URL` | `http://localhost:8080` | Apex URL. `http` leaves the session cookie non-Secure. |
| `BASE_DOMAIN` | none | Required. Public sites are served on `{slug}.{BASE_DOMAIN}`. The process refuses to start if this is empty. |
| `DATA_DIR` | `data` | SQLite file `app.db` and site files `sites/{slug}/` |
| `SETUP_TOKEN` | generated and logged once if unset | Required to create the first account |

Open `BASE_URL`. While no account exists, the page asks for email, a password of at least 12 characters, and the setup token. That calls `POST /auth/email-password/signup` with `X-Setup-Token`. After the first account exists, signup is closed and the same page is email/password sign-in.

The same routes are also mounted directly, matching the theauth Echo example (chi subrouter, then `echo.WrapHandler`, paths under `/auth/`):

```sh
curl -c jar -X POST "$BASE_URL/auth/email-password/signup" \
  -H "X-Setup-Token: $SETUP_TOKEN" -H 'Content-Type: application/json' \
  -d '{"email":"admin@example.com","password":"a long passphrase here"}'
```

## Publish from the browser

Sign in, choose **Create site**, and upload files. `index.html` is the front page. The page shows the public URL, for example `http://alksgi.localhost:8080/` when `BASE_DOMAIN=localhost`.

Replacing a file uploads the same path again. Delete removes that file only. The file list is the directory on disk; file rows are not stored in SQLite.

## Publish from MCP

On the admin page, create an API token. The secret is shown once and expires in 90 days. It has the `publish` ability.

`/mcp` is Streamable HTTP. Anonymous requests are rejected. A session cookie or `Authorization: Bearer <token>` is accepted (`RequireAbility`). Agents should send the bearer token.

Tools:

- `create_site` — no arguments; returns `slug` and `url`
- `put_file` — `slug`, `path`, `content`, optional `encoding` (`utf-8` or `base64`)
- `delete_file` — `slug`, `path`
- `list_files` — `slug`

Paths are cleaned. `..` and absolute paths are rejected. Files live in `DATA_DIR/sites/{slug}/`.

## Local check without DNS

Use `BASE_DOMAIN=localhost` and send a `Host` header. No wildcard DNS or TLS is set up by this binary.

```sh
# after uploading index.html for slug alksgi
curl -s -H 'Host: alksgi.localhost' http://127.0.0.1:8080/
curl -s -H 'Host: alksgi.localhost:8080' http://127.0.0.1:8080/
```

The apex host (`localhost`, or `127.0.0.1` when that is how you connect) serves only the admin UI and MCP, not site files. A site host serves only that site's files.

`go test ./...` covers slug generation, path traversal, Host routing, the browser upload, and MCP `create_site` + `put_file`.

## Limits

- Magic-link and password-reset mail is discarded, not delivered (the library's default sender would also only log the message). Use the setup token and a password for the first account, then email and password to sign in.
- `storage/sqlite` v0.3.0 is built for theauth-go v2.11.0. Newer theauth releases add storage methods this SQLite adapter does not implement, so the module is pinned to v2.11.0.
- Signup closes after the first user.
- There is no DNS or certificate automation. Point `*.BASE_DOMAIN` at this process yourself.
- When `BASE_DOMAIN` is not a localhost name, the MCP SDK's loopback Host check is turned off so a reverse proxy on `127.0.0.1` can forward the public `Host`. Calls still require a session or API token.
- Uploads are limited to 8 MiB.
