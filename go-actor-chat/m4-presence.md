# M4: presence and typing indicators

Goal (PRD §9, §10.3, §11): the room actor learns who is *in* the room right
now and fans out `presence_update` and `typing_update` frames. The web side
is already done — `ChatRoom` sends join/leave and typing frames, and
`chatState` applies both update types — so this milestone is almost entirely
Go. When it lands, two browsers in one room see "2 online: …" and a live
typing indicator.

## The actor-model frame

Three ideas worth internalizing this time:

1. **Ephemeral state lives in the actor that owns the conversation.**
   Messages are durable, so they flow through the single writer into Convex.
   Presence is a fact about *connections*, not history — so it lives only in
   the room actor's memory. No table, no TTL cleanup job, no cache
   invalidation. When the actor dies, presence dies with it, and that is
   correct: if the server restarts, nobody is connected.
2. **Broadcast is not infrastructure, it's a loop.** There is no pub/sub
   bus. The room actor holds the PIDs of its member connections and mails
   each one. Because every socket write goes through that connection's
   mailbox, a presence broadcast can never interleave mid-frame with an ack.
3. **Actors decide their own death — and the criteria can grow.** The room
   actor already self-poisons after 5 idle minutes. Now "idle" must also
   mean "empty": an occupied room holds presence state that must not
   evaporate under a quiet lurker. One line in the `idleTick` handler is the
   whole change. Conversely, the *connection* actor cleans up after itself
   on `actor.Stopped` by mailing `Leave` to every room it joined — no
   supervisor watches it, because the actor that holds the `joined` map is
   the one best placed to act on it.

## Design decision: Convex `_id` in presence frames, not the Clerk sub

This was the open question from M3. The answer falls out of the client code:
`ChatRoom.nameOf` maps **`users._id` → displayName**, and the typing filter
compares against `identity.userId`, which is the Convex `_id`. Frames
carrying Clerk subs would force the client to build a second directory
keyed by `authId` — and would broadcast Clerk subject ids to every client
for zero benefit.

So the upgrade handler resolves the identity *once*, by calling the existing
`users:getOrCreateFromAuth` mutation with the user's own token (idempotent —
the web client calls the same mutation at sign-in). The connection then
carries both ids with distinct jobs:

- **AuthID** (Clerk sub) — for *proving*: every token refresh must present
  the same subject.
- **UserID** (Convex `_id`) — for *being seen*: presence, typing, and
  anything else other clients render.

Cost: one Convex round trip per WS connect, before the upgrade. Acceptable;
it also guarantees the user row exists before their presence is ever shown.

Known v1 tradeoffs:

- Presence is best-effort. If a room actor panics and restarts, its member
  set is gone and presence shows empty until clients reconnect and rejoin.
  Fine for v1; revisit in M5 if it bites.
- A `join` can still race the idle self-poison and land in the deadletter —
  same class as the M3 send race. Much rarer now, since occupancy blocks
  poisoning entirely.

---

## 1. `internal/room/messages.go` — the new vocabulary

```go
// Join registers the sending connection actor as a member of the room.
// UserID is the Convex users._id — the identity other clients render.
type Join struct {
	UserID string
}

// Leave removes the sending connection from the room. No UserID: members
// are keyed by connection PID, because one user may have several tabs.
type Leave struct{}

// Typing reports the sender's typing state for fan-out to other members.
type Typing struct {
	UserID string
	Typing bool
}

// PresenceUpdate is broadcast to every member when the online set changes.
// Users is always the full set (PRD §11): idempotent for the client, no
// diff protocol to get wrong.
type PresenceUpdate struct {
	RoomID string
	Users  []string
}

// TypingUpdate fans out one member's typing state to the other members.
type TypingUpdate struct {
	RoomID string
	UserID string
	Typing bool
}
```

**Cleanup while you're here:** delete `Send.UserID`. Since M3 the mutation
derives the sender from the JWT; the field is dead weight that *looks* like
it grants identity but doesn't. (Touches `connection.go` and `room_test.go`.)

## 2. `internal/room/room.go` — membership, typing, broadcast

