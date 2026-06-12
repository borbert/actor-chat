import { FormEvent, useState } from "react";
import { useMutation } from "convex/react";
import { api } from "../../../convex/_generated/api";
import { Identity } from "../lib/identity";

// Dev-mode sign-in: provisions a Convex user from a display name. Replaced
// by WorkOS AuthKit in M3.
export default function NamePrompt({
  onIdentity,
}: {
  onIdentity: (identity: Identity) => void;
}) {
  const [name, setName] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const getOrCreate = useMutation(api.users.getOrCreateFromAuth);

  const submit = async (e: FormEvent) => {
    e.preventDefault();
    const trimmed = name.trim();
    if (!trimmed) return;
    setBusy(true);
    setError(null);
    try {
      const user = await getOrCreate({
        authId: `dev:${trimmed.toLowerCase()}`,
        displayName: trimmed,
      });
      if (!user) throw new Error("user creation returned nothing");
      onIdentity({ userId: user._id, name: user.displayName });
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err));
      setBusy(false);
    }
  };

  return (
    <div className="prompt-screen">
      <form className="prompt-card" onSubmit={submit}>
        <h1>Actor Chat</h1>
        <p>Pick a display name to join.</p>
        <input
          autoFocus
          value={name}
          onChange={(e) => setName(e.target.value)}
          placeholder="Your name"
          maxLength={32}
        />
        <button type="submit" disabled={busy || !name.trim()}>
          {busy ? "Joining…" : "Join"}
        </button>
        {error && <p className="error-text">{error}</p>}
      </form>
    </div>
  );
}
