const std = @import("std");
const InkList = @import("InkList");
const Engine = InkList.Engine;
const room = @import("room.zig");

const Allocator = std.mem.Allocator;

/// Lazy room registry: room_id → InkList actor id. Respawns after idle eviction.
pub const RoomRegistry = struct {
    allocator: Allocator,
    engine: *Engine,
    mutex: std.Thread.Mutex = .{},
    rooms: std.StringHashMap(u64),

    pub fn init(allocator: Allocator, engine: *Engine) RoomRegistry {
        return .{
            .allocator = allocator,
            .engine = engine,
            .rooms = std.StringHashMap(u64).init(allocator),
        };
    }

    pub fn deinit(self: *RoomRegistry) void {
        var it = self.rooms.keyIterator();
        while (it.next()) |key| {
            self.allocator.free(key.*);
        }
        self.rooms.deinit();
    }

    pub fn handleFor(self: *RoomRegistry, room_id: []const u8) !u64 {
        self.mutex.lock();
        defer self.mutex.unlock();

        if (self.rooms.get(room_id)) |existing| {
            if (self.engine.getActorState(room.RoomActor, existing)) |state| {
                if (!state.evicted) return existing;
            } else |_| {}
            // stale entry
            if (self.rooms.fetchRemove(room_id)) |kv| {
                self.allocator.free(kv.key);
            }
        }

        const actor_id = try self.engine.spawnActor(room.RoomActor);
        const state = try self.engine.getActorState(room.RoomActor, actor_id);
        try state.configure(self.engine, actor_id, room_id, onEvict, self);

        const key = try self.allocator.dupe(u8, room_id);
        try self.rooms.put(key, actor_id);
        return actor_id;
    }

    fn onEvict(ctx: *anyopaque, room_id: []const u8, actor_id: u64) void {
        const self: *RoomRegistry = @ptrCast(@alignCast(ctx));
        self.mutex.lock();
        defer self.mutex.unlock();
        // Only drop/poison if this is still the mapped actor — a newer spawn
        // for the same room_id must stay registered.
        const current = self.rooms.get(room_id) orelse return;
        if (current != actor_id) return;
        if (self.rooms.fetchRemove(room_id)) |kv| {
            self.allocator.free(kv.key);
            // poison asynchronously so we are not inside RoomActor.receive
            const engine = self.engine;
            engine.thread_pool.spawn(poisonRoom, .{ engine, actor_id }) catch {};
        }
    }

    fn poisonRoom(engine: *Engine, actor_id: u64) void {
        std.Thread.sleep(5 * std.time.ns_per_ms);
        engine.poisonActor(room.RoomActor, actor_id) catch {};
    }
};