New state on `Actor`:

```go
	members map[string]member // key: connection PID string (one user may have many tabs)
	typing  map[string]bool   // userIDs currently typing
```

```go
// member is one connected tab: where to mail updates, and who it is.
type member struct {
	pid    *actor.PID
	userID string
}
```

Initialize both maps in the producer. New `Receive` cases (each updates
`a.lastActivity` like `Send` does):

```go
	case Join:
		a.lastActivity = time.Now()
		a.handleJoin(c, msg)
	case Leave:
		a.lastActivity = time.Now()
		a.handleLeave(c)
	case Typing:
		a.lastActivity = time.Now()
		a.handleTyping(c, msg)
```

The one-line lifecycle change — an occupied room never self-poisons:

```go
	case idleTick:
		if len(a.members) == 0 && time.Since(a.lastActivity) > idleTimeout {
```

Handlers. All of them rely on `c.Sender()` — the connection actor sent with
`c.Send`, so its PID rides along, and the room never needs to be *told* who
to reply to:

```go
func (a *Actor) handleJoin(c *actor.Context, msg Join) {
	sender := c.Sender()
	if sender == nil {
		return
	}
	a.members[sender.String()] = member{pid: sender, userID: msg.UserID}
	a.broadcastPresence(c)
}

func (a *Actor) handleLeave(c *actor.Context) {
	sender := c.Sender()
	if sender == nil {
		return
	}
	m, ok := a.members[sender.String()]
	if !ok {
		return
	}
	delete(a.members, sender.String())
	// If that was the user's last tab, their typing state must not linger.
	if a.typing[m.userID] && !a.online(m.userID) {
		delete(a.typing, m.userID)
		a.broadcast(c, TypingUpdate{RoomID: a.roomID, UserID: m.userID}, "")
	}
	a.broadcastPresence(c)
}

func (a *Actor) handleTyping(c *actor.Context, msg Typing) {
	if msg.Typing {
		a.typing[msg.UserID] = true
	} else {
		delete(a.typing, msg.UserID)
	}
	// Skip the sender's own tab; the client filters self anyway.
	exclude := ""
	if s := c.Sender(); s != nil {
		exclude = s.String()
	}
	a.broadcast(c, TypingUpdate{RoomID: a.roomID, UserID: msg.UserID, Typing: msg.Typing}, exclude)
}
```

Helpers:

```go
// online reports whether any connected tab belongs to userID.
func (a *Actor) online(userID string) bool {
	for _, m := range a.members {
		if m.userID == userID {
			return true
		}
	}
	return false
}

// presence returns the deduplicated, sorted set of online userIDs.
// Sorted so broadcasts are deterministic (and so are tests).
func (a *Actor) presence() []string {
	set := make(map[string]bool, len(a.members))
	for _, m := range a.members {
		set[m.userID] = true
	}
	users := make([]string, 0, len(set))
	for u := range set {
		users = append(users, u)
	}
	sort.Strings(users)
	return users
}

func (a *Actor) broadcastPresence(c *actor.Context) {
	a.broadcast(c, PresenceUpdate{RoomID: a.roomID, Users: a.presence()}, "")
}

// broadcast mails msg to every member except the PID keyed by exclude
// ("" excludes nobody). Fan-out is just a loop over PIDs — each member's
// connection actor serializes the actual socket write.
func (a *Actor) broadcast(c *actor.Context, msg any, exclude string) {
	for key, m := range a.members {
		if key == exclude {
			continue
		}
		c.Send(m.pid, msg)
	}
}
```

Note the join broadcast goes to *everyone including the joiner* — that is
how the joiner gets its initial presence snapshot. No separate "sync" reply
needed; the regular update already carries the full set.

## 3. `internal/ws/connection.go`

The connection now carries two ids with different jobs, so bundle them:

```go
// Identity is what the upgrade handler established about the peer.
// AuthID proves (token refreshes must present the same Clerk sub);
// UserID is seen (presence and typing render against users._id).
type Identity struct {
	UserID string // Convex users._id
	AuthID string // Clerk JWT subject
	Token  string // current JWT lease, swapped on ping refresh
}
```

