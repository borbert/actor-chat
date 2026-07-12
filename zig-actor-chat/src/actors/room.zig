const std = @import("std");
const InkList = @import("InkList");
const Message = InkList.Message;
const Engine = InkList.Engine;
const protocol = @import("../protocol.zig");
const connection = @import("connection.zig");

const Allocator = std.mem.Allocator;

const idle_check_ns: u64 = 60 * std.time.ns_per_s;
const idle_timeout_ns: i128 = 300 * std.time.ns_per_s;

const Member = struct {
    conn_actor_id: u64,
    user_id: []u8,
};

pub const JoinCtx = struct {
    conn_actor_id: u64,
    user_id: []u8,

    fn deinitFn(ctx: *anyopaque, allocator: Allocator) void {
        const self: *JoinCtx = @ptrCast(@alignCast(ctx));
        allocator.free(self.user_id);
        allocator.destroy(self);
    }

    fn cloneFn(ctx: *anyopaque, allocator: Allocator) Allocator.Error!*anyopaque {
        const self: *JoinCtx = @ptrCast(@alignCast(ctx));
        const copy = try allocator.create(JoinCtx);
        copy.* = .{
            .conn_actor_id = self.conn_actor_id,
            .user_id = try allocator.dupe(u8, self.user_id),
        };
        return copy;
    }

    fn call(ctx: *anyopaque, actor: *anyopaque) void {
        const self: *JoinCtx = @ptrCast(@alignCast(ctx));
        const room: *RoomActor = @ptrCast(@alignCast(actor));
        room.onJoin(self.conn_actor_id, self.user_id);
    }
};

pub const LeaveCtx = struct {
    conn_actor_id: u64,

    fn deinitFn(ctx: *anyopaque, allocator: Allocator) void {
        const self: *LeaveCtx = @ptrCast(@alignCast(ctx));
        allocator.destroy(self);
    }

    fn cloneFn(ctx: *anyopaque, allocator: Allocator) Allocator.Error!*anyopaque {
        const self: *LeaveCtx = @ptrCast(@alignCast(ctx));
        const copy = try allocator.create(LeaveCtx);
        copy.* = .{ .conn_actor_id = self.conn_actor_id };
        return copy;
    }

    fn call(ctx: *anyopaque, actor: *anyopaque) void {
        const self: *LeaveCtx = @ptrCast(@alignCast(ctx));
        const room: *RoomActor = @ptrCast(@alignCast(actor));
        room.onLeave(self.conn_actor_id);
    }
};

pub const TypingCtx = struct {
    conn_actor_id: u64,
    user_id: []u8,
    on: bool,

    fn deinitFn(ctx: *anyopaque, allocator: Allocator) void {
        const self: *TypingCtx = @ptrCast(@alignCast(ctx));
        allocator.free(self.user_id);
        allocator.destroy(self);
    }

    fn cloneFn(ctx: *anyopaque, allocator: Allocator) Allocator.Error!*anyopaque {
        const self: *TypingCtx = @ptrCast(@alignCast(ctx));
        const copy = try allocator.create(TypingCtx);
        copy.* = .{
            .conn_actor_id = self.conn_actor_id,
            .user_id = try allocator.dupe(u8, self.user_id),
            .on = self.on,
        };
        return copy;
    }

    fn call(ctx: *anyopaque, actor: *anyopaque) void {
        const self: *TypingCtx = @ptrCast(@alignCast(ctx));
        const room: *RoomActor = @ptrCast(@alignCast(actor));
        room.onTyping(self.conn_actor_id, self.user_id, self.on);
    }
};

