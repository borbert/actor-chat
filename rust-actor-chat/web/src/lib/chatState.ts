import { Frame } from "./protocol";

// Per-room ephemeral state fed by WS frames, plus optimistic sends awaiting
// their Convex subscription update (PRD §10.1).

export type PendingSend = {
  roomId: string;
  body: string;
  at: number;
  status: "pending" | "acked" | "failed";
  reason?: string;
};

export type ChatState = {
  pending: Record<string, PendingSend>; // keyed by clientId
  presence: Record<string, string[]>; // roomId -> online userIds
  typing: Record<string, string[]>; // roomId -> userIds currently typing
};

export const initialChatState: ChatState = {
  pending: {},
  presence: {},
  typing: {},
};

export type ChatAction =
  | { kind: "sent"; clientId: string; roomId: string; body: string }
  | { kind: "frame"; frame: Frame }
  | { kind: "settle"; clientIds: string[] }
  | { kind: "reset" };

export function chatReducer(state: ChatState, action: ChatAction): ChatState {
  switch (action.kind) {
    case "reset":
      return initialChatState;
    case "sent":
      return {
        ...state,
        pending: {
          ...state.pending,
          [action.clientId]: {
            roomId: action.roomId,
            body: action.body,
            at: Date.now(),
            status: "pending",
          },
        },
      };
    case "settle": {
      // Convex delivered these messages; drop the optimistic copies.
      const pending = { ...state.pending };
      let changed = false;
      for (const clientId of action.clientIds) {
        if (clientId in pending) {
          delete pending[clientId];
          changed = true;
        }
      }
      return changed ? { ...state, pending } : state;
    }
    case "frame":
      return applyFrame(state, action.frame);
  }
}

function applyFrame(state: ChatState, frame: Frame): ChatState {
  switch (frame.type) {
    case "ack": {
      const entry = frame.clientId ? state.pending[frame.clientId] : undefined;
      if (!entry) return state;
      return {
        ...state,
        pending: {
          ...state.pending,
          [frame.clientId!]: { ...entry, status: "acked" },
        },
      };
    }
    case "error": {
      const entry = frame.clientId ? state.pending[frame.clientId] : undefined;
      if (!entry) return state;
      return {
        ...state,
        pending: {
          ...state.pending,
          [frame.clientId!]: {
            ...entry,
            status: "failed",
            reason: frame.reason,
          },
        },
      };
    }
    case "presence_update":
      if (!frame.roomId) return state;
      return {
        ...state,
        presence: { ...state.presence, [frame.roomId]: frame.users ?? [] },
      };
    case "typing_update": {
      if (!frame.roomId || !frame.userId) return state;
      const current = state.typing[frame.roomId] ?? [];
      const next = frame.typing
        ? current.includes(frame.userId)
          ? current
          : [...current, frame.userId]
        : current.filter((u) => u !== frame.userId);
      return { ...state, typing: { ...state.typing, [frame.roomId]: next } };
    }
    default:
      return state;
  }
}