- `Connection` replaces `userID string`/`token string` with `id Identity`
  (keep `validate` as is). `NewConnection(conn *websocket.Conn, id Identity,
  validate func(string) (string, error), registry *room.Registry)`.
- Token refresh check becomes `sub != cn.id.AuthID`; the swap is
  `cn.id.Token = f.Token`; `Send` carries `cn.id.Token`.
- `actor.Stopped` — leave every joined room *before* closing the socket:

```go
	case actor.Stopped:
		for roomID := range cn.joined {
			c.Send(cn.registry.PIDFor(roomID), room.Leave{})
		}
		_ = cn.conn.Close(websocket.StatusNormalClosure, "server closing connection")
```

- The join/leave/typing frame cases replace their M4 placeholder comments:

```go
	case TypeJoin:
		if f.RoomID == "" {
			cn.write(Frame{Type: TypeError, Reason: "join requires roomId"})
			return
		}
		if cn.joined[f.RoomID] {
			return // React effects re-fire; join once
		}
		cn.joined[f.RoomID] = true
		c.Send(cn.registry.PIDFor(f.RoomID), room.Join{UserID: cn.id.UserID})
	case TypeLeave:
		if !cn.joined[f.RoomID] {
			return
		}
		delete(cn.joined, f.RoomID)
		c.Send(cn.registry.PIDFor(f.RoomID), room.Leave{})
	case TypeTypingStart, TypeTypingStop:
		if !cn.joined[f.RoomID] {
			return // not a member: nothing to fan out, not worth an error
		}
		c.Send(cn.registry.PIDFor(f.RoomID), room.Typing{
			UserID: cn.id.UserID,
			Typing: f.Type == TypeTypingStart,
		})
```

- And two new `Receive` cases, mirroring `room.SendAck`:

```go
	case room.PresenceUpdate:
		cn.write(Frame{Type: TypePresenceUpdate, RoomID: msg.RoomID, Users: msg.Users})
	case room.TypingUpdate:
		cn.write(Frame{Type: TypeTypingUpdate, RoomID: msg.RoomID, UserID: msg.UserID, Typing: msg.Typing})
```

Wire note: `Frame.Typing` is `omitempty`, so a stop frame serializes without
the field. That's fine — the client reducer treats a missing `typing` as
falsy and removes the user.

## 4. `internal/server/auth.go` — resolve who they are, once

`auth.go` already owns "who are you" at the boundary; user provisioning
belongs next to JWT verification:

```go
// ProvisionUser exchanges a validated token for the caller's Convex
// users._id, creating the row on first contact. Idempotent — the web
// client calls the same mutation at sign-in (PRD §11, §13).
func ProvisionUser(client *convex.Client) func(ctx context.Context, token string) (string, error) {
	return func(ctx context.Context, token string) (string, error) {
		var user struct {
			ID string `json:"_id"`
		}
		if err := client.WithAuth(token).Mutation(ctx, "users:getOrCreateFromAuth", map[string]any{}, &user); err != nil {
			return "", fmt.Errorf("users:getOrCreateFromAuth: %w", err)
		}
		if user.ID == "" {
			return "", fmt.Errorf("users:getOrCreateFromAuth returned no _id")
		}
		return user.ID, nil
	}
}
```

(Returned as a func, not a method on a new type: the `Server` stores it as
`resolveUser func(ctx context.Context, token string) (string, error)`, which
the test harness can stub without Convex — same trick as `validate` in M3.)

## 5. `internal/server/server.go` + `ws.go`

`server.go` — add the field and wire it inside the existing `CONVEX_URL`
block (it needs `s.convex`):

```go
	resolveUser func(ctx context.Context, token string) (string, error)
```

```go
		s.rooms = room.NewRegistry(eng, room.NewConvexStore(s.convex))
		s.resolveUser = ProvisionUser(s.convex)
```

`ws.go` — between `Validate` and `Accept` (it must run before the upgrade,
while returning an HTTP error is still possible):

