import { query } from "./_generated/server";

// ping is a lightweight, public readiness probe. The Go server's /ready
// endpoint calls it to confirm the Convex deployment is reachable.
export const ping = query({
  args: {},
  handler: async () => {
    return { now: Date.now() };
  },
});
