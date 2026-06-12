import { StrictMode } from "react";
import { createRoot } from "react-dom/client";
import { AuthKitProvider, useAuth } from "@workos-inc/authkit-react";
import { ConvexProviderWithAuthKit } from "@convex-dev/workos";
import { ConvexReactClient } from "convex/react";
import App from "./App";
import "./styles.css";

const convexUrl = import.meta.env.VITE_CONVEX_URL as string | undefined;
const workosClientId = import.meta.env.VITE_WORKOS_CLIENT_ID as
  | string
  | undefined;
if (!convexUrl || !workosClientId) {
  throw new Error(
    "VITE_CONVEX_URL and VITE_WORKOS_CLIENT_ID are required (see web/.env.example)",
  );
}

const convex = new ConvexReactClient(convexUrl);
const redirectUri =
  (import.meta.env.VITE_WORKOS_REDIRECT_URI as string | undefined) ??
  window.location.origin;

createRoot(document.getElementById("root")!).render(
  <StrictMode>
    <AuthKitProvider clientId={workosClientId} redirectUri={redirectUri}>
      <ConvexProviderWithAuthKit client={convex} useAuth={useAuth}>
        <App />
      </ConvexProviderWithAuthKit>
    </AuthKitProvider>
  </StrictMode>,
);