```go
	sub, err := s.auth.Validate(token)
	if err != nil { /* unchanged 401 */ }

	userID, err := s.resolveUser(c.Request().Context(), token)
	if err != nil {
		slog.Error("ws user resolution failed", "err", err)
		return echo.NewHTTPError(http.StatusServiceUnavailable, "user lookup failed")
	}

	// ... Accept unchanged ...

	pid := s.engine.Spawn(ws.NewConnection(conn, ws.Identity{
		UserID: userID,
		AuthID: sub,
		Token:  token,
	}, s.auth.Validate, s.rooms), "conn")
```

(503, not 401: the token was valid; Convex was not reachable or misbehaving.)

## 6. Web client

**No changes.** `ChatRoom` already joins/leaves per room, `MessageInput`
already debounces typing with a 3s idle stop, and `chatState` already
applies both frame types. That's the payoff of having designed the protocol
in the PRD up front. (`bun run build` in the DoD is just a drift check.)

## 7. Tests

`internal/room/room_test.go` — broadcasts need observable inboxes. Add a
probe actor and use `engine.SendWithSender` so the room sees a sender PID
without a real connection:

```go
// probe records every message it receives, standing in for a connection.
type probe struct {
	mu  sync.Mutex
	got []any
}

func (p *probe) Receive(c *actor.Context) {
	switch c.Message().(type) {
	case actor.Started, actor.Stopped, actor.Initialized:
		return
	}
	p.mu.Lock()
	p.got = append(p.got, c.Message())
	p.mu.Unlock()
}
```

Spawn each probe, keep its PID, and drive the room with
`eng.SendWithSender(roomPID, room.Join{UserID: "u1"}, probePID)`. Broadcasts
are async, so assert with a poll-until-deadline helper (`waitFor(t, func()
bool)` with ~2s timeout), not a single read. Cases:

1. Two users join → both probes eventually hold a `PresenceUpdate` with
   `Users: ["cv1", "cv2"]` (sorted).
2. Same user joins from a second probe (two tabs) → presence still lists
   them once; leaving one tab keeps them present.
3. `Typing{Typing: true}` from A → B receives `TypingUpdate`; A does not
   (count A's TypingUpdates: zero).
4. A leaves while typing → B receives `TypingUpdate{Typing: false}`, then
   a `PresenceUpdate` without A.

`internal/server/ws_test.go` — the harness `Server` literal gains a stub
resolver that derives a fake Convex id from the validated sub:

```go
	s := &Server{echo: e, engine: eng, rooms: room.NewRegistry(eng, fakeStore{}), auth: auth.validator}
	s.resolveUser = func(_ context.Context, token string) (string, error) {
		sub, err := auth.validator.Validate(token)
		return "cv_" + sub, err
	}
```

End-to-end cases (two dialed connections, users u1 and u2):

1. Both join `r1` → reading frames on conn2 eventually yields a
   `presence_update` with `["cv_u1", "cv_u2"]` (conn1 may see an
   intermediate single-user update first — read until match or timeout).
2. conn1 sends `typing_start` → conn2 reads `typing_update` with
   `userId: "cv_u1"`, `typing: true`.
3. Not echoed to sender: after step 2, conn1 sends a `ping` and its next
   frame is the `pong` — no typing frame snuck in before it.
4. conn1 closes → conn2 eventually reads a `presence_update` of just
   `["cv_u2"]` (this proves the `actor.Stopped` → `Leave` path).

## 8. Definition of done

- [ ] `go build ./... && go vet ./... && go test ./...` green;
      `bun run build` green in `web/`.
- [ ] Two browsers, same room: header shows "2 online" with both names.
- [ ] Typing in one browser → indicator in the other within a beat; it
      clears ~3s after you stop, and immediately when you hit send.
- [ ] Close one tab → the other's presence count drops.
- [ ] Same user in two tabs → counted once; closing one tab keeps them
      online.
- [ ] Lurk >5 min in an open room, then have the other browser type →
      presence and typing still work (occupied room didn't self-poison).
- [ ] Leave a room empty >5 min → server logs "room actor idle,
      self-poisoning" (eviction still works for empty rooms).

Ping me when it's in and I'll review the diff.
