import { MutationCtx, QueryCtx } from "../_generated/server";
import { Doc, Id } from "../_generated/dataModel";

type Ctx = QueryCtx | MutationCtx;

// requireUser resolves the authenticated caller to their users row. The
// WorkOS JWT subject is stored as users.authId (PRD §13).
export async function requireUser(ctx: Ctx): Promise<Doc<"users">> {
  const identity = await ctx.auth.getUserIdentity();
  if (identity === null) {
    throw new Error("Not authenticated");
  }
  const user = await ctx.db
    .query("users")
    .withIndex("by_authId", (q) => q.eq("authId", identity.subject))
    .unique();
  if (user === null) {
    throw new Error("User not provisioned; call users:getOrCreateFromAuth first");
  }
  return user;
}

// requireMembership enforces row-level access: the caller must be a member
// of the room (PRD §13).
export async function requireMembership(
  ctx: Ctx,
  roomId: Id<"rooms">,
  userId: Id<"users">,
): Promise<Doc<"memberships">> {
  const membership = await ctx.db
    .query("memberships")
    .withIndex("by_room_user", (q) => q.eq("roomId", roomId).eq("userId", userId))
    .unique();
  if (membership === null) {
    throw new Error("Not a member of this room");
  }
  return membership;
}
