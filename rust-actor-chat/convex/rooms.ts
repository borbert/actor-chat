import { mutation, query } from "./_generated/server";
import { v } from "convex/values";
import { requireMembership, requireUser } from "./lib/identity";

// Room functions (PRD §11). All derive the caller from the validated JWT.

export const create = mutation({
  args: {
    name: v.string(),
    kind: v.union(v.literal("dm"), v.literal("group")),
  },
  handler: async (ctx, args) => {
    const user = await requireUser(ctx);
    const now = Date.now();
    const roomId = await ctx.db.insert("rooms", {
      name: args.name,
      kind: args.kind,
      createdBy: user._id,
      createdAt: now,
    });
    await ctx.db.insert("memberships", {
      roomId,
      userId: user._id,
      joinedAt: now,
    });
    return await ctx.db.get(roomId);
  },
});

// list returns the rooms the caller belongs to.
export const list = query({
  args: {},
  handler: async (ctx) => {
    const user = await requireUser(ctx);
    const memberships = await ctx.db
      .query("memberships")
      .withIndex("by_user", (q) => q.eq("userId", user._id))
      .take(100);
    const rooms = [];
    for (const membership of memberships) {
      const room = await ctx.db.get(membership.roomId);
      if (room !== null) {
        rooms.push(room);
      }
    }
    return rooms;
  },
});

export const get = query({
  args: { roomId: v.id("rooms") },
  handler: async (ctx, args) => {
    const user = await requireUser(ctx);
    await requireMembership(ctx, args.roomId, user._id);
    return await ctx.db.get(args.roomId);
  },
});

// join adds the caller to a room (self-join by room ID; the v1 take on
// PRD §11's addMember). Idempotent.
export const join = mutation({
  args: { roomId: v.id("rooms") },
  handler: async (ctx, args) => {
    const user = await requireUser(ctx);
    const room = await ctx.db.get(args.roomId);
    if (room === null) {
      throw new Error("Room not found");
    }
    const existing = await ctx.db
      .query("memberships")
      .withIndex("by_room_user", (q) =>
        q.eq("roomId", args.roomId).eq("userId", user._id),
      )
      .unique();
    if (existing !== null) {
      return existing;
    }
    const id = await ctx.db.insert("memberships", {
      roomId: args.roomId,
      userId: user._id,
      joinedAt: Date.now(),
    });
    return await ctx.db.get(id);
  },
});
