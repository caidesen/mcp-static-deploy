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

## Container image

The published image is `ghcr.io/caidesen/mcp-static-deploy`. [`.github/workflows/publish-image.yml`](.github/workflows/publish-image.yml) builds it with `docker/build-push-action` and pushes to GitHub Container Registry using `GITHUB_TOKEN` (`packages: write`). It runs on a push to `main` and on version tags (`v*`). Pull requests do not publish.

A push to `main` is tagged `latest` and with the short commit sha. A version tag such as `v1.2.3` is also published as `v1.2.3`, `1.2.3`, `1.2`, and `1`, plus the short sha. Version tags do not move `latest`.

The Dockerfile is multi-stage. The build stage uses `golang:1.26.10-bookworm` and compiles with `CGO_ENABLED=0`. The final stage is distroless static and runs only that binary, as uid 65532 (`nonroot`).

```sh
docker build -t mcp-static-deploy .
docker run --rm -p 8080:8080 \
  -e BASE_URL=http://localhost:8080 \
  -e BASE_DOMAIN=localhost \
  -e SETUP_TOKEN=dev-setup-token \
  -v mcp-static-data:/data \
  mcp-static-deploy
```

Pull the published image with `docker pull ghcr.io/caidesen/mcp-static-deploy:latest`.

| Variable | Image default | Role |
| --- | --- | --- |
| `ADDR` | `:8080` | Listen address. The image exposes port 8080. |
| `BASE_URL` | `http://localhost:8080` | Apex URL. `http` leaves the session cookie non-Secure. |
| `BASE_DOMAIN` | none | Required. The image does not set this. Public sites are served on `{slug}.{BASE_DOMAIN}`. The process refuses to start if it is empty. |
| `DATA_DIR` | `/data` | SQLite file `app.db` and site files `sites/{slug}/`. Mount a volume here. This replaces the binary's relative default `data`. |
| `SETUP_TOKEN` | unset | If unset, the process generates a setup token and logs it once. |

A Docker named volume is created writable by uid 65532. A bind-mounted host directory must already be writable by that user.

This image does not configure DNS or TLS. The wildcard DNS assumption stays outside this change: point `*.BASE_DOMAIN` at the process yourself.

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

`/mcp` is Streamable HTTP. It accepts only an OAuth access token: `Authorization: Bearer` with a JWT whose audience is `{BASE_URL}/mcp`. A browser session does not authorize MCP. Anonymous calls are rejected.

This process is the authorization server. Clients discover it from `GET /.well-known/oauth-protected-resource/mcp`, which points at `GET /.well-known/oauth-authorization-server`.

Registration is open. An MCP client registers itself with `POST /oauth/register` (one redirect URI, a public client, PKCE S256). There is no registration token to create. The person signs in with email and password when the browser is sent to `/`, then the client finishes the authorization-code exchange at `POST /oauth/token`. Request the `publish` scope. Loopback `http://127.0.0.1` redirect URIs are accepted, including a changing port.

Mounted OAuth routes:

- `GET /.well-known/oauth-authorization-server`
- `GET /.well-known/openid-configuration`
- `GET /.well-known/oauth-protected-resource`
- `GET /.well-known/oauth-protected-resource/mcp`
- `GET /oauth/jwks`
- `GET /oauth/authorize`
- `POST /oauth/token`
- `POST /oauth/revoke`
- `POST /oauth/introspect`
- `POST /oauth/register`

The signing key is sealed with `DATA_DIR/oauth-encryption.key`, created on first boot. Keep that file with the database.

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
- When `BASE_DOMAIN` is not a localhost name, the MCP SDK's loopback Host check is turned off so a reverse proxy on `127.0.0.1` can forward the public `Host`. Calls still require an OAuth access token for this MCP resource.
- Uploads are limited to 8 MiB.
