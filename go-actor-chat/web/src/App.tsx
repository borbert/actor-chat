import { useCallback, useReducer, useState } from "react";
import { Frame } from "./lib/protocol";
import { Identity, clearIdentity, loadIdentity, saveIdentity } from "./lib/identity";
import { useChatSocket } from "./lib/useChatSocket";
import { chatReducer, initialChatState } from "./lib/chatState";
import NamePrompt from "./components/NamePrompt";
import RoomList from "./components/RoomList";
import ChatRoom from "./components/ChatRoom";

export default function App() {
  const [identity, setIdentity] = useState<Identity | null>(loadIdentity);
  const [activeRoomId, setActiveRoomId] = useState<string | null>(null);
  const [chat, dispatch] = useReducer(chatReducer, initialChatState);

  const onFrame = useCallback((frame: Frame) => {
    dispatch({ kind: "frame", frame });
  }, []);

  const { status, send } = useChatSocket(identity?.userId ?? null, onFrame);

  const sendMessage = useCallback(
    (roomId: string, body: string) => {
      const clientId = crypto.randomUUID();
      dispatch({ kind: "sent", clientId, roomId, body });
      send({ type: "send", roomId, body, clientId });
    },
    [send],
  );

  const handleIdentity = (id: Identity) => {
    saveIdentity(id);
    setIdentity(id);
  };

  const handleSignOut = () => {
    clearIdentity();
    setIdentity(null);
    setActiveRoomId(null);
  };

  if (!identity) {
    return <NamePrompt onIdentity={handleIdentity} />;
  }

  return (
    <div className="app">
      <aside className="sidebar">
        <header className="sidebar-header">
          <h1>Actor Chat</h1>
          <span className={`conn-dot conn-${status}`} title={`socket: ${status}`} />
        </header>
        <RoomList
          userId={identity.userId}
          activeRoomId={activeRoomId}
          onSelect={setActiveRoomId}
        />
        <footer className="sidebar-footer">
          <span className="me">{identity.name}</span>
          <button className="link" onClick={handleSignOut}>
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
