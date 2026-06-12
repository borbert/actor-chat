import { useAuth } from "@workos-inc/authkit-react";

export default function SignIn() {
  const { signIn, isLoading } = useAuth();

  return (
    <div className="prompt-screen">
      <div className="prompt-card">
        <h1>Actor Chat</h1>
        <p>Sign in to join the conversation.</p>
        <button onClick={() => void signIn()} disabled={isLoading}>
          {isLoading ? "Loading…" : "Sign in"}
        </button>
      </div>
    </div>
  );
}
