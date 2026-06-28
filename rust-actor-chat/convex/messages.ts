import { mutation, query } from "./_generated/server";
import { v } from "convex/values";
import { paginationOptsValidator } from "convex/server";
import { requireMembership, requireUser } from "./lib/identity";

// send is the idempotent message insert (PRD §10.1, §12). The Go RoomActor
// is the single writer for a room and calls this with the sender's JWT, so
// identity and membership are enforced here. Retries with the same
// (roomId, clientId) return the existing row instead of inserting a
// duplicate.
export const send = mutation({
  args: {
    roomId: v.id("rooms"),
    body: v.string(),
    clientId: v.string(),
  },
  handler: async (ctx, args) => {
    const user = await requireUser(ctx);
    await requireMembership(ctx, args.roomId, user._id);

    const existing = await ctx.db
      .query("messages")
      .withIndex("by_room_clientId", (q) =>
        q.eq("roomId", args.roomId).eq("clientId", args.clientId),
      )
      .unique();
    if (existing !== null) {
      return existing;
    }

    const id = await ctx.db.insert("messages", {
      roomId: args.roomId,
      userId: user._id,
      body: args.body,
      clientId: args.clientId,
      createdAt: Date.now(),
    });
    return await ctx.db.get(id);
  },
});

// list returns a page of messages for a room, newest first. The web client
// subscribes to this for live updates (PRD §10.2).
export const list = query({
  args: {
    roomId: v.id("rooms"),
    paginationOpts: paginationOptsValidator,
  },
  handler: async (ctx, args) => {
    const user = await requireUser(ctx);
    await requireMembership(ctx, args.roomId, user._id);
    return await ctx.db
      .query("messages")
      .withIndex("by_room_time", (q) => q.eq("roomId", args.roomId))
      .order("desc")
      .paginate(args.paginationOpts);
  },
});
