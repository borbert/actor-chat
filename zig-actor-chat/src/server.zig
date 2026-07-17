const std = @import("std");
const httpz = @import("httpz");
const InkList = @import("InkList");

const config = @import("config.zig");
const protocol = @import("protocol.zig");
const auth = @import("auth.zig");
const convex = @import("convex.zig");
const connection = @import("actors/connection.zig");
const room = @import("actors/room.zig");
const registry_mod = @import("actors/registry.zig");

const Allocator = std.mem.Allocator;
const Engine = InkList.Engine;
const websocket = httpz.websocket;

pub const App = struct {
    allocator: Allocator,
    engine: *Engine,
    registry: *registry_mod.RoomRegistry,
    validator: *auth.TokenValidator,
    convex: *convex.Client,

    pub const WebsocketHandler = WsClient;
};

pub const WsClient = struct {
    app: *App,
    conn: *websocket.Conn,
    writer_actor_id: u64,
    user_id: []u8,
    auth_id: []u8,
    token: []u8,
    joined: std.StringHashMap(void),

    pub const Context = struct {
        app: *App,
        writer_actor_id: u64,
        user_id: []u8,
        auth_id: []u8,
        token: []u8,
    };

    pub fn init(conn: *websocket.Conn, ctx: *const Context) !WsClient {
        const writer = try ctx.app.engine.getActorState(connection.ConnWriter, ctx.writer_actor_id);
        writer.bind(conn);

        return .{
            .app = ctx.app,
            .conn = conn,
            .writer_actor_id = ctx.writer_actor_id,
            .user_id = ctx.user_id,
            .auth_id = ctx.auth_id,
            .token = ctx.token,
            .joined = std.StringHashMap(void).init(ctx.app.allocator),
        };
    }

    pub fn afterInit(self: *WsClient) !void {
        connection.sendHello(self.app.engine, self.writer_actor_id, self.app.allocator);
    }

    pub fn clientMessage(self: *WsClient, data: []const u8) !void {
        const parsed = protocol.parseInbound(self.app.allocator, data) catch {
            const json = try protocol.errorJson(self.app.allocator, null, null, "bad frame");
            connection.sendOwnedJson(self.app.engine, self.writer_actor_id, self.app.allocator, json);
            return;
        };
        defer parsed.deinit();
        const frame = parsed.value;

        if (std.mem.eql(u8, frame.type, protocol.inbound.join)) {
            try self.onJoin(frame.roomId orelse return);
        } else if (std.mem.eql(u8, frame.type, protocol.inbound.leave)) {
            try self.onLeave(frame.roomId orelse return);
        } else if (std.mem.eql(u8, frame.type, protocol.inbound.typing_start)) {
            try self.onTyping(frame.roomId orelse return, true);
        } else if (std.mem.eql(u8, frame.type, protocol.inbound.typing_stop)) {
            try self.onTyping(frame.roomId orelse return, false);
        } else if (std.mem.eql(u8, frame.type, protocol.inbound.send)) {
            try self.onSend(
                frame.roomId orelse return,
                frame.body orelse return,
                frame.clientId orelse return,
            );
        } else if (std.mem.eql(u8, frame.type, protocol.inbound.ping)) {
            try self.onPing(frame.token);
        }
    }

    pub fn clientClose(self: *WsClient, _: []const u8) !void {
        self.cleanup();
    }

    fn onJoin(self: *WsClient, room_id: []const u8) !void {
        if (self.joined.contains(room_id)) return;
        const key = try self.app.allocator.dupe(u8, room_id);
        errdefer self.app.allocator.free(key);
        try self.joined.put(key, {});
        const room_actor = try self.app.registry.handleFor(room_id);
        try room.sendJoin(self.app.engine, room_actor, self.app.allocator, self.writer_actor_id, self.user_id);
    }

    fn onLeave(self: *WsClient, room_id: []const u8) !void {
        const kv = self.joined.fetchRemove(room_id) orelse return;
        defer self.app.allocator.free(kv.key);
        const room_actor = try self.app.registry.handleFor(room_id);
        try room.sendLeave(self.app.engine, room_actor, self.app.allocator, self.writer_actor_id);
    }

    fn onTyping(self: *WsClient, room_id: []const u8, on: bool) !void {
        if (!self.joined.contains(room_id)) return;
        const room_actor = try self.app.registry.handleFor(room_id);
        try room.sendTyping(self.app.engine, room_actor, self.app.allocator, self.writer_actor_id, self.user_id, on);
    }

    fn onSend(self: *WsClient, room_id: []const u8, body: []const u8, client_id: []const u8) !void {
        const job = try self.app.allocator.create(SendWork);
        errdefer self.app.allocator.destroy(job);
        job.* = .{
            .allocator = self.app.allocator,
            .engine = self.app.engine,
            .writer_actor_id = self.writer_actor_id,
            .convex_base = self.app.convex,
            .token = try self.app.allocator.dupe(u8, self.token),
            .room_id = try self.app.allocator.dupe(u8, room_id),
            .body = try self.app.allocator.dupe(u8, body),
            .client_id = try self.app.allocator.dupe(u8, client_id),
        };
        // Bounded InkList pool (same pattern as registry.poisonRoom); not an OS thread per send.
        self.app.engine.thread_pool.spawn(SendWork.run, .{job}) catch {
            self.app.allocator.free(job.token);
            self.app.allocator.free(job.room_id);
            self.app.allocator.free(job.body);
            self.app.allocator.free(job.client_id);
            // job itself freed by errdefer
            return error.ThreadPoolBusy;
        };
    }

    fn onPing(self: *WsClient, maybe_token: ?[]const u8) !void {
        if (maybe_token) |fresh| {
            if (self.app.validator.validate(fresh)) |sub| {
                defer self.app.allocator.free(sub);
                if (std.mem.eql(u8, sub, self.auth_id)) {
                    self.app.allocator.free(self.token);
                    self.token = try self.app.allocator.dupe(u8, fresh);
                } else {
                    std.log.warn("ping token subject mismatch; keeping current token", .{});
                }
            } else |err| {
                std.log.warn("ping token invalid: {}", .{err});
            }
        }
        const json = try protocol.pongJson(self.app.allocator);
        connection.sendOwnedJson(self.app.engine, self.writer_actor_id, self.app.allocator, json);
    }

    fn cleanup(self: *WsClient) void {
        var it = self.joined.iterator();
        while (it.next()) |entry| {
            const room_id = entry.key_ptr.*;
            if (self.app.registry.handleFor(room_id)) |room_actor| {
                room.sendLeave(self.app.engine, room_actor, self.app.allocator, self.writer_actor_id) catch {};
            } else |_| {}
            self.app.allocator.free(room_id);
        }
        self.joined.deinit();
        self.app.allocator.free(self.user_id);
        self.app.allocator.free(self.auth_id);
        self.app.allocator.free(self.token);
        self.app.engine.poisonActor(connection.ConnWriter, self.writer_actor_id) catch {};
    }
};

