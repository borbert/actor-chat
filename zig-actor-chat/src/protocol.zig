const std = @import("std");

/// Wire-protocol frame (Go-style one struct both directions).
pub const Frame = struct {
    type: []const u8,
    roomId: ?[]const u8 = null,
    userId: ?[]const u8 = null,
    body: ?[]const u8 = null,
    clientId: ?[]const u8 = null,
    messageId: ?[]const u8 = null,
    createdAt: ?f64 = null,
    reason: ?[]const u8 = null,
    users: ?[]const []const u8 = null,
    typing: ?bool = null,
    token: ?[]const u8 = null,
    server: ?[]const u8 = null,
    version: ?[]const u8 = null,
};

pub const inbound = struct {
    pub const join = "join";
    pub const leave = "leave";
    pub const send = "send";
    pub const typing_start = "typing_start";
    pub const typing_stop = "typing_stop";
    pub const ping = "ping";
};

pub const outbound = struct {
    pub const hello = "hello";
    pub const pong = "pong";
    pub const ack = "ack";
    pub const @"error" = "error";
    pub const presence_update = "presence_update";
    pub const typing_update = "typing_update";
};

pub fn parseInbound(allocator: std.mem.Allocator, text: []const u8) !std.json.Parsed(Frame) {
    return try std.json.parseFromSlice(Frame, allocator, text, .{
        .ignore_unknown_fields = true,
        .allocate = .alloc_always,
    });
}

pub fn helloJson(allocator: std.mem.Allocator, server: []const u8, ver: []const u8) ![]u8 {
    return try std.json.Stringify.valueAlloc(allocator, .{
        .type = outbound.hello,
        .server = server,
        .version = ver,
    }, .{});
}

pub fn pongJson(allocator: std.mem.Allocator) ![]u8 {
    return try std.json.Stringify.valueAlloc(allocator, .{ .type = outbound.pong }, .{});
}

pub fn errorJson(
    allocator: std.mem.Allocator,
    room_id: ?[]const u8,
    client_id: ?[]const u8,
    reason: []const u8,
) ![]u8 {
    return try std.json.Stringify.valueAlloc(allocator, .{
        .type = outbound.@"error",
        .roomId = room_id,
        .clientId = client_id,
        .reason = reason,
    }, .{});
}

pub fn ackJson(
    allocator: std.mem.Allocator,
    room_id: []const u8,
    message_id: []const u8,
    client_id: []const u8,
    created_at: f64,
) ![]u8 {
    return try std.json.Stringify.valueAlloc(allocator, .{
        .type = outbound.ack,
        .roomId = room_id,
        .messageId = message_id,
        .clientId = client_id,
        .createdAt = created_at,
    }, .{});
}

pub fn presenceJson(allocator: std.mem.Allocator, room_id: []const u8, users: []const []const u8) ![]u8 {
    return try std.json.Stringify.valueAlloc(allocator, .{
        .type = outbound.presence_update,
        .roomId = room_id,
        .users = users,
    }, .{});
}

pub fn typingJson(
    allocator: std.mem.Allocator,
    room_id: []const u8,
    user_id: []const u8,
    typing: bool,
) ![]u8 {
    return try std.json.Stringify.valueAlloc(allocator, .{
        .type = outbound.typing_update,
        .roomId = room_id,
        .userId = user_id,
        .typing = typing,
    }, .{});
}

test "parse join frame" {
    const parsed = try parseInbound(std.testing.allocator,
        \\{"type":"join","roomId":"r1"}
    );
    defer parsed.deinit();
    try std.testing.expectEqualStrings(inbound.join, parsed.value.type);
    try std.testing.expectEqualStrings("r1", parsed.value.roomId.?);
}
