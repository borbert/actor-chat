# rust-actor-chat

A real-time chat server built on the **actor model in idiomatic Rust** — hand-rolled
`tokio` actors (no actor framework), `axum` for HTTP/WebSocket, **Clerk** for auth,
and **Convex** for persistence. It's a Rust re-implementation of a Go/[Hollywood](https://github.com/anthdm/hollywood)
chat server, kept protocol-compatible so the same React frontend can talk to either
backend — you can tell which one you're hitting from a 🦀/🐹 badge in the UI.

> Built as a learning project to explore how the actor model maps onto async Rust.
> The companion Go implementation lives in a sibling `go-actor-chat/` directory.

## What it is

The server is a **stateless real-time relay**: ephemeral state (room presence, typing
indicators) lives in the actors, while durable state (users, rooms, messages) lives in
Convex. The React client reads message history directly from Convex (reactive queries)
and uses the WebSocket only for sending, presence, and typing.

## Architecture

```
   Browser (React + Vite)
     │            │
     │ Clerk      │ WebSocket (?token=<Clerk JWT>)
     │ + Convex   ▼
     │ reactive   ┌──────────────────────────────────────────────┐
     │ queries    │            rust-actor-chat (axum)            │
     │            │                                              │
     │            │   ws_handler ── validates Clerk JWT (JWKS)   │
     │            │       │         resolves Convex user id      │
     │            │       ▼                                      │
     │            │  ┌───────────────┐   ConnHandle   ┌────────┐ │
     │            │  │  read loop    │──(mpsc)───────▶│ conn   │ │
     │            │  │ parse Inbound │                │ actor  │─┼─▶ socket
     │            │  └───────┬───────┘                └────────┘ │
     │            │          │ RoomMsg                  ▲        │
     │            │          ▼                          │ Outbound
     │            │  ┌───────────────┐  broadcast       │        │
     │            │  │  room actor   │──────────────────┘        │
     │            │  │ presence/typing│  (RoomRegistry, lazy)    │
     │            │  └───────────────┘                           │
     │            └───────────────────────┬──────────────────────┘
     ▼                                    │ HTTP (/api/mutation, /api/query)
   Clerk                                  ▼
                                        Convex  (users, rooms, memberships, messages)
```

Three actor types, coordinated only by messages:

- **Connection actor** — one per WebSocket. Owns the socket's write half so all writes
  serialize through one mailbox (`tokio::mpsc`). Its address is a cloneable `ConnHandle`.
- **Room actor** — one per room, spawned lazily. Single owner of that room's presence and
  typing state; broadcasts `presence_update` / `typing_update` to members. Self-evicts
  after going idle and respawns on demand.
- **RoomRegistry** — `Arc<Mutex<HashMap<RoomId, RoomHandle>>>` mapping rooms to actors.

The blocking socket **read** runs off the actor (a plain loop that forwards parsed frames
into mailboxes), mirroring the same discipline as the Go `ReadLoop`.

## WebSocket protocol

One JSON envelope per message, `{"type": ...}` discriminated (modeled as `serde` tagged
enums in [`src/protocol.rs`](src/protocol.rs)).

| Inbound (client→server) | Fields |
|---|---|
| `join` / `leave` | `roomId` |
| `typing_start` / `typing_stop` | `roomId` |
| `send` | `roomId`, `body`, `clientId` |
| `ping` | optional `token` (fresh JWT lease) |

| Outbound (server→client) | Fields |
|---|---|
| `hello` | `server`, `version` (backend identity for the UI badge) |
| `ack` | `roomId`, `messageId`, `clientId`, `createdAt` |
| `error` | `roomId?`, `clientId?`, `reason` |
| `presence_update` | `roomId`, `users[]` |
| `typing_update` | `roomId`, `userId`, `typing` |
| `pong` | — |

## Project layout

```
src/
  main.rs            thin entry point
  lib.rs             AppState + run() + graceful shutdown
  config.rs          env-based Settings
  telemetry.rs       tracing setup
  auth.rs            Clerk JWT validation (JWKS, RS256, aud="convex")
  convex.rs          minimal Convex HTTP client (reqwest)
  protocol.rs        Inbound/Outbound serde tagged enums
  routes.rs          /health /ready /version /ws
  actors/
    connection.rs    per-connection actor + read loop + send/ping
    room.rs          per-room actor (presence/typing/idle eviction)
    registry.rs      lazy room registry
convex/              Convex backend (schema + functions), deployed separately
web/                 React 19 + Vite frontend (Clerk + Convex)
```

## Running it

### Prerequisites
- Rust (stable), and `bun` or `node` for the Convex CLI + frontend
- A **Convex** project (`npx convex dev` to create/link one)
- A **Clerk** application with a JWT template named exactly `convex` (audience `convex`)

### 1. Deploy the Convex backend
```bash
bun install
npx convex dev --once                                   # push schema + functions
npx convex env set CLERK_JWT_ISSUER_DOMAIN https://<your-app>.clerk.accounts.dev
```

### 2. Configure and run the Rust server
Create `.env.local`:
```
PORT=8090
CONVEX_URL=https://<your-deployment>.convex.cloud
CLERK_JWT_ISSUER_DOMAIN=https://<your-app>.clerk.accounts.dev
```
```bash
cargo run        # listening on http://0.0.0.0:8090
```

### 3. Run the frontend
Create `web/.env.local`:
```
VITE_CONVEX_URL=https://<your-deployment>.convex.cloud
VITE_WS_URL_GO=ws://localhost:8080
VITE_WS_URL_RUST=ws://localhost:8090
VITE_CLERK_PUBLISHABLE_KEY=pk_test_...
```
```bash
cd web && bun install && bun dev      # http://localhost:5173
```

Sign in, pick **Rust** (or Go) on the implementation screen, and the sidebar badge
shows which actor server you're on. Click the badge to switch.

## Go vs. Rust at a glance

| Concern | Go implementation | Rust implementation |
|---|---|---|
| Actor runtime | `anthdm/hollywood` framework | hand-rolled `tokio::mpsc` + `oneshot` |
| HTTP / WebSocket | `echo` + `coder/websocket` | `axum` |
| Wire protocol | one `Frame` struct, `omitempty` | `serde` tagged enums (compiler-checked) |
| Request/response | `engine.Request(...).Result()` | `oneshot` reply channels |
| Room registry | mutex map + engine registry | `Arc<Mutex<HashMap>>` + `is_closed()` respawn |
| JWT / JWKS | `golang-jwt` + `keyfunc` | `jsonwebtoken` + cached JWKS |
| Convex client | `net/http` | `reqwest` |
| Idle eviction | `SendRepeat` tick + `Poison` | `select!` + `interval`; respawn on demand |

## Design notes & known simplifications

- **Message persistence runs in the connection, not the room actor.** A Convex write is a
  network round-trip; doing it inside the room actor would stall presence/typing for
  everyone else in that room. So sends are spawned concurrently from the read loop, and
  Convex's `by_room_clientId` unique index is the idempotency authority (the Go version's
  in-memory `seenCache` was only ever a fast path). The "single writer" guarantee still
  holds for *room state* — persistence just isn't room state.
- **Broadcasts `await` on bounded per-connection mailboxes**, so a very slow client can
  apply backpressure to a room. A production version would `try_send` and drop/disconnect.
- **JWKS is fetched at startup and refetched on an unknown `kid`** (key rotation), without
  a periodic background refresh.
- **Graceful shutdown** drains HTTP but waits on open WebSocket upgrades (hyper); a second
  `Ctrl-C` force-quits.

## License

MIT — see [LICENSE](../LICENSE).
