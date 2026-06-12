import { useEffect, useMemo, useRef } from "react";
import { usePaginatedQuery, useQuery } from "convex/react";
import { api } from "../../../convex/_generated/api";
import { Id } from "../../../convex/_generated/dataModel";
import { Frame } from "../lib/protocol";
import { Identity } from "../App";
import { ChatState } from "../lib/chatState";
import { SocketStatus } from "../lib/useChatSocket";
import MessageInput from "./MessageInput";

export default function ChatRoom({
  roomId,
  identity,
  socketStatus,
  sendFrame,
  sendMessage,
  chat,
  settle,
}: {
  roomId: string;
  identity: Identity;
  socketStatus: SocketStatus;
  sendFrame: (frame: Frame) => boolean;
  sendMessage: (roomId: string, body: string) => void;
  chat: ChatState;
  settle: (clientIds: string[]) => void;
}) {
  const room = useQuery(api.rooms.get, { roomId: roomId as Id<"rooms"> });
  const users = useQuery(api.users.list) ?? [];
  const { results: messages } = usePaginatedQuery(
    api.messages.list,
    { roomId: roomId as Id<"rooms"> },
    { initialNumItems: 50 },
  );

  const nameOf = useMemo(() => {
    const byId = new Map(users.map((u) => [u._id as string, u.displayName]));
    return (userId: string) => byId.get(userId) ?? "unknown";
  }, [users]);

  // Announce presence for this room while it is open (PRD §10.3).
  useEffect(() => {
    if (socketStatus !== "open") return;
    sendFrame({ type: "join", roomId });
    return () => {
      sendFrame({ type: "leave", roomId });
    };
  }, [roomId, socketStatus, sendFrame]);

  // Drop optimistic copies once Convex delivers the real rows (PRD §10.1).
  useEffect(() => {
    const delivered = messages
      .map((m) => m.clientId)
      .filter((clientId) => clientId in chat.pending);
    if (delivered.length > 0) settle(delivered);
  }, [messages, chat.pending, settle]);

  // messages arrive newest-first; render oldest-first.
  const ordered = useMemo(() => [...messages].reverse(), [messages]);
  const pendingHere = Object.entries(chat.pending)
    .filter(([, p]) => p.roomId === roomId)
    .sort(([, a], [, b]) => a.at - b.at);

  const bottomRef = useRef<HTMLDivElement>(null);
  useEffect(() => {
    bottomRef.current?.scrollIntoView({ behavior: "smooth" });
  }, [ordered.length, pendingHere.length]);

  const online = chat.presence[roomId] ?? [];
  const typing = (chat.typing[roomId] ?? []).filter(
    (u) => u !== identity.userId,
  );

  return (
    <div className="chat-room">
      <header className="chat-header">
        <div>
          <h2># {room?.name ?? "…"}</h2>
          <button
            className="link"
            onClick={() => navigator.clipboard.writeText(roomId)}
            title="Copy room ID to invite others"
          >
            copy room ID
          </button>
        </div>
        {online.length > 0 && (
          <span className="online">
            {online.length} online: {online.map(nameOf).join(", ")}
          </span>
        )}
      </header>

      <div className="messages">
        {ordered.map((m) => (
          <div
            key={m._id}
            className={`msg ${m.userId === identity.userId ? "mine" : ""}`}
          >
            <span className="msg-author">{nameOf(m.userId)}</span>
            <span className="msg-body">{m.body}</span>
            <span className="msg-time">
              {new Date(m.createdAt).toLocaleTimeString()}
            </span>
          </div>
        ))}
        {pendingHere.map(([clientId, p]) => (
          <div key={clientId} className={`msg mine pending-${p.status}`}>
            <span className="msg-author">{identity.name}</span>
            <span className="msg-body">{p.body}</span>
            <span className="msg-time">
              {p.status === "failed" ? `failed: ${p.reason ?? "error"}` : "sending…"}
            </span>
          </div>
        ))}
        <div ref={bottomRef} />
      </div>

      {typing.length > 0 && (
        <p className="typing-indicator">
          {typing.map(nameOf).join(", ")} {typing.length === 1 ? "is" : "are"} typing…
        </p>
      )}

      <MessageInput
        disabled={socketStatus !== "open"}
        onSend={(body) => sendMessage(roomId, body)}
        onTyping={(isTyping) =>
          sendFrame({
            type: isTyping ? "typing_start" : "typing_stop",
            roomId,
          })
        }
      />
    </div>
  );
}
