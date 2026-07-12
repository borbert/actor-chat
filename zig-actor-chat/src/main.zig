const std = @import("std");
const httpz = @import("httpz");
const InkList = @import("InkList");

const config = @import("config.zig");
const auth = @import("auth.zig");
const convex = @import("convex.zig");
const server = @import("server.zig");
const registry_mod = @import("actors/registry.zig");

const Engine = InkList.Engine;

pub const std_options = std.Options{
    .log_scope_levels = &[_]std.log.ScopeLevel{
        .{ .scope = .websocket, .level = .err },
    },
};

var server_instance: ?*httpz.Server(*server.App) = null;

pub fn main() !void {
    var gpa = std.heap.GeneralPurposeAllocator(.{}){};
    defer _ = gpa.deinit();
    const allocator = gpa.allocator();

    var settings = try config.Settings.load(allocator);
    defer settings.deinit(allocator);

    var http_client: std.http.Client = .{ .allocator = allocator };
    defer http_client.deinit();

    var engine = try Engine.init(allocator, .{ .allocator = allocator, .n_jobs = 4 }, 64);
    defer engine.deinit();

    var registry = registry_mod.RoomRegistry.init(allocator, &engine);
    defer registry.deinit();

    const validator = try auth.TokenValidator.init(allocator, &http_client, settings.clerk_issuer);
    defer validator.deinit();

    var convex_client = try convex.Client.init(allocator, &http_client, settings.convex_url);
    defer convex_client.deinit();

    var app = server.App{
        .allocator = allocator,
        .engine = &engine,
        .registry = &registry,
        .validator = validator,
        .convex = &convex_client,
    };

    installSignalHandlers();

    // Clerk JWTs in ?token= easily exceed httpz's default 4KiB request buffer → 431.
    var http_server = try httpz.Server(*server.App).init(allocator, .{
        .address = .all(settings.port),
        .request = .{ .buffer_size = 32 * 1024 },
    }, &app);
    // stop() is invoked from the signal handler; calling it again here
    // double-closes the listener and panics (BADF).
    defer http_server.deinit();
    server_instance = &http_server;

    var router = try http_server.router(.{});
    router.get("/health", server.health, .{});
    router.get("/version", server.versionEndpoint, .{});
    router.get("/ready", server.ready, .{});
    router.get("/ws", server.ws, .{});

    std.log.info("listening on http://0.0.0.0:{d}", .{settings.port});
    try http_server.listen();
}

fn installSignalHandlers() void {
    const action = std.posix.Sigaction{
        .handler = .{ .handler = onSignal },
        .mask = std.posix.sigemptyset(),
        .flags = 0,
    };
    std.posix.sigaction(std.posix.SIG.INT, &action, null);
    std.posix.sigaction(std.posix.SIG.TERM, &action, null);
}

fn onSignal(_: c_int) callconv(.c) void {
    if (server_instance) |s| {
        server_instance = null;
        s.stop();
    }
}

test {
    _ = @import("protocol.zig");
    _ = @import("actors/connection.zig");
}
