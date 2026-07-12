const std = @import("std");
const InkList = @import("InkList");
const Message = InkList.Message;
const Engine = InkList.Engine;
const protocol = @import("../protocol.zig");

const Allocator = std.mem.Allocator;
const websocket = @import("httpz").websocket;

/// InkList actor that owns serialized writes to one WebSocket.
pub const ConnWriter = struct {
    allocator: Allocator,
    conn: ?*websocket.Conn = null,
    closed: std.atomic.Value(bool) = std.atomic.Value(bool).init(false),

    pub fn init(allocator: Allocator) !*ConnWriter {
        const self = try allocator.create(ConnWriter);
        self.* = .{ .allocator = allocator };
        return self;
    }

    pub fn deinit(self: *ConnWriter) void {
        self.closed.store(true, .release);
        self.allocator.destroy(self);
    }

    pub fn bind(self: *ConnWriter, conn: *websocket.Conn) void {
        self.conn = conn;
    }

    pub fn receive(self: *ConnWriter, allocator: Allocator, msg: *Message) void {
        _ = allocator;
        if (self.closed.load(.acquire)) return;
        const conn = self.conn orelse return;

        switch (msg.instruction) {
            .custom => |json| {
                conn.write(json) catch {
                    self.closed.store(true, .release);
                };
            },
            .func => |f| f.call_fn(f.context, @ptrCast(self)),
        }
    }
};

pub fn sendJson(engine: *Engine, actor_id: u64, allocator: Allocator, json: []const u8) !void {
    const msg = try Message.makeCustomPayload(allocator, 0, json);
    try engine.sendMessage(actor_id, msg);
}

pub fn sendOwnedJson(engine: *Engine, actor_id: u64, allocator: Allocator, json: []u8) void {
    defer allocator.free(json);
    sendJson(engine, actor_id, allocator, json) catch {};
}

pub fn sendHello(engine: *Engine, actor_id: u64, allocator: Allocator) void {
    const json = protocol.helloJson(allocator, protocol_server_name(), protocol_version()) catch return;
    sendOwnedJson(engine, actor_id, allocator, json);
}

fn protocol_server_name() []const u8 {
    return @import("../config.zig").server_name;
}

fn protocol_version() []const u8 {
    return @import("../config.zig").version;
}

test "conn writer init/deinit" {
    const w = try ConnWriter.init(std.testing.allocator);
    w.deinit();
}
