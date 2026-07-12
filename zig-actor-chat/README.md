# zig-actor-chat

A real-time chat server built on the **actor model in Zig** — [InkList](https://github.com/Joseph-Matteo-Scorsone/InkList) actors,
[http.zig](https://github.com/karlseguin/http.zig) + [websocket.zig](https://github.com/karlseguin/websocket.zig) for HTTP/WebSocket,
**Clerk** for auth, and **Convex** for persistence. Protocol-compatible with the Go and Rust siblings so the same React frontend can talk to any of them.

## Architecture

```
   Browser (React + Vite)
     │            │
     │ Clerk      │ WebSocket (?token=<Clerk JWT>)
     │ + Convex   ▼
     │ reactive   ┌──────────────────────────────────────────────┐
     │ queries    │         zig-actor-chat (httpz :8100)         │
     │            │                                              │
     │            │   /ws ── validates Clerk JWT (JWKS / RS256)  │
     │            │       │  resolves Convex user id             │
     │            │       ▼                                      │
     │            │  WebsocketHandler (read loop)                │
     │            │       │                                      │
     │            │       ├─ InkList ConnWriter actor ──▶ socket │
     │            │       └─ InkList Room actors (presence/typing)│
     │            │            via RoomRegistry (lazy)           │
     │            └───────────────────────┬──────────────────────┘
     ▼                                    │ HTTP (/api/mutation, /api/query)
   Clerk                                  ▼
                                        Convex
```

Three actor roles (same design as Go/Rust):

- **ConnWriter** — InkList actor that owns WebSocket writes (serialized mailbox).
- **RoomActor** — InkList actor that single-owns presence/typing; idle-evicts via `sendEvery`.
- **RoomRegistry** — mutex map of `room_id → actor_id`, respawns after eviction.

The WebSocket **read** path lives in httpz's `WebsocketHandler` (off-actor), matching the Go `ReadLoop` / Rust read loop.

## WebSocket protocol

Same JSON envelopes as the other implementations (`join`, `leave`, `send`, `typing_*`, `ping` inbound; `hello`, `ack`, `error`, `presence_update`, `typing_update`, `pong` outbound). The `hello` frame identifies the backend as `zig-actor-chat`.

## Project layout

```
src/
  main.zig              entry + graceful shutdown
  config.zig            env / .env.local loading
  auth.zig              Clerk JWT validation (JWKS, RS256, aud=convex)
  convex.zig            minimal Convex HTTP client
  protocol.zig          wire frames
  server.zig            httpz routes + WebsocketHandler
  actors/
    connection.zig      ConnWriter InkList actor
    room.zig            RoomActor + typed func payloads
    registry.zig        lazy room registry
vendor/InkList/         vendored actor runtime (patched for Zig 0.15)
```

Use the Convex backend + React app from `rust-actor-chat/` or `go-actor-chat/` (point `VITE_WS_URL_ZIG` at this server).

## Running it

### Prerequisites
- Zig **0.15.x**
- A Convex project and Clerk app with a JWT template named `convex` (audience `convex`)

### Configure
```bash
cp .env.example .env.local
# fill CONVEX_URL and CLERK_JWT_ISSUER_DOMAIN (same values as the Rust/Go servers)
```

### Build & run
```bash
zig build
zig build run
# listening on http://0.0.0.0:8100
```

### Frontend
From `rust-actor-chat/web` (or go):
```
VITE_WS_URL_ZIG=ws://localhost:8100
```
Pick **Zig** on the implementation screen after sign-in.

### Tests
```bash
zig build test
```

## Go / Rust / Zig at a glance

| Concern | Go | Rust | Zig |
|---|---|---|---|
| Actor runtime | Hollywood | hand-rolled tokio mpsc | **InkList** |
| HTTP / WebSocket | echo + coder/websocket | axum | **httpz + websocket.zig** |
| Port | 8080 | 8090 | **8100** |

## License

MIT (or your choice). InkList is MIT; see `vendor/InkList/LICENSE`.
