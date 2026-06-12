// Clerk issues JWTs from the app's Frontend API domain. The "convex" JWT
// template (Clerk dashboard → JWT Templates → Convex preset) sets aud to
// "convex", which is what applicationID matches against.
// https://docs.convex.dev/auth/clerk
// CLERK_JWT_ISSUER_DOMAIN is set on the Convex deployment:
//   bunx convex env set CLERK_JWT_ISSUER_DOMAIN https://<your-app>.clerk.accounts.dev
export default {
  providers: [
    {
      domain: process.env.CLERK_JWT_ISSUER_DOMAIN,
      applicationID: "convex",
    },
  ],
};
