# Migrating auth: WorkOS AuthKit → Clerk

Reference docs:
- Clerk React (Vite) quickstart: https://clerk.com/docs/react/getting-started/quickstart
- Convex + Clerk integration: https://docs.convex.dev/auth/clerk

Why this is a small migration: auth is a **boundary concern**. The actor core
(RoomActor, ConnectionActor, registry) never sees WorkOS or Clerk — it only
sees messages carrying an identity that was validated at the edge. Swapping
providers means swapping the edge: the React provider tree, the Convex JWT
validator config, and (later, M3) the Go WS upgrade check. Nothing inside a
mailbox changes.

---

## 0. Clerk dashboard setup (one-time, manual)

1. Create an application at https://dashboard.clerk.com (name: `actor-chat`).
2. **JWT template**: Configure → JWT Templates → New template → choose the
   **Convex** preset. Keep the name exactly `convex` — Convex's
   `ConvexProviderWithClerk` requests `getToken({ template: "convex" })`.
3. Copy two values:
   - **Publishable key**: API keys page → framework **React**.
   - **Issuer domain** (a.k.a. Frontend API URL), e.g.
     `https://verb-noun-00.clerk.accounts.dev` — shown on the JWT template
     page as Issuer.

## 1. Dependencies (run in `web/`)

```bash
bun remove @workos-inc/authkit-react @convex-dev/workos
bun add @clerk/react@latest
```

Note the package is `@clerk/react` — the older `@clerk/clerk-react` is its
deprecated predecessor; don't install that one.

## 2. Environment

`web/.env.local` — remove all `VITE_WORKOS_*` lines, add:

```bash
VITE_CLERK_PUBLISHABLE_KEY=pk_test_...   # from step 0
```

(Keep `VITE_CONVEX_URL` and `VITE_WS_URL` as they are. The `VITE_` prefix is
what exposes the var to client code; `.env.local` keeps it out of git.)

Convex deployment env (run from repo root):

```bash
bunx convex env set CLERK_JWT_ISSUER_DOMAIN https://verb-noun-00.clerk.accounts.dev
bunx convex env remove WORKOS_CLIENT_ID
```

Mirror the same line in `web/.env.example` so the example stays honest.

## 3. `convex/auth.config.ts` — replace entirely

```typescript
// Clerk issues JWTs from the app's Frontend API domain. The "convex" JWT
// template (Clerk dashboard) sets aud to "convex", which is what
// applicationID matches against. https://docs.convex.dev/auth/clerk
export default {
  providers: [
    {
      domain: process.env.CLERK_JWT_ISSUER_DOMAIN,
      applicationID: "convex",
    },
  ],
};
```

Then push it: `bunx convex dev` (leave it running; it deploys on save).

## 4. `web/src/main.tsx` — swap the provider tree

`ClerkProvider` reads `VITE_CLERK_PUBLISHABLE_KEY` from the environment
itself — do **not** pass a `publishableKey` prop.

```typescript
import { StrictMode } from "react";
import { createRoot } from "react-dom/client";
import { ClerkProvider, useAuth } from "@clerk/react";
import { ConvexProviderWithClerk } from "convex/react-clerk";
import { ConvexReactClient } from "convex/react";
import App from "./App";
import "./styles.css";

const convexUrl = import.meta.env.VITE_CONVEX_URL as string | undefined;
if (!convexUrl) {
  throw new Error("VITE_CONVEX_URL is required (see web/.env.example)");
}

const convex = new ConvexReactClient(convexUrl);

createRoot(document.getElementById("root")!).render(
  <StrictMode>
    <ClerkProvider afterSignOutUrl="/">
      <ConvexProviderWithClerk client={convex} useAuth={useAuth}>
        <App />
      </ConvexProviderWithClerk>
    </ClerkProvider>
  </StrictMode>,
);
```

Things that disappear vs. the WorkOS version: redirect URI handling,
`onRedirectCallback`, the `/callback` route concern. Clerk's modal/hosted
flow manages its own redirects.

## 5. `web/src/App.tsx` — delete the WorkOS scar tissue

This file earned a lot of diagnostic code for WorkOS session leakage
(`tokenDiag`, `issuerClientId`, `clearWorkOSSession`, the issuer-mismatch and
missing-aud screens). **All of it goes.** Target shape:

