import { mutation, query } from "./_generated/server";
import { v } from "convex/values";
import { requireUser } from "./lib/identity";

// getOrCreateFromAuth provisions a user row for the authenticated caller on
// first connection (PRD §11, §13). Identity comes from the validated WorkOS
// JWT; displayName is an optional override used only at creation time.
export const getOrCreateFromAuth = mutation({
  args: { displayName: v.optional(v.string()) },
  handler: async (ctx, args) => {
    const identity = await ctx.auth.getUserIdentity();
    if (identity === null) {
      throw new Error("Not authenticated");
    }
    const existing = await ctx.db
      .query("users")
      .withIndex("by_authId", (q) => q.eq("authId", identity.subject))
      .unique();
    if (existing !== null) {
      return existing;
    }
    const id = await ctx.db.insert("users", {
      authId: identity.subject,
      displayName: args.displayName ?? identity.name ?? identity.email ?? "User",
      avatarUrl: typeof identity.pictureUrl === "string" ? identity.pictureUrl : undefined,
      createdAt: Date.now(),
    });
    return await ctx.db.get(id);
  },
});

// list provides a bounded user directory so the client can map userIds to
// display names. Replace with a per-room member query if it outgrows this.
export const list = query({
  args: {},
  handler: async (ctx) => {
    await requireUser(ctx);
    return await ctx.db.query("users").take(100);
  },
});
