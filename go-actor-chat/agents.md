# Product Requirements Document — Chat App

| | |
|---|---|
| **Status** | Draft v0.1 |
| **Owner** | Bob (borbert) |
| **Last updated** | 2026-06-07 |
| **Codename** | TBD |

---

## 1. Executive summary

A real-time chat application built in Go using the Hollywood actor framework for in-memory concurrency and message ordering, with Convex as the persistence and client fan-out layer. The Go server handles business logic, validation, presence, and rate limiting; Convex handles durable storage and pushes updates to connected clients over its native WebSocket. The architecture optimizes for correctness (single-writer-per-room ordering, idempotent writes) and simplicity (no DIY fan-out, no DIY subscription layer) over raw latency.

---

## 2. Problem and motivation

Most chat implementations either (a) ship a thin wrapper over a database with no concurrency story, leading to ordering bugs, race conditions, and fragile delivery, or (b) build a from-scratch realtime stack with bespoke WebSocket fan-out, presence, and persistence that ends up being 80% of the codebase. This project explores a third path: use an actor system for the logical concurrency model and a reactive database for the realtime data plane. The goal is to find out how lean a correct, real-time chat app can be when each layer does the job it's best at.

---

## 3. Goals

1. **Correct message ordering** within a room, guaranteed by single-writer actor semantics.
2. **Durable, idempotent writes** — retries are safe; no duplicate messages even under network failures or actor restarts.
3. **Realtime delivery** to all connected clients in a room with end-to-end latency under 250ms p95.
4. **Resilient to restarts** — actors can be re-spawned on cold start or failover and reconstruct state from Convex.
5. **Small, idiomatic Go codebase** — favor clarity over abstraction. No frameworks beyond Echo, Hollywood, and the Convex HTTP client.
6. **Production deployable on a single node** for v1; architecture leaves a clear path to multi-node later.

---

## 4. Non-goals (v1)

- Voice, video, or screen sharing.
- File attachments beyond text and image URLs.
- End-to-end encryption.
- Federation or interoperability with other chat systems.
- Multi-region or multi-node clustering. Single-node deployment only.
- Native mobile apps (web client only initially).
- Admin moderation tooling beyond basic block/report flags in the schema.
- Search across full message history (Convex queries by room/time only).

---

## 5. Users and use cases

**Primary user**: an individual joining one or more chat rooms to exchange text messages in real time with other members.

**Core use cases**:
1. Sign in and see a list of rooms I belong to.
2. Open a room and see recent message history.
3. Send a message and see it appear immediately (optimistic UI), with confirmation when persisted.
4. Receive messages from others in the room in near-real-time.
5. See who else is currently online in the room (presence).
6. See typing indicators when others are composing.
7. Reconnect after a network blip and catch up on missed messages without duplicates.

**Out of scope for v1**: invitations, room discovery, public rooms, threading, reactions, mentions, push notifications.

---

## 6. Architecture overview

```
                ┌────────────────────────────────────────────┐
                │              Web Client (TS)               │
                │  ┌──────────────┐    ┌──────────────────┐  │
                │  │ Convex SDK   │    │ App WebSocket    │  │
                │  │ (subscribes  │    │ (presence,       │  │
                │  │  to messages)│    │  typing, send)   │  │
                │  └──────┬───────┘    └────────┬─────────┘  │
                └─────────┼─────────────────────┼────────────┘
                          │                     │
                  ┌───────▼─────────┐  ┌────────▼─────────────┐
                  │   Convex        │  │  Go Server (Echo)    │
                  │  • messages     │  │  • Auth middleware   │
                  │  • rooms        │  │  • WS connection mgr │
                  │  • memberships  │  │  • Hollywood engine  │
                  │  • subscriptions│  │     ├─ Room actors   │
                  │    push to      │  │     └─ Presence actor│
                  │    clients      │  └────────┬─────────────┘
                  └────────▲────────┘           │
                           │     mutations      │
                           └────────────────────┘
```

**Key principles**:
- **Convex is the source of truth** for durable data. Actors hold in-memory working state only.
- **The room actor is the single writer** for its room's messages. This guarantees ordering and makes retries safe without DB locks.
- **Convex pushes message updates to clients directly.** The Go server does not fan out chat messages.
- **The Go WebSocket exists for ephemeral state** that doesn't belong in the DB: presence, typing indicators, send acknowledgements, rate limiting, and any future features needing sub-50ms response.

