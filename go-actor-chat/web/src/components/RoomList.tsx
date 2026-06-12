import { FormEvent, useState } from "react";
import { useMutation, useQuery } from "convex/react";
import { api } from "../../../convex/_generated/api";
import { Id } from "../../../convex/_generated/dataModel";

export default function RoomList({
  userId,
  activeRoomId,
  onSelect,
}: {
  userId: string;
  activeRoomId: string | null;
  onSelect: (roomId: string) => void;
}) {
  const rooms = useQuery(api.rooms.list, { userId: userId as Id<"users"> });
  const createRoom = useMutation(api.rooms.create);
  const addMember = useMutation(api.rooms.addMember);

  const [newName, setNewName] = useState("");
  const [joinId, setJoinId] = useState("");
  const [error, setError] = useState<string | null>(null);

  const handleCreate = async (e: FormEvent) => {
    e.preventDefault();
    const name = newName.trim();
    if (!name) return;
    setError(null);
    try {
      const room = await createRoom({
        name,
        kind: "group",
        userId: userId as Id<"users">,
      });
      setNewName("");
      if (room) onSelect(room._id);
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err));
    }
  };

  const handleJoin = async (e: FormEvent) => {
    e.preventDefault();
    const roomId = joinId.trim();
    if (!roomId) return;
    setError(null);
    try {
      await addMember({
        roomId: roomId as Id<"rooms">,
        userId: userId as Id<"users">,
      });
      setJoinId("");
      onSelect(roomId);
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err));
    }
  };

  return (
    <div className="room-list">
      <nav>
        {rooms === undefined && <p className="muted">Loading rooms…</p>}
        {rooms?.length === 0 && <p className="muted">No rooms yet.</p>}
        {rooms?.map((room) => (
          <button
            key={room._id}
            className={`room-item ${room._id === activeRoomId ? "active" : ""}`}
            onClick={() => onSelect(room._id)}
          >
            # {room.name}
          </button>
        ))}
      </nav>
      <form onSubmit={handleCreate} className="room-form">
        <input
          value={newName}
          onChange={(e) => setNewName(e.target.value)}
          placeholder="New room name"
          maxLength={48}
        />
        <button type="submit" disabled={!newName.trim()}>
          Create
        </button>
      </form>
      <form onSubmit={handleJoin} className="room-form">
        <input
          value={joinId}
          onChange={(e) => setJoinId(e.target.value)}
          placeholder="Join by room ID"
        />
        <button type="submit" disabled={!joinId.trim()}>
          Join
        </button>
      </form>
      {error && <p className="error-text">{error}</p>}
    </div>
  );
}
