import { SignInButton, SignUpButton } from "@clerk/react";

export default function SignIn() {
  return (
    <div className="prompt-screen">
      <div className="prompt-card">
        <h1>Actor Chat</h1>
        <p>Sign in to join the conversation.</p>
        <div className="auth-buttons">
          <SignInButton mode="modal" />
          <SignUpButton mode="modal" />
        </div>
      </div>
    </div>
  );
}
