import { useCallback, useEffect, useReducer, useState } from "react";
import { useAuth } from "@workos-inc/authkit-react";
import { useConvexAuth, useMutation } from "convex/react";
import { api } from "../../convex/_generated/api";
import { Frame } from "./lib/protocol";
import { useChatSocket } from "./lib/useChatSocket";
import { chatReducer, initialChatState } from "./lib/chatState";
import SignIn from "./components/SignIn";
import RoomList from "./components/RoomList";
import ChatRoom from "./components/ChatRoom";

export type Identity = {
  userId: string;
  name: string;
};

export default function App() {
  const { user, isLoading, signOut, getAccessToken } = useAuth();
  const { isAuthenticated } = useConvexAuth();
  const getOrCreate = useMutation(api.users.getOrCreateFromAuth);

  const [identity, setIdentity] = useState<Identity | null>(null);
  const [activeRoomId, setActiveRoomId] = useState<string | null>(null);
  const [chat, dispatch] = useReducer(chatReducer, initialChatState);

  useEffect(() => {
    if (!user || !isAuthenticated || identity) return;
    let cancelled = false;
    getOrCreate({ displayName: user.firstName ?? user.email ?? undefined })
      .then((u) => {
        if (!cancelled && u) {
          setIdentity({ userId: u._id, name: u.displayName });
        }
      })
      .catch((err) => console.error("user provisioning failed", err));
    return () => {
      cancelled = true;
    };
  }, [user, isAuthenticated, identity, getOrCreate]);

  const onFrame = useCallback((frame: Frame) => {
    dispatch({ kind: "frame", frame });
  }, []);

  const getToken = useCallback(
    () => getAccessToken().catch(() => null),
    [getAccessToken],
  );

  const { status, send } = useChatSocket(identity ? getToken : null, onFrame);

  const sendMessage = useCallback(
    (roomId: string, body: string) => {
      const clientId = crypto.randomUUID();
      dispatch({ kind: "sent", clientId, roomId, body });
      send({ type: "send", roomId, body, clientId });
    },
    [send],
  );

  if (isLoading) {
    return (
      <div className="prompt-screen">
        <p className="muted">Loading…</p>
      </div>
    );
  }
  if (!user) {
    return <SignIn />;
  }
  if (!identity) {
    return (
      <div className="prompt-screen">
        <p className="muted">Setting up your account…</p>
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
          <button className="link" onClick={() => void signOut()}>
            sign out
          </button>
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
