import { useCallback, useEffect, useRef, useState } from "react";
import { Frame, WS_BASE } from "./protocol";

export type SocketStatus = "connecting" | "open" | "closed";

const PING_INTERVAL_MS = 25_000;
const MAX_BACKOFF_MS = 10_000;

export function useChatSocket(
  getToken: (() => Promise<string | null>) | null,
  onFrame: (frame: Frame) => void,
) {
  const [status, setStatus] = useState<SocketStatus>("closed");
  const wsRef = useRef<WebSocket | null>(null);
  const onFrameRef = useRef(onFrame);
  onFrameRef.current = onFrame;

  useEffect(() => {
    if (!getToken) return;

    let disposed = false;
    let attempt = 0;
    let pingTimer: ReturnType<typeof setInterval> | undefined;
    let reconnectTimer: ReturnType<typeof setTimeout> | undefined;

    const scheduleReconnect = () => {
      if (disposed) return;
      const backoff = Math.min(1000 * 2 ** attempt, MAX_BACKOFF_MS);
      attempt += 1;
      reconnectTimer = setTimeout(() => void connect(), backoff);
    };

    const connect = async () => {
      setStatus("connecting");
      const token = await getToken();
      if (disposed) return;
      if (!token) {
        setStatus("closed");
        scheduleReconnect();
        return;
      }

      const ws = new WebSocket(
        `${WS_BASE}/ws?token=${encodeURIComponent(token)}`,
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
        scheduleReconnect();
      };
    };

    void connect();
    return () => {
      disposed = true;
      clearInterval(pingTimer);
      clearTimeout(reconnectTimer);
      wsRef.current?.close();
      wsRef.current = null;
    };
  }, [getToken]);

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
