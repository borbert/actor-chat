import { defineSchema, defineTable } from "convex/server";
import { v } from "convex/values";

// Schema for the chat app. See PRD §8 for rationale.
export default defineSchema({
  users: defineTable({
    authId: v.string(), // external auth provider subject (JWT "sub")
    displayName: v.string(),
    avatarUrl: v.optional(v.string()),
    createdAt: v.number(),
  }).index("by_authId", ["authId"]),

  rooms: defineTable({
    name: v.string(),
    kind: v.union(v.literal("dm"), v.literal("group")),
    createdBy: v.id("users"),
    createdAt: v.number(),
  }),

  memberships: defineTable({
    roomId: v.id("rooms"),
    userId: v.id("users"),
    joinedAt: v.number(),
    lastReadAt: v.optional(v.number()),
  })
    .index("by_room", ["roomId"])
    .index("by_user", ["userId"])
    .index("by_room_user", ["roomId", "userId"]),

  messages: defineTable({
    roomId: v.id("rooms"),
    userId: v.id("users"),
    body: v.string(),
    clientId: v.string(), // idempotency key from the sender (PRD §12)
    createdAt: v.number(),
    editedAt: v.optional(v.number()),
    deletedAt: v.optional(v.number()),
  })
    .index("by_room_time", ["roomId", "createdAt"])
    .index("by_room_clientId", ["roomId", "clientId"]),
});
