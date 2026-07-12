# actor-chat

One real-time chat app, built **three ways** — to study how the **actor model** maps
onto different language/runtime ecosystems. All implementations speak the same
WebSocket protocol, persist to the same kind of [Convex](https://convex.dev) backend,
authenticate with the same [Clerk](https://clerk.com) setup, and are driven by the same
React frontend, so you can run any one behind an identical UI.

| Implementation | Actor runtime | HTTP/WS | Port | Status |
|---|---|---|---|---|
| **[go-actor-chat/](go-actor-chat/)** | [`anthdm/hollywood`](https://github.com/anthdm/hollywood) | `echo` + `coder/websocket` | `8080` | working |
| **[rust-actor-chat/](rust-actor-chat/)** | hand-rolled `tokio` (`mpsc`/`oneshot`) | `axum` | `8090` | working |
| **[zig-actor-chat/](zig-actor-chat/)** | [`InkList`](https://github.com/Joseph-Matteo-Scorsone/InkList) | `httpz` + `websocket.zig` | `8100` | working |

Each subproject has its own detailed README and run guide. Go and Rust ship self-contained
`convex/` + `web/`; the Zig server reuses those (point `VITE_WS_URL_ZIG` at `:8100`).

## The shared design

All servers are **stateless real-time relays**. Ephemeral state — room presence and
typing indicators — lives in the actors; durable state — users, rooms, memberships,
messages — lives in Convex. The React client reads message history straight from Convex
(reactive queries) and uses the WebSocket only for sending, presence, and typing.

```
   Browser (React + Vite)
     │            │
     │ Clerk      │ WebSocket  (?token=<Clerk JWT>)
     │ + Convex   ▼
     │ reactive   ┌──────────────────────────────────────────┐
     │ queries    │   actor server  (Go :8080 / Rust :8090 / │
     │            │                  Zig :8100)              │
     │            │                                          │
     │            │   validate Clerk JWT → resolve user      │
     │            │   per-connection writer  (owns socket)   │
     │            │   per-room actor        (presence/typing)│
     │            │   lazy room registry                     │
     │            └───────────────────┬──────────────────────┘
     ▼                                │ HTTP (/api/query, /api/mutation)
   Clerk                              ▼
                                    Convex  (users, rooms, memberships, messages)
```

Three actor roles in all three: a **per-connection writer** that owns socket writes
(so they serialize through one mailbox), a **per-room actor** that single-owns presence
and typing and broadcasts updates, and a **registry** that spawns rooms lazily and
evicts them when idle. The blocking socket read runs *off* the actor in all three.

## Shared WebSocket protocol

One JSON envelope per message, discriminated by `type`.

- **Inbound:** `join`, `leave`, `send`, `typing_start`, `typing_stop`, `ping`
- **Outbound:** `ack`, `error`, `presence_update`, `typing_update`, `pong`
  (Rust and Zig also send a `hello` frame on connect)

## Go vs. Rust vs. Zig at a glance

| Concern | go-actor-chat | rust-actor-chat | zig-actor-chat |
|---|---|---|---|
| Actor runtime | `anthdm/hollywood` | hand-rolled `tokio::mpsc` | **InkList** |
| HTTP / WebSocket | `echo` + `coder/websocket` | `axum` | **httpz + websocket.zig** |
| Wire protocol | one `Frame` struct | `serde` tagged enums | one `Frame` struct |
| Request/response | `engine.Request` | `oneshot` | func payloads / threads |
| Room registry | mutex map + engine registry | `Arc<Mutex<HashMap>>` | mutex map + actor ids |
| JWT / JWKS | `golang-jwt` + `keyfunc` | `jsonwebtoken` | std RSA + JWKS fetch |
| Convex client | `net/http` | `reqwest` | `std.http.Client` |
| Idle eviction | `SendRepeat` + `Poison` | `select!` + interval | InkList `sendEvery` |

The point of the repo: see the same actor shapes expressed through a framework (Go),
hand-rolled async primitives (Rust), and a Zig actor library (InkList).

## Running them / A-B-C comparison

See each subproject's README for full setup (Convex deploy + Clerk JWT template + env):

- **[go-actor-chat/README.md](go-actor-chat/README.md)**
- **[rust-actor-chat/README.md](rust-actor-chat/README.md)**
- **[zig-actor-chat/README.md](zig-actor-chat/README.md)**

Because they speak the same protocol, you can point the React frontend at any
backend from the **implementation picker** shown right after sign-in (Go :8080,
Rust :8090, Zig :8100). Choice is stored in `localStorage`; click the sidebar badge
to switch. Override URLs with `VITE_WS_URL_GO` / `VITE_WS_URL_RUST` / `VITE_WS_URL_ZIG`.

## Repository layout

```
actor-chat/
├── go-actor-chat/     Go + Hollywood implementation (server, convex/, web/)
├── rust-actor-chat/   Rust + tokio implementation   (server, convex/, web/)
├── zig-actor-chat/    Zig + InkList implementation  (server; reuse Go/Rust web)
└── README.md          (this file)
```

## License

MIT (or your choice).
