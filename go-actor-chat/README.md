# go-actor-chat

A real-time chat server built on the **actor model in Go** — [`anthdm/hollywood`](https://github.com/anthdm/hollywood)
actors, `echo` + `coder/websocket` for HTTP/WebSocket, **Clerk** for auth, and
**Convex** for persistence. This is the original implementation that the Rust and
Zig ports are measured against; all three speak the same WebSocket protocol and are
driven by the same React frontend, so you can tell which backend you're on from a
badge in the UI.

> Built as a learning project to explore the actor model through a real framework.
> The companion implementations live in sibling `rust-actor-chat/` and
> `zig-actor-chat/` directories; the [repo-root README](../README.md) covers the
> shared design.

## What it is

The server is a **stateless real-time relay**: ephemeral state (room presence, typing
indicators) lives in the actors, while durable state (users, rooms, messages) lives in
Convex. The React client reads message history directly from Convex (reactive queries)
and uses the WebSocket only for sending, presence, and typing.

Three actor roles, coordinated only by messages:

- **Connection actor** — one per WebSocket, spawned by the hollywood engine. Owns the
  socket's write half so all writes serialize through its mailbox. The blocking socket
  **read** runs off the actor (a plain `ReadLoop` that parses frames and forwards them
  into mailboxes).
- **Room actor** — one per room, spawned lazily. Single owner of that room's presence
  and typing state; broadcasts `presence_update` / `typing_update` to members. An idle
  `SendRepeat` tick lets it `Poison` itself when empty; it respawns on demand.
- **Registry** — maps room IDs to actor PIDs and spawns rooms lazily.

## WebSocket protocol

One JSON envelope per message, `{"type": ...}` discriminated (a single `Frame` struct
with `omitempty` fields, in [`internal/ws/protocol.go`](internal/ws/protocol.go)).

| Inbound (client→server) | Fields |
|---|---|
| `join` / `leave` | `roomId` |
| `typing_start` / `typing_stop` | `roomId` |
| `send` | `roomId`, `body`, `clientId` |
| `ping` | optional `token` (fresh JWT lease) |

| Outbound (server→client) | Fields |
|---|---|
| `ack` | `roomId`, `messageId`, `clientId`, `createdAt` |
| `error` | `roomId?`, `clientId?`, `reason` |
| `presence_update` | `roomId`, `users[]` |
| `typing_update` | `roomId`, `userId`, `typing` |
| `pong` | — |

## Project layout

```
cmd/api/main.go       entry point + graceful shutdown
internal/
  server/             echo wiring, routes (/health /ready /ws),
                      Clerk JWT validation (JWKS, RS256, aud="convex"),
                      WS upgrade with origin allowlist
  ws/                 per-connection actor, read loop, wire Frame
  room/               per-room actor (presence/typing/idle eviction),
                      lazy registry, message persistence via Convex
  ping/               request/response actor used by the readiness probe
  convex/             minimal Convex HTTP client (net/http)
convex/               Convex backend (schema + functions), deployed separately
web/                  React 19 + Vite frontend (Clerk + Convex)
```

## Running it

### Prerequisites
- Go (see [`go.mod`](go.mod) for the toolchain version), and `bun` or `node` for the
  Convex CLI + frontend
- A **Convex** project (`npx convex dev` to create/link one)
- A **Clerk** application with a JWT template named exactly `convex` (audience `convex`)

### 1. Deploy the Convex backend
```bash
bun install
npx convex dev --once                                   # push schema + functions
npx convex env set CLERK_JWT_ISSUER_DOMAIN https://<your-app>.clerk.accounts.dev
```

### 2. Configure and run the Go server
Create `.env.local` (sourced automatically by `make run` / `make watch`):
```
PORT=8080
CONVEX_URL=https://<your-deployment>.convex.cloud
CLERK_JWT_ISSUER_DOMAIN=https://<your-app>.clerk.accounts.dev
# comma-separated origins for non-localhost deployments (defaults allow localhost only)
# ALLOWED_ORIGINS=https://chat.example.com
```
```bash
make run         # go run, listening on :8080
make watch       # live reload via air
make test        # go test ./...
make build       # builds ./main
```

### 3. Run the frontend
Copy [`web/.env.example`](web/.env.example) to `web/.env.local` and fill in your
Convex URL and Clerk publishable key, then:
```bash
cd web && bun install && bun dev      # http://localhost:5173
```

Sign in, pick **Go** on the implementation screen, and the sidebar badge shows which
actor server you're on. Click the badge to switch.

## Design notes & known simplifications

- **Idempotent sends**: the room actor keeps an in-memory `seen` cache keyed on
  `(roomId, clientId)` as a fast path, but the Convex `by_room_clientId` unique index
  is the idempotency authority — a restart doesn't break dedupe.
- **Origin allowlist fails closed**: WS upgrades allow `localhost`/`127.0.0.1` by
  default and reject everything else unless `ALLOWED_ORIGINS` is set.
- **Presence/typing are in-memory and unauthorized**: Convex enforces room membership
  on `messages:send`/`messages:list`, but any signed-in user can join the realtime
  presence channel of a room ID they know. Fine for a demo; a production version
  would check membership on `join`.
- **Graceful shutdown** gives in-flight requests 5 seconds; a second Ctrl-C force-quits.

## License

MIT — see [LICENSE](../LICENSE).