---

## 7. Tech stack and rationale

| Layer | Choice | Why |
|---|---|---|
| Language | Go 1.23+ | Goroutines and channels map naturally to actor concurrency; mature ecosystem; owner is a daily Go developer. |
| HTTP framework | Echo v4 | Owner's standard; lightweight; clean middleware story. |
| Actor framework | Hollywood | Owner is familiar; first-class Go API; supports per-actor mailboxes and supervision. |
| Database / Realtime | Convex | Built-in client subscriptions eliminate the need to write a fan-out layer. Schemaful, transactional, fast. |
| Convex client (server-side) | Custom Go HTTP client | No official Go SDK; HTTP API is straightforward (`/api/query`, `/api/mutation`, `/api/action`). |
| Web client | TypeScript + Convex React SDK | Convex's reactive SDK does the heavy lifting client-side. Framework choice TBD (React/TanStack Start likely). |
| Auth | TBD — Clerk or WorkOS | Both are familiar to the owner. Decision driven by pricing model and Convex auth integration. |
| Live reload | Air | Standard with go-blueprint scaffold. |
| Hosting | TBD — Fly.io likely | Owner has prior deployment experience there. |

---

## 8. Data model (Convex schema)

Defined in `convex/schema.ts`. Indicative shape:

```ts
export default defineSchema({
  users: defineTable({
    authId: v.string(),         // external auth provider subject
    displayName: v.string(),
    avatarUrl: v.optional(v.string()),
    createdAt: v.number(),
  }).index("by_authId", ["authId"]),

  rooms: defineTable({
    name: v.string(),
    kind: v.union(v.literal("dm"), v.literal("group")),
    createdBy: v.id("users"),
    createdAt: v.number(),
  }),

  memberships: defineTable({
    roomId: v.id("rooms"),
    userId: v.id("users"),
    joinedAt: v.number(),
    lastReadAt: v.optional(v.number()),
  }).index("by_room", ["roomId"])
    .index("by_user", ["userId"])
    .index("by_room_user", ["roomId", "userId"]),

  messages: defineTable({
    roomId: v.id("rooms"),
    userId: v.id("users"),
    body: v.string(),
    clientId: v.string(),       // idempotency key from sender
    createdAt: v.number(),
    editedAt: v.optional(v.number()),
    deletedAt: v.optional(v.number()),
  }).index("by_room_time", ["roomId", "createdAt"])
    .index("by_room_clientId", ["roomId", "clientId"]),
});
```

**Notes**:
- `clientId` is required on every message and is unique per `(roomId, clientId)`. The `send` mutation checks for existing rows on this index before inserting; if found, it returns the existing message instead of creating a new one. This is what makes the entire send path idempotent.
- `lastReadAt` on `memberships` powers unread-message indicators (computed client-side via Convex subscriptions).
- Soft-delete via `deletedAt` rather than hard delete so the client can show "message deleted" placeholders.

---

## 9. Actor topology

### `RoomActor`
- One per active room. Spawned lazily on first activity, named by deterministic PID `room/<roomId>`.
- **Responsibilities**: validate sends, enforce rate limits, hold idempotency cache, write to Convex, broadcast presence/typing events to local WS connections.
- **State (in-memory only)**:
  - `members map[string]*actor.PID` — userId → connection actor for currently connected users
  - `seen map[string]string` — bounded LRU of `clientId → messageId`
  - `lastActivity time.Time` — for idle eviction
- **Lifecycle**: idle for >5 minutes with zero connected members → self-poison.

### `ConnectionActor`
- One per active WebSocket connection.
- **Responsibilities**: read frames from the client, route messages to the appropriate RoomActor, write frames back (presence updates, send acks, errors).
- **Lifecycle**: lives for the duration of the WS connection.

### `PresenceActor` (single, global)
- Tracks online users across all rooms.
- **Responsibilities**: respond to "is user X online?" queries, broadcast user-online/offline events to RoomActors that care.
- May be redundant for v1 — could fold into RoomActor's `members` map. Decision deferred to milestone 2.

### `RoomRegistry`
- Not an actor — a small struct around the Hollywood engine that maps `roomId → *actor.PID`, spawning if absent. Provides the only public API for code to "get me the actor for this room."

---

## 10. Core flows

