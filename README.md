# actor-chat

One real-time chat app, built **two ways** — to study how the **actor model** maps
onto two different language/runtime ecosystems. Both implementations speak the same
WebSocket protocol, persist to the same kind of [Convex](https://convex.dev) backend,
authenticate with the same [Clerk](https://clerk.com) setup, and are driven by the same
React frontend, so you can run either one behind an identical UI.

| Implementation | Actor runtime | HTTP/WS | Port | Status |
|---|---|---|---|---|
| **[go-actor-chat/](go-actor-chat/)** | [`anthdm/hollywood`](https://github.com/anthdm/hollywood) | `echo` + `coder/websocket` | `8080` | working |
| **[rust-actor-chat/](rust-actor-chat/)** | hand-rolled `tokio` (`mpsc`/`oneshot`) | `axum` | `8090` | M0–M6 complete |

Each subproject has its own detailed README, run guide, and self-contained
`convex/` + `web/`.

## The shared design

Both servers are **stateless real-time relays**. Ephemeral state — room presence and
typing indicators — lives in the actors; durable state — users, rooms, memberships,
messages — lives in Convex. The React client reads message history straight from Convex
(reactive queries) and uses the WebSocket only for sending, presence, and typing.

```
   Browser (React + Vite)
     │            │
     │ Clerk      │ WebSocket  (?token=<Clerk JWT>)
     │ + Convex   ▼
     │ reactive   ┌──────────────────────────────────────┐
     │ queries    │   actor server  (Go :8080 / Rust :8090)│
     │            │                                        │
     │            │   validate Clerk JWT → resolve user    │
     │            │   per-connection actor  (owns socket)  │
     │            │   per-room actor        (presence/typing) │
     │            │   lazy room registry                   │
     │            └───────────────────┬────────────────────┘
     ▼                                │ HTTP (/api/query, /api/mutation)
   Clerk                              ▼
                                    Convex  (users, rooms, memberships, messages)
```

Three actor roles in both: a **per-connection actor** that owns its socket's writes
(so they serialize through one mailbox), a **per-room actor** that single-owns presence
and typing and broadcasts updates, and a **registry** that spawns rooms lazily and
evicts them when idle. The blocking socket read runs *off* the actor in both.

## Shared WebSocket protocol

One JSON envelope per message, discriminated by `type`.

- **Inbound:** `join`, `leave`, `send`, `typing_start`, `typing_stop`, `ping`
- **Outbound:** `ack`, `error`, `presence_update`, `typing_update`, `pong`
  (the Rust server also sends a `hello` frame on connect — see below)

## Go vs. Rust at a glance

| Concern | go-actor-chat | rust-actor-chat |
|---|---|---|
| Actor runtime | `anthdm/hollywood` framework | hand-rolled `tokio::mpsc` + `oneshot` |
| HTTP / WebSocket | `echo` + `coder/websocket` | `axum` |
| Wire protocol | one `Frame` struct, `omitempty` | `serde` tagged enums (compiler-checked) |
| Request/response | `engine.Request(...).Result()` | `oneshot` reply channels |
| Room registry | mutex map + engine registry | `Arc<Mutex<HashMap>>` + `is_closed()` respawn |
| JWT / JWKS | `golang-jwt` + `keyfunc` | `jsonwebtoken` + cached JWKS |
| Convex client | `net/http` | `reqwest` |
| Message persistence | room actor + in-memory `seenCache` | connection-side spawned task; Convex index is the authority |
| Idle eviction | `SendRepeat` tick + `Poison` | `select!` + `interval`; respawn on demand |

The headline difference: Go leans on a **framework** (Hollywood gives you actors,
supervision, and a registry), while Rust **hand-rolls** the same patterns from `tokio`
primitives — an actor is just a struct owning an `mpsc::Receiver` in a loop, and its
address is a cloneable handle wrapping the `Sender`. Seeing both side by side is the
point of this repo.

## Running them / A-B comparison

See each subproject's README for full setup (Convex deploy + Clerk JWT template + env):

- **[go-actor-chat/README.md](go-actor-chat/README.md)**
- **[rust-actor-chat/README.md](rust-actor-chat/README.md)**

Because both speak the same protocol, you can point the React frontend at either
backend from the **implementation picker** shown right after sign-in (Go :8080,
Rust :8090; Zig is listed but not available yet). Choice is stored in
`localStorage`; click the sidebar badge to switch. Override URLs with
`VITE_WS_URL_GO` / `VITE_WS_URL_RUST` (and later `VITE_WS_URL_ZIG`). The Rust
server announces itself with a `hello` frame that upgrades the sidebar badge.

## Repository layout

```
actor-chat/
├── go-actor-chat/     Go + Hollywood implementation (server, convex/, web/)
├── rust-actor-chat/   Rust + tokio implementation   (server, convex/, web/)
└── README.md          (this file)
```

## License

MIT (or your choice).
