// Mirror of the Go WS protocol in internal/ws/protocol.go (PRD §11).
export type Frame = {
  type: string;
  roomId?: string;
  userId?: string;
  body?: string;
  clientId?: string;
  messageId?: string;
  createdAt?: number;
  reason?: string;
  users?: string[];
  typing?: boolean;
};

export const WS_BASE =
  (import.meta.env.VITE_WS_URL as string | undefined) ?? "ws://localhost:8080";
