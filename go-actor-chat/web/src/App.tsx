import { useCallback, useEffect, useReducer, useRef, useState } from "react";
import { Show, UserButton, useAuth, useUser } from "@clerk/react";
import { useConvexAuth, useMutation } from "convex/react";
import { api } from "../../convex/_generated/api";
import {
  IMPLEMENTATIONS,
  clearImplementation,
  loadImplementation,
  saveImplementation,
  type ImplementationId,
} from "./lib/backends";
import { Frame } from "./lib/protocol";
import { useChatSocket } from "./lib/useChatSocket";
import { chatReducer, initialChatState } from "./lib/chatState";
import SignIn from "./components/SignIn";
import ImplementationPicker from "./components/ImplementationPicker";
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
  const [implementationId, setImplementationId] =
    useState<ImplementationId | null>(() => loadImplementation());
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

  const selectImplementation = useCallback((id: ImplementationId) => {
    saveImplementation(id);
    setImplementationId(id);
  }, []);

  const changeImplementation = useCallback(() => {
    clearImplementation();
    setImplementationId(null);
    setActiveRoomId(null);
    dispatch({ kind: "reset" });
  }, []);

  // Token for the actor WebSocket. Uses the same "convex" template so both
  // servers can validate one token shape against Clerk's JWKS.
  const getSocketToken = useCallback(
    () => getToken({ template: "convex" }).catch(() => null),
    [getToken],
  );

  const onFrame = useCallback((frame: Frame) => {
    dispatch({ kind: "frame", frame });
  }, []);

  const implementation = implementationId
    ? IMPLEMENTATIONS[implementationId]
    : null;
  const wsBase = implementation?.wsUrl ?? null;

  const { status, send, backend } = useChatSocket(
    identity && wsBase ? getSocketToken : null,
    onFrame,
    wsBase,
  );

  const sendMessage = useCallback(
    (roomId: string, body: string) => {
      const clientId = crypto.randomUUID();
      dispatch({ kind: "sent", clientId, roomId, body });
      if (!send({ type: "send", roomId, body, clientId })) {
        // Socket dropped between render and click; fail the optimistic
        // bubble instead of leaving it "sending…" forever.
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

  if (!implementation) {
    return <ImplementationPicker onSelect={selectImplementation} />;
  }

  const badgeLabel =
    backend?.server === "rust-actor-chat"
      ? "Rust"
      : backend?.server === "zig-actor-chat"
        ? "Zig"
        : backend?.server === "go-actor-chat"
          ? "Go"
          : backend?.server
            ? backend.server
            : implementation.label;

  return (
    <div className="app">
      <aside className="sidebar">
        <header className="sidebar-header">
          <h1>Actor Chat</h1>
          <button
            type="button"
            className="backend-badge"
            title={
              backend
                ? `${backend.server} v${backend.version} — click to switch`
                : `${implementation.label} (${implementation.wsUrl}) — click to switch`
            }
            onClick={changeImplementation}
          >
            {badgeLabel}
          </button>
          <span
            className={`conn-dot conn-${status}`}
            title={`socket: ${status}`}
          />
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
