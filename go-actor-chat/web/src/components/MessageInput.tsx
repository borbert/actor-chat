import { FormEvent, useRef, useState } from "react";

const TYPING_IDLE_MS = 3000;

export default function MessageInput({
  disabled,
  onSend,
  onTyping,
}: {
  disabled: boolean;
  onSend: (body: string) => void;
  onTyping: (typing: boolean) => void;
}) {
  const [body, setBody] = useState("");
  const typingRef = useRef(false);
  const idleTimer = useRef<ReturnType<typeof setTimeout>>(undefined);

  const stopTyping = () => {
    if (typingRef.current) {
      typingRef.current = false;
      onTyping(false);
    }
    clearTimeout(idleTimer.current);
  };

  const handleChange = (value: string) => {
    setBody(value);
    if (!typingRef.current && value.length > 0) {
      typingRef.current = true;
      onTyping(true);
    }
    clearTimeout(idleTimer.current);
    idleTimer.current = setTimeout(stopTyping, TYPING_IDLE_MS);
  };

  const submit = (e: FormEvent) => {
    e.preventDefault();
    const trimmed = body.trim();
    if (!trimmed) return;
    onSend(trimmed);
    setBody("");
    stopTyping();
  };

  return (
    <form className="message-input" onSubmit={submit}>
      <input
        value={body}
        onChange={(e) => handleChange(e.target.value)}
        placeholder={disabled ? "Connecting…" : "Type a message"}
        disabled={disabled}
        maxLength={4000}
      />
      <button type="submit" disabled={disabled || !body.trim()}>
        Send
      </button>
    </form>
  );
}
