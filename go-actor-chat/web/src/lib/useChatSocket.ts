import { useCallback, useEffect, useRef, useState } from "react";
import { Frame, WS_BASE } from "./protocol";

export type SocketStatus = "connecting" | "open" | "closed";

const PING_INTERVAL_MS = 25_000;
const MAX_BACKOFF_MS = 10_000;

// useChatSocket maintains one app WebSocket to the Go server with automatic
// reconnect (PRD §10.4) and an app-level ping to keep the 60s read deadline
// fresh. Inbound frames are delivered to onFrame.
export function useChatSocket(
  userId: string | null,
  onFrame: (frame: Frame) => void,
) {
  const [status, setStatus] = useState<SocketStatus>("closed");
  const wsRef = useRef<WebSocket | null>(null);
  const onFrameRef = useRef(onFrame);
  onFrameRef.current = onFrame;

  useEffect(() => {
    if (!userId) return;

    let disposed = false;
    let attempt = 0;
    let pingTimer: ReturnType<typeof setInterval> | undefined;
    let reconnectTimer: ReturnType<typeof setTimeout> | undefined;

    const connect = () => {
      setStatus("connecting");
      const ws = new WebSocket(
        `${WS_BASE}/ws?user=${encodeURIComponent(userId)}`,
      );
      wsRef.current = ws;

      ws.onopen = () => {
        attempt = 0;
        setStatus("open");
        pingTimer = setInterval(() => {
          if (ws.readyState === WebSocket.OPEN) {
            ws.send(JSON.stringify({ type: "ping" }));
          }
        }, PING_INTERVAL_MS);
      };

      ws.onmessage = (event) => {
        try {
          onFrameRef.current(JSON.parse(event.data as string) as Frame);
        } catch {
          // Ignore malformed frames from the server.
        }
      };

      ws.onclose = () => {
        clearInterval(pingTimer);
        setStatus("closed");
        if (!disposed) {
          const backoff = Math.min(1000 * 2 ** attempt, MAX_BACKOFF_MS);
          attempt += 1;
          reconnectTimer = setTimeout(connect, backoff);
        }
      };
    };

    connect();
    return () => {
      disposed = true;
      clearInterval(pingTimer);
      clearTimeout(reconnectTimer);
      wsRef.current?.close();
      wsRef.current = null;
    };
  }, [userId]);

  const send = useCallback((frame: Frame) => {
    const ws = wsRef.current;
    if (ws && ws.readyState === WebSocket.OPEN) {
      ws.send(JSON.stringify(frame));
      return true;
    }
    return false;
  }, []);

  return { status, send };
}