const SendWork = struct {
    allocator: Allocator,
    engine: *Engine,
    writer_actor_id: u64,
    convex_base: *convex.Client,
    token: []u8,
    room_id: []u8,
    body: []u8,
    client_id: []u8,

    fn run(self: *SendWork) void {
        defer {
            self.allocator.free(self.token);
            self.allocator.free(self.room_id);
            self.allocator.free(self.body);
            self.allocator.free(self.client_id);
            self.allocator.destroy(self);
        }

        const q_room = jsonQuote(self.allocator, self.room_id) catch return self.fail("encode failed");
        defer self.allocator.free(q_room);
        const q_body = jsonQuote(self.allocator, self.body) catch return self.fail("encode failed");
        defer self.allocator.free(q_body);
        const q_client = jsonQuote(self.allocator, self.client_id) catch return self.fail("encode failed");
        defer self.allocator.free(q_client);

        const args = std.fmt.allocPrint(
            self.allocator,
            "{{\"roomId\":{s},\"body\":{s},\"clientId\":{s}}}",
            .{ q_room, q_body, q_client },
        ) catch return self.fail("encode failed");
        defer self.allocator.free(args);

        var client = self.convex_base.withAuth(self.token);
        const value = client.mutation("messages:send", args) catch return self.fail("convex mutation failed");
        defer self.allocator.free(value);

        var sent = convex.Client.parseSentMessage(self.allocator, value) catch return self.fail("decode failed");
        defer sent.deinit(self.allocator);

        const json = protocol.ackJson(self.allocator, self.room_id, sent.id, self.client_id, sent.created_at) catch return self.fail("encode failed");
        connection.sendOwnedJson(self.engine, self.writer_actor_id, self.allocator, json);
    }

    fn fail(self: *SendWork, reason: []const u8) void {
        const json = protocol.errorJson(self.allocator, self.room_id, self.client_id, reason) catch return;
        connection.sendOwnedJson(self.engine, self.writer_actor_id, self.allocator, json);
    }
};