### 10.1 Send message
1. Client generates `clientId` (UUID) for the new message and renders it optimistically in its own UI.
2. Client sends `{type: "send", roomId, body, clientId}` over the app WebSocket.
3. ConnectionActor receives, looks up RoomActor via RoomRegistry.
4. RoomActor checks its in-memory `seen` map. If `clientId` present → respond with existing messageId, done.
5. RoomActor calls `messages:send` Convex mutation (synchronous, with 5s timeout).
6. Convex mutation: check `by_room_clientId` index. If exists, return that message. Otherwise insert new row with `createdAt: Date.now()`.
7. RoomActor caches `clientId → messageId` and sends `SendAck` back to ConnectionActor.
8. ConnectionActor writes ack frame to client.
9. **Independently**: Convex pushes the new row to all clients subscribed to `messagesInRoom(roomId)`. They reconcile against their optimistic UI by `clientId`.

### 10.2 Open room and load history
1. Client navigates to room. Convex React SDK subscribes to `messagesInRoom(roomId, limit: 50)`.
2. Initial payload arrives; client renders.
3. Subscription stays open — new messages and edits arrive via push.
4. **No Go server involvement** for history load.

### 10.3 Join WebSocket and announce presence
1. Client opens WS to Go server with auth token.
2. Auth middleware validates token, extracts `userId`.
3. Server spawns ConnectionActor for this connection.
4. Client sends `{type: "join", roomId}` for each room it wants presence in.
5. ConnectionActor messages RoomActor: `Join{userId, conn: c.PID()}`.
6. RoomActor adds to `members`, broadcasts presence update to other ConnectionActors in `members`.

### 10.4 Reconnect after network blip
1. Convex subscription auto-reconnects and resyncs missed messages — no server logic needed for message catchup.
2. New WebSocket connection to Go server creates a fresh ConnectionActor; client re-issues join for each room.
3. Any in-flight sends that the client didn't get acks for are re-sent with the **same `clientId`** → idempotent dedupe in Convex.

### 10.5 Actor crash / restart
1. Hollywood supervisor restarts the RoomActor with empty state.
2. In-memory `seen` cache is lost — first retry of a recently-acked message will hit Convex, which dedupes via the unique index, returns the existing messageId.
3. No data loss, no duplicates, no client-visible disruption.

---

## 11. API surface

### Go HTTP / WebSocket

| Method | Path | Purpose |
|---|---|---|
| `GET` | `/health` | Liveness check. |
| `GET` | `/ready` | Readiness: returns 200 only when Hollywood engine is up and Convex is reachable. |
| `GET` | `/ws` | WebSocket upgrade. Requires `Authorization: Bearer <jwt>` header. |

**WebSocket message types** (JSON, `{type, ...}`):
- Inbound: `join`, `leave`, `send`, `typing_start`, `typing_stop`, `ping`
- Outbound: `ack`, `error`, `presence_update`, `typing_update`, `pong`

Schema for each documented in `internal/ws/protocol.go`.

### Convex functions

`convex/messages.ts`:
- `send(args)` — mutation. Idempotent insert keyed by `(roomId, clientId)`.
- `list(args)` — query. Paginated by `(roomId, createdAt)`.
- `edit(args)` — mutation. Authorizes by sender. Sets `editedAt`.
- `softDelete(args)` — mutation. Authorizes by sender or room admin.

`convex/rooms.ts`:
- `create(args)`, `list()`, `get(args)`, `addMember(args)`, `removeMember(args)`

`convex/users.ts`:
- `getOrCreateFromAuth(args)` — called on first authenticated connection.

All mutations are deterministic and assume Convex's automatic transactional retry. None call third-party APIs (no `action` functions in v1).

---

## 12. Reliability, consistency, ordering

| Property | How achieved |
|---|---|
| **In-room message ordering** | Single-writer RoomActor processes its mailbox serially. |
| **Idempotent sends** | Client-generated `clientId` + Convex unique index check before insert. |
| **At-least-once delivery to clients** | Convex subscriptions; clients dedupe by `_id` or `clientId`. |
| **Restart safety** | Stateless actor restart; idempotency makes retries safe. |
| **Backpressure** | Hollywood mailboxes are bounded; if a room exceeds capacity, send returns an error and client backs off. Default mailbox size TBD. |
| **Timeout discipline** | Every Convex call wrapped in `context.WithTimeout(5s)`. WebSocket reads have a 60s deadline refreshed by `ping`. |

