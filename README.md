# Glimpse

Glimpse is a small, stateless media and URL preview proxy built for Clover. It fetches public web resources on behalf of clients, extracts link metadata, and streams bounded image, audio, and video responses without connecting each user directly to the remote media host.

## Features

- Open Graph, Twitter Card, oEmbed, and standard HTML metadata parsing.
- Image, audio, and video inspection with bounded streaming and single-range support.
- SSRF protection for private, loopback, link-local, and special-use networks.
- DNS validation and address pinning to resist DNS rebinding.
- Bounded response sizes, redirects, deadlines, global concurrency, and per-host concurrency.
- Exact browser-origin CORS allowlists.
- No database or persistent application state.

## Running Glimpse

Download a binary from the [latest release](https://github.com/0230am/glimpse/releases/latest):

```sh
curl -fLO https://github.com/0230am/glimpse/releases/latest/download/glimpse-linux-amd64
curl -fLO https://github.com/0230am/glimpse/releases/latest/download/glimpse-linux-amd64.sha256
sha256sum -c glimpse-linux-amd64.sha256
chmod +x glimpse-linux-amd64

env GLIMPSE_ALLOWED_ORIGINS=http://localhost:5173 ./glimpse-linux-amd64
```

The default listener is `127.0.0.1:8080`. Put an internet-facing deployment behind a reverse proxy that terminates TLS and applies request, connection, and bandwidth limits.

To run from source:

```sh
go test ./...
go run ./cmd/glimpse
```

Glimpse requires Go 1.27 or newer when building from source.

## Configuration

| Variable | Default | Description |
| --- | --- | --- |
| `GLIMPSE_LISTEN_ADDRESS` | `127.0.0.1:8080` | HTTP listen address. |
| `GLIMPSE_PUBLIC_URL` | `http://<listen address>` | Canonical public origin used in generated media URLs. |
| `GLIMPSE_ALLOWED_ORIGINS` | empty | Comma-separated exact browser origins allowed by CORS. |
| `GLIMPSE_ALLOWED_PORTS` | `80,443` | Comma-separated remote HTTP/HTTPS ports Glimpse may contact. |
| `GLIMPSE_MAX_CONCURRENT_REQUESTS` | `64` | Maximum concurrent outbound requests. |
| `GLIMPSE_MAX_CONCURRENT_REQUESTS_PER_HOST` | `8` | Maximum concurrent outbound requests per hostname. |

Example:

```sh
GLIMPSE_LISTEN_ADDRESS=127.0.0.1:8080
GLIMPSE_PUBLIC_URL=https://glimpse.example.com
GLIMPSE_ALLOWED_ORIGINS=https://chat.example.com,http://localhost:5173
GLIMPSE_ALLOWED_PORTS=80,443,5443
GLIMPSE_MAX_CONCURRENT_REQUESTS=32
GLIMPSE_MAX_CONCURRENT_REQUESTS_PER_HOST=4
```

## HTTP API

All content endpoints accept the remote HTTP or HTTPS URL through the `url` query parameter.

| Endpoint | Purpose |
| --- | --- |
| `GET /api/link-preview?url=...` | Return normalized link-preview metadata as JSON. |
| `GET /api/link-preview/image?url=...` | Fetch a bounded preview image. |
| `GET /api/link-preview/media?url=...` | Inspect remote media type and size. |
| `GET /api/link-preview/media?content=1&url=...` | Stream bounded audio or video content. |
| `GET /api/link-preview/media/image?url=...` | Stream a bounded full-size image. |
| `GET /livez` | Liveness probe. |
| `GET /readyz` | Readiness probe. |

The media streaming endpoint accepts at most one HTTP byte range.

## Security model

Glimpse accepts HTTP and HTTPS URLs only on the ports listed in `GLIMPSE_ALLOWED_PORTS`. Ports 80 and 443 are enabled by default; deployments can add ports used by federated media hosts, such as 5443. It rejects embedded credentials, local hostnames, private addresses, special-use addresses, disallowed ports, and redirects that cross those boundaries. DNS answers are validated before connecting, and the accepted address is pinned for that connection.

Current response limits are:

- HTML: 512 KiB.
- oEmbed JSON: 128 KiB.
- Preview images: 5 MiB.
- Streamed media and full-size images: 32 MiB.
- Redirects: four.
- Metadata requests: seven seconds.
- Media streams: two minutes.

These application limits complement, rather than replace, rate limiting at the reverse proxy.

### Browser origins and private instances

`GLIMPSE_ALLOWED_ORIGINS` controls which browser origins receive CORS permission. A request with a disallowed `Origin` header receives `403 Forbidden`. Requests without an `Origin` header are accepted so server-side and native clients can use the API.

CORS is not authentication. A direct client can omit or spoof `Origin`, so an origin allowlist cannot make an internet-facing proxy exclusive to one instance.

For an instance-private deployment, keep Glimpse on a private network or loopback interface and expose it through the instance's authenticated backend or API gateway. Browser and native clients should authenticate to that gateway. Do not embed one shared proxy secret in browser JavaScript or a native application binary.

## Health checks

`/livez` answers whether the process is alive. A failed liveness probe normally tells an orchestrator to restart the process.

`/readyz` answers whether the process should receive traffic. A failed readiness probe normally removes an instance from routing without restarting it.

Glimpse currently has no database or startup dependency, so both endpoints return `204 No Content` while the HTTP server is running. They remain separate so readiness can become stricter later without changing liveness behavior. The `z` suffix is a conventional health-check name used by Kubernetes-style infrastructure; it has no special protocol meaning.

## Clover

Clover can use a Glimpse deployment by setting:

```env
PUBLIC_GLIMPSE_URL=https://glimpse.example.com
```

The Clover development server normally runs at `http://localhost:5173`, which must appear in `GLIMPSE_ALLOWED_ORIGINS` when it calls a remote Glimpse instance directly.

## License

Glimpse is available under the [MIT License](LICENSE).