```typescript
import { useCallback, useEffect, useReducer, useRef, useState } from "react";
import { Show, UserButton, useAuth, useUser } from "@clerk/react";
import { useConvexAuth, useMutation } from "convex/react";
import { api } from "../../convex/_generated/api";
import { Frame } from "./lib/protocol";
import { useChatSocket } from "./lib/useChatSocket";
import { chatReducer, initialChatState } from "./lib/chatState";
import SignIn from "./components/SignIn";
import RoomList from "./components/RoomList";
import ChatRoom from "./components/ChatRoom";

export type Identity = {
  userId: string; // Convex users._id (NOT the Clerk user id)
  name: string;
};

export default function App() {
  return (
    <>
      <Show when="signed-out">
        <SignIn />
      </Show>
      <Show when="signed-in">
        <AuthedApp />
      </Show>
    </>
  );
}

function AuthedApp() {
  const { user } = useUser();
  const { getToken } = useAuth();
  const { isAuthenticated, isLoading: convexAuthLoading } = useConvexAuth();
  const getOrCreate = useMutation(api.users.getOrCreateFromAuth);

  const [identity, setIdentity] = useState<Identity | null>(null);
  const [provisionError, setProvisionError] = useState<string | null>(null);
  const provisioningRef = useRef(false);
  const [activeRoomId, setActiveRoomId] = useState<string | null>(null);
  const [chat, dispatch] = useReducer(chatReducer, initialChatState);

  // Provision the Convex user row once Convex accepts the Clerk JWT
  // (PRD §13: users:getOrCreateFromAuth on first authenticated connection).
  const provision = useCallback(async () => {
    if (identity || provisioningRef.current) return;
    provisioningRef.current = true;
    setProvisionError(null);
    try {
      const u = await getOrCreate({
        displayName:
          user?.firstName ??
          user?.primaryEmailAddress?.emailAddress ??
          undefined,
      });
      if (!u) throw new Error("User provisioning returned no data");
      setIdentity({ userId: u._id, name: u.displayName });
    } catch (err) {
      setProvisionError(err instanceof Error ? err.message : String(err));
    } finally {
      provisioningRef.current = false;
    }
  }, [identity, getOrCreate, user]);

  useEffect(() => {
    if (identity || convexAuthLoading || !isAuthenticated) return;
    void provision();
  }, [identity, convexAuthLoading, isAuthenticated, provision]);

  // Token for the Go WebSocket. Use the same "convex" template so M3's Go
  // middleware validates one token shape against Clerk's JWKS.
  const getSocketToken = useCallback(
    () => getToken({ template: "convex" }).catch(() => null),
    [getToken],
  );

  const onFrame = useCallback((frame: Frame) => {
    dispatch({ kind: "frame", frame });
  }, []);

  const { status, send } = useChatSocket(identity ? getSocketToken : null, onFrame);

  const sendMessage = useCallback(
    (roomId: string, body: string) => {
      const clientId = crypto.randomUUID();
      dispatch({ kind: "sent", clientId, roomId, body });
      if (!send({ type: "send", roomId, body, clientId })) {
        dispatch({
          kind: "frame",
          frame: { type: "error", clientId, reason: "not connected" },
        });
      }
    },
    [send],
  );

  if (!identity) {
    return (
      <div className="prompt-screen">
        <div className="prompt-card">
          <p className="muted">
            {convexAuthLoading || !isAuthenticated
              ? "Connecting to Convex…"
              : "Setting up your account…"}
          </p>
          {provisionError && (
            <>
              <p className="error-text">{provisionError}</p>
              <button className="link" onClick={() => void provision()}>
                Try again
              </button>
            </>
          )}
        </div>
      </div>
    );
  }

  return (
    <div className="app">
      <aside className="sidebar">
        <header className="sidebar-header">
          <h1>Actor Chat</h1>
          <span className={`conn-dot conn-${status}`} title={`socket: ${status}`} />
        </header>
        <RoomList activeRoomId={activeRoomId} onSelect={setActiveRoomId} />
        <footer className="sidebar-footer">
          <span className="me">{identity.name}</span>
          <UserButton />
        </footer>
      </aside>
      <main className="main">
        {activeRoomId ? (
          <ChatRoom
            key={activeRoomId}
            roomId={activeRoomId}
            identity={identity}
            socketStatus={status}
            sendFrame={send}
            sendMessage={sendMessage}
            chat={chat}
            settle={(clientIds) => dispatch({ kind: "settle", clientIds })}
          />
        ) : (
          <div className="empty-state">
            <p>Select or create a room to start chatting.</p>
          </div>
        )}
      </main>
    </div>
  );
}
```

Deliberate choices to preserve:
- `Identity.userId` stays the **Convex `users._id`** (what `ChatRoom` compares
  message authorship against), not the Clerk user id. The Clerk `sub` lands in
  `users.authId` via `getOrCreateFromAuth` — same as WorkOS before it.
- The optimistic-send failure dispatch (added today) stays.

## 6. `web/src/components/SignIn.tsx` — replace entirely

```typescript
import { SignInButton, SignUpButton } from "@clerk/react";

export default function SignIn() {
  return (
    <div className="prompt-screen">
      <div className="prompt-card">
        <h1>Actor Chat</h1>
        <p>Sign in to join the conversation.</p>
        <div className="auth-buttons">
          <SignInButton mode="modal" />
          <SignUpButton mode="modal" />
        </div>
      </div>
    </div>
  );
}
```

(The old `oauthError()` query-param parsing was WorkOS-specific — gone.)

## 7. Untouched on purpose

- `convex/schema.ts`, `convex/users.ts`, `convex/lib/identity.ts` — they only
  know `identity.subject`; Clerk's `sub` slots straight in.
- `convex/messages.ts`, `convex/rooms.ts` — row-level checks unchanged.
- All of `internal/` (Go) — the WS send path is still pending its M3 server
  half. When we do it, Go validates the same Clerk template token against
  `https://<issuer-domain>/.well-known/jwks.json`. Migrating providers now
  means M3 is written once, for Clerk only.
- `useChatSocket`, `chatState`, `protocol` — provider-agnostic already.

## 8. Definition of done

- [ ] `bun run build` passes in `web/` (catches dead WorkOS imports).
- [ ] `bunx convex dev` deployed the new `auth.config.ts` without errors.
- [ ] Fresh browser profile: sign up via Clerk modal → lands in the app with
      your display name in the sidebar footer.
- [ ] Convex dashboard → Data → `users`: one row, `authId` starts with
      `user_` (Clerk id format).
- [ ] Create a room; messages still render from Convex subscription.
- [ ] `rg -i workos web/src convex` returns nothing.
- [ ] The socket dot will stay red — expected until M3's Go half lands.

When you're done, ping me and I'll review the diff.