/// Per-room InkList actor: sole owner of presence/typing for that room.
pub const RoomActor = struct {
    allocator: Allocator,
    engine: ?*Engine = null,
    actor_id: u64 = 0,
    room_id: []u8 = &.{},
    members: std.AutoHashMap(u64, Member),
    last_activity_ns: i128 = 0,
    idle_sender: ?*InkList.PeriodicSender = null,
    evicted: bool = false,
    on_evict: ?*const fn (*anyopaque, []const u8) void = null,
    on_evict_ctx: ?*anyopaque = null,

    pub fn init(allocator: Allocator) !*RoomActor {
        const self = try allocator.create(RoomActor);
        self.* = .{
            .allocator = allocator,
            .members = std.AutoHashMap(u64, Member).init(allocator),
            .last_activity_ns = std.time.nanoTimestamp(),
        };
        return self;
    }

    pub fn deinit(self: *RoomActor) void {
        if (self.idle_sender) |sender| sender.stop();
        var it = self.members.iterator();
        while (it.next()) |entry| {
            self.allocator.free(entry.value_ptr.user_id);
        }
        self.members.deinit();
        if (self.room_id.len != 0) self.allocator.free(self.room_id);
        self.allocator.destroy(self);
    }

    pub fn configure(
        self: *RoomActor,
        engine: *Engine,
        actor_id: u64,
        room_id: []const u8,
        on_evict: ?*const fn (*anyopaque, []const u8) void,
        on_evict_ctx: ?*anyopaque,
    ) !void {
        self.engine = engine;
        self.actor_id = actor_id;
        self.room_id = try self.allocator.dupe(u8, room_id);
        self.on_evict = on_evict;
        self.on_evict_ctx = on_evict_ctx;
        self.last_activity_ns = std.time.nanoTimestamp();

        // Periodic idle check via InkList sendEvery.
        const tick = try Message.makeCustomPayload(self.allocator, 0, "idle_tick");
        self.idle_sender = try engine.sendEvery(actor_id, tick, idle_check_ns);
        tick.deinit(self.allocator);
    }

    pub fn receive(self: *RoomActor, allocator: Allocator, msg: *Message) void {
        _ = allocator;
        if (self.evicted) return;

        switch (msg.instruction) {
            .custom => |s| {
                if (std.mem.eql(u8, s, "idle_tick")) {
                    self.onIdleTick();
                }
            },
            .func => |f| f.call_fn(f.context, @ptrCast(self)),
        }
    }

    fn onIdleTick(self: *RoomActor) void {
        if (self.members.count() == 0) {
            const idle_for = std.time.nanoTimestamp() - self.last_activity_ns;
            if (idle_for >= idle_timeout_ns) {
                self.evicted = true;
                if (self.on_evict) |cb| {
                    if (self.on_evict_ctx) |ctx| cb(ctx, self.room_id);
                }
                std.log.info("room evicted (idle) room={s}", .{self.room_id});
            }
        }
    }

    fn touch(self: *RoomActor) void {
        self.last_activity_ns = std.time.nanoTimestamp();
    }

    fn onJoin(self: *RoomActor, conn_actor_id: u64, user_id: []const u8) void {
        self.touch();
        const gop = self.members.getOrPut(conn_actor_id) catch return;
        if (gop.found_existing) {
            self.allocator.free(gop.value_ptr.user_id);
        }
        gop.value_ptr.* = .{
            .conn_actor_id = conn_actor_id,
            .user_id = self.allocator.dupe(u8, user_id) catch return,
        };
        self.broadcastPresence();
    }

    fn onLeave(self: *RoomActor, conn_actor_id: u64) void {
        self.touch();
        if (self.members.fetchRemove(conn_actor_id)) |kv| {
            self.allocator.free(kv.value.user_id);
            self.broadcastPresence();
        }
    }

    fn onTyping(self: *RoomActor, sender: u64, user_id: []const u8, on: bool) void {
        self.touch();
        const engine = self.engine orelse return;
        const json = protocol.typingJson(self.allocator, self.room_id, user_id, on) catch return;
        defer self.allocator.free(json);

        var it = self.members.iterator();
        while (it.next()) |entry| {
            if (entry.key_ptr.* == sender) continue;
            connection.sendJson(engine, entry.key_ptr.*, self.allocator, json) catch {};
        }
    }

    fn broadcastPresence(self: *RoomActor) void {
        const engine = self.engine orelse return;
        const users = self.presenceList() catch return;
        defer {
            for (users) |u| self.allocator.free(u);
            self.allocator.free(users);
        }
        const json = protocol.presenceJson(self.allocator, self.room_id, users) catch return;
        defer self.allocator.free(json);

        var it = self.members.iterator();
        while (it.next()) |entry| {
            connection.sendJson(engine, entry.key_ptr.*, self.allocator, json) catch {};
        }
    }

    fn presenceList(self: *RoomActor) ![][]const u8 {
        var set = std.StringHashMap(void).init(self.allocator);
        defer set.deinit();

        var it = self.members.iterator();
        while (it.next()) |entry| {
            try set.put(entry.value_ptr.user_id, {});
        }

        var list: std.ArrayList([]const u8) = .{};
        errdefer {
            for (list.items) |u| self.allocator.free(u);
            list.deinit(self.allocator);
        }
        var sit = set.keyIterator();
        while (sit.next()) |key| {
            try list.append(self.allocator, try self.allocator.dupe(u8, key.*));
        }
        std.mem.sort([]const u8, list.items, {}, struct {
            fn less(_: void, a: []const u8, b: []const u8) bool {
                return std.mem.order(u8, a, b) == .lt;
            }
        }.less);
        return try list.toOwnedSlice(self.allocator);
    }
};

pub fn sendJoin(engine: *Engine, room_actor_id: u64, allocator: Allocator, conn_actor_id: u64, user_id: []const u8) !void {
    const ctx = try allocator.create(JoinCtx);
    ctx.* = .{
        .conn_actor_id = conn_actor_id,
        .user_id = try allocator.dupe(u8, user_id),
    };
    const msg = try Message.makeFuncPayload(
        allocator,
        conn_actor_id,
        JoinCtx.call,
        ctx,
        JoinCtx.deinitFn,
        JoinCtx.cloneFn,
    );
    try engine.sendMessage(room_actor_id, msg);
}

pub fn sendLeave(engine: *Engine, room_actor_id: u64, allocator: Allocator, conn_actor_id: u64) !void {
    const ctx = try allocator.create(LeaveCtx);
    ctx.* = .{ .conn_actor_id = conn_actor_id };
    const msg = try Message.makeFuncPayload(
        allocator,
        conn_actor_id,
        LeaveCtx.call,
        ctx,
        LeaveCtx.deinitFn,
        LeaveCtx.cloneFn,
    );
    try engine.sendMessage(room_actor_id, msg);
}

pub fn sendTyping(
    engine: *Engine,
    room_actor_id: u64,
    allocator: Allocator,
    conn_actor_id: u64,
    user_id: []const u8,
    on: bool,
) !void {
    const ctx = try allocator.create(TypingCtx);
    ctx.* = .{
        .conn_actor_id = conn_actor_id,
        .user_id = try allocator.dupe(u8, user_id),
        .on = on,
    };
    const msg = try Message.makeFuncPayload(
        allocator,
        conn_actor_id,
        TypingCtx.call,
        ctx,
        TypingCtx.deinitFn,
        TypingCtx.cloneFn,
    );
    try engine.sendMessage(room_actor_id, msg);
}