**What we explicitly do NOT guarantee**:
- Cross-room ordering (each room is independent).
- Exactly-once delivery to clients — clients must tolerate duplicates and dedupe.
- Strict synchronous replication across regions (single-node v1).

---

## 13. Auth and security

- External auth provider (Clerk or WorkOS — TBD) issues a JWT to the client.
- Client presents JWT on WebSocket upgrade. Echo middleware validates the JWT signature, extracts `sub` (auth ID).
- On first connection for a new `sub`, Go server calls `users:getOrCreateFromAuth` to ensure a user row exists in Convex.
- The Go server's per-request Convex client uses the user's JWT (Bearer auth) so Convex mutations run with that user's identity. Convex functions enforce row-level access (e.g., `messages:send` checks that the caller has a membership row for the target room).
- A separate long-lived Convex client uses a deploy key (Convex auth scheme) for server-only operations like user provisioning.
- Secrets (deploy key, JWT signing keys) loaded from environment. Never committed.
- Rate limiting in RoomActor: cap at N sends/minute per user, configurable. Excess sends return an error frame; do not silently drop.

---

## 14. Observability

- **Structured logging** via `log/slog`. JSON output in production, key-value in dev. Every WS handler and actor adds `request_id`, `user_id`, `room_id` context where available.
- **Metrics** (decision TBD: Prometheus endpoint vs OTLP push):
  - `chat_messages_sent_total{room}` counter
  - `chat_convex_request_duration_seconds{op}` histogram
  - `chat_active_connections` gauge
  - `chat_rooms_active` gauge
  - `chat_actor_mailbox_depth{actor}` gauge
- **Tracing**: OpenTelemetry spans around each Convex call and each WS message handler. Single trace per inbound WS message.
- **Convex side**: native dashboard for query/mutation perf, function logs, and subscription stats.

---

## 15. Project structure

Following go-blueprint conventions with chat-specific additions:

```
chatapp/
├── cmd/
│   └── api/
│       └── main.go                  # Wires Hollywood engine, Echo server, Convex client
├── internal/
│   ├── server/
│   │   ├── server.go                # Echo server lifecycle
│   │   ├── routes.go                # Route registration
│   │   ├── auth.go                  # JWT middleware
│   │   └── ws.go                    # WebSocket upgrade handler
│   ├── ws/
│   │   ├── protocol.go              # Inbound/outbound message types
│   │   └── connection.go            # ConnectionActor
│   ├── room/
│   │   ├── room.go                  # RoomActor
│   │   ├── registry.go              # RoomRegistry — lookup/spawn
│   │   └── messages.go              # Actor message types (Send, Join, Leave, ...)
│   ├── convex/
│   │   ├── client.go                # HTTP client (Query/Mutation/Action)
│   │   ├── errors.go                # Convex error envelope handling
│   │   └── auth.go                  # Bearer vs deploy-key helpers
│   └── presence/                    # (Milestone 2)
│       └── presence.go
├── convex/
│   ├── schema.ts
│   ├── messages.ts
│   ├── rooms.ts
│   ├── users.ts
│   └── _generated/                  # Convex codegen
├── web/                             # Web client — framework TBD
├── .air.toml
├── .env.example
├── .gitignore
├── docker-compose.yml
├── Dockerfile
├── go.mod
├── Makefile
└── PRD.md                           # This document
```

**Package naming notes** (per Go conventions): `room`, `ws`, `convex`, `presence` — not `models`, `utils`, `helpers`. Each package's name describes its responsibility.

---

## 16. Milestones

### M0 — Project scaffold (1–2 days)
- `go-blueprint` scaffold with Echo.
- Convex project initialized; `schema.ts` committed.
- Custom Convex HTTP client (`internal/convex`) with Query/Mutation, bearer + deploy-key auth, error handling.
- Health check passing locally.

### M1 — Send and persist a message (1 week)
- Hollywood engine wired into Echo server.
- `RoomActor` with `Send` handler writing to Convex.
- `messages:send` Convex mutation with idempotency check.
- HTTP test endpoint to send messages (no WebSocket yet).
- Manual verification: messages appear in Convex dashboard, retries dedupe.

### M2 — WebSocket and real-time receive (1 week)
- WebSocket upgrade and ConnectionActor.
- Web client subscribing to `messagesInRoom` via Convex SDK.
- Send via WS, receive via Convex push.
- End-to-end demo: two browsers, one room, messages flow.