fn jsonQuote(allocator: Allocator, s: []const u8) ![]u8 {
    return try std.json.Stringify.valueAlloc(allocator, s, .{});
}

pub fn health(_: *App, _: *httpz.Request, res: *httpz.Response) !void {
    res.header("Server", try std.fmt.allocPrint(res.arena, "{s}/{s}", .{ config.server_name, config.version }));
    res.body = "ok";
}

pub fn versionEndpoint(_: *App, _: *httpz.Request, res: *httpz.Response) !void {
    res.header("Server", try std.fmt.allocPrint(res.arena, "{s}/{s}", .{ config.server_name, config.version }));
    try res.json(.{
        .backend = config.server_name,
        .runtime = "InkList",
        .version = config.version,
    }, .{});
}

pub fn ready(app: *App, _: *httpz.Request, res: *httpz.Response) !void {
    const value = app.convex.query("health:ping", "{}") catch {
        res.status = 503;
        res.body = "not ready";
        return;
    };
    defer app.allocator.free(value);
    res.body = "ready";
}

pub fn ws(app: *App, req: *httpz.Request, res: *httpz.Response) !void {
    const query = try req.query();
    const token = query.get("token") orelse {
        res.status = 401;
        res.body = "missing token";
        return;
    };

    const sub = app.validator.validate(token) catch {
        std.log.warn("token rejected", .{});
        res.status = 401;
        res.body = "invalid token";
        return;
    };

    var authed = app.convex.withAuth(token);
    const user_json = authed.mutation("users:getOrCreateFromAuth", "{}") catch {
        app.allocator.free(sub);
        res.status = 500;
        res.body = "user provisioning failed";
        return;
    };
    defer app.allocator.free(user_json);

    const user = convex.Client.parseUser(app.allocator, user_json) catch {
        app.allocator.free(sub);
        res.status = 500;
        res.body = "user provisioning failed";
        return;
    };

    std.log.info("ws authenticated", .{});

    const writer_actor_id = app.engine.spawnActor(connection.ConnWriter) catch |err| {
        app.allocator.free(sub);
        app.allocator.free(user.id);
        return err;
    };

    const token_owned = app.allocator.dupe(u8, token) catch |err| {
        app.allocator.free(sub);
        app.allocator.free(user.id);
        app.engine.poisonActor(connection.ConnWriter, writer_actor_id) catch {};
        return err;
    };

    const ctx = WsClient.Context{
        .app = app,
        .writer_actor_id = writer_actor_id,
        .user_id = user.id,
        .auth_id = sub,
        .token = token_owned,
    };

    if (try httpz.upgradeWebsocket(WsClient, req, res, &ctx) == false) {
        res.status = 400;
        res.body = "invalid websocket handshake";
        app.allocator.free(ctx.user_id);
        app.allocator.free(ctx.auth_id);
        app.allocator.free(ctx.token);
        app.engine.poisonActor(connection.ConnWriter, writer_actor_id) catch {};
        return;
    }
    // Success: WsClient owns user_id / auth_id / token.
}
