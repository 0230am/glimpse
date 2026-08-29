# Glimpse

Clover Media and URL Preview Proxy.

Glimpse fetches public HTTP resources, extracts URL preview metadata, and proxies bounded image, audio, and video responses without exposing Clover users directly to remote media hosts.

## Development

```sh
go test ./...
go run ./cmd/glimpse
```

The server listens on `127.0.0.1:8080` by default.

## Configuration

| Variable | Default | Description |
| --- | --- | --- |
| `GLIMPSE_LISTEN_ADDRESS` | `127.0.0.1:8080` | HTTP listen address. |
| `GLIMPSE_PUBLIC_URL` | `http://<listen address>` | Canonical public origin used in generated media URLs. Set this explicitly in production. |
| `GLIMPSE_ALLOWED_ORIGINS` | empty | Comma-separated exact browser origins allowed by CORS. |
| `GLIMPSE_MAX_CONCURRENT_REQUESTS` | `64` | Maximum concurrent outbound requests. |
| `GLIMPSE_MAX_CONCURRENT_REQUESTS_PER_HOST` | `8` | Maximum concurrent outbound requests per hostname. |

## HTTP API

The initial API intentionally preserves Clover's existing routes:

- `GET /api/link-preview?url=...`
- `GET /api/link-preview/image?url=...`
- `GET /api/link-preview/media?url=...`
- `GET /api/link-preview/media?content=1&url=...`
- `GET /api/link-preview/media/image?url=...`
- `GET /livez`
- `GET /readyz`

The plain media route returns metadata. Adding the `content` query parameter streams the bounded remote media response and supports one HTTP byte range.

## Security boundaries

Glimpse is stateless. It rejects non-HTTP URLs, credentials, non-standard ports, local hostnames, private or special-use IP addresses, and redirect targets that cross those boundaries. DNS answers are validated and the accepted address is pinned for the connection. Responses, redirects, concurrency, and per-host concurrency are bounded.

Production still needs edge rate limiting and an exact CORS allowlist. Those controls live in [`0230am/glimpse-deploy`](https://github.com/0230am/glimpse-deploy).

## Clover cutover

Build Clover with `PUBLIC_GLIMPSE_URL` set to this service's public origin. Glimpse must allow Clover's exact browser origin through `GLIMPSE_ALLOWED_ORIGINS`. Clover keeps its same-origin proxy routes when the variable is empty so deployment and rollback can happen independently.