### M3 — Auth (3–5 days)
- Integrate chosen auth provider.
- JWT middleware on Echo, user provisioning on first connect.
- Convex auth integration so mutations run with user identity.

### M4 — Presence and typing (3–5 days)
- `Join`/`Leave` flows.
- Typing indicators via WS broadcast (not Convex).
- Decide whether to fold PresenceActor into RoomActor or keep separate.

### M5 — Rate limiting, observability, hardening (1 week)
- Per-user rate limits in RoomActor.
- Structured logging across the stack.
- Metrics endpoint and basic dashboards.
- Graceful shutdown: drain WS, stop accepting new connections, let actors finish in-flight writes.

### M6 — Polish and deploy (3–5 days)
- Reconnect/catchup verification under simulated failures.
- Dockerfile and deploy to chosen host.
- Final smoke tests against production deployment.

---

## 17. Open questions

1. **Project name.** Codename for the repo and product.
2. **Auth provider.** Clerk vs WorkOS — driven by pricing, Convex integration story, and whether DM-friendly user search is needed.
3. **Web client framework.** Vanilla React, Next.js, TanStack Start, or other.
4. **Room types in v1.** Only group rooms? Only DMs? Both?
5. **Multi-node story.** When (if ever) do we need to address room actors across nodes? Hollywood cluster support, consistent-hash router, or sticky LB? Defer until single-node hits real limits.
6. **Hosting.** Fly.io is the leading candidate but unconfirmed.
7. **Logging library.** Stdlib `slog` vs `zap` vs `zerolog`. Default to `slog` unless perf becomes a concern.
8. **Hollywood version pin.** Confirm latest stable and pin it.
9. **Image/file attachments.** Out of v1 scope, but worth deciding now whether Convex file storage will be the long-term answer so the schema can accommodate later.
10. **Mobile.** Web-first v1, but does anything about the protocol need to be mobile-friendly from day one? (e.g., heartbeat frequency for cellular reconnect patterns.)

---

## 18. Risks and mitigations

| Risk | Likelihood | Impact | Mitigation |
|---|---|---|---|
| No official Go Convex client | High | Med | Build minimal HTTP client (already sketched); accept that subscriptions on Go side aren't supported (not needed for this architecture). |
| Convex latency dominates perceived send latency | Med | Med | Optimistic UI on sender; if measured p95 > 250ms, consider actor-direct fan-out for live clients. |
| Single-node deployment becomes a bottleneck | Low (v1) | High (later) | Architecture supports clustering via Hollywood remote actors. Defer until measured. |
| Actor crash loses in-memory `seen` cache | Med | Low | Convex unique index is authoritative; cache is optimization only. |
| Convex outage | Low | High | Document failure mode: server returns errors; clients show "reconnecting". No local queue in v1. |
| WebSocket connection storms after region outage | Low | Med | Connection limit and exponential backoff documented for clients. |

---

## 19. Glossary

- **Actor**: an isolated unit of state and behavior in Hollywood. Processes one message at a time from its mailbox.
- **Mailbox**: per-actor queue of incoming messages. Hollywood's bounded by default.
- **PID**: actor reference. Deterministic for named actors (e.g., `room/abc123`).
- **Mutation (Convex)**: a transactional write function defined in `convex/`. Runs serializably; auto-retries on conflict.
- **Query (Convex)**: a read function. Used by client subscriptions for live updates.
- **Idempotency key (`clientId`)**: client-generated UUID that uniquely identifies a logical send across retries.
- **Subscription**: client-side hook into a Convex query that auto-updates as underlying data changes.
- **Single-writer-per-room**: only one actor (the RoomActor for that room) ever writes that room's messages. Sidesteps DB-level locking.

---

## Appendix A — Decisions log

| Date | Decision | Rationale |
|---|---|---|
| 2026-06-07 | Use Convex (not Turso, MongoDB) | Built-in client subscriptions eliminate the need to build a WebSocket fan-out layer for chat history. |
| 2026-06-07 | Stick with Go (not Rust) | Owner is daily Go developer; no perf justification for Rust in an I/O-bound app. |
| 2026-06-07 | Single writer per room via actor | Sidesteps DB locking; preserves ordering trivially. |
| 2026-06-07 | Convex pushes to clients; actor does NOT broadcast chat | Avoids duplicate delivery paths. Latency trade is acceptable. |
| 2026-06-07 | Custom Go HTTP client for Convex | No official SDK; HTTP API is small enough to wrap in ~80 lines. |
