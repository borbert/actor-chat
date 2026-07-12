const std = @import("std");

pub const version = "0.1.0";
pub const server_name = "zig-actor-chat";

var file_env: ?std.StringHashMap([]const u8) = null;

pub const Settings = struct {
    port: u16,
    convex_url: []const u8,
    clerk_issuer: []const u8,

    pub fn load(allocator: std.mem.Allocator) !Settings {
        var map = std.StringHashMap([]const u8).init(allocator);
        errdefer {
            var it = map.iterator();
            while (it.next()) |e| {
                allocator.free(e.key_ptr.*);
                allocator.free(e.value_ptr.*);
            }
            map.deinit();
        }

        try loadDotEnvFile(allocator, &map, ".env.local");
        try loadDotEnvFile(allocator, &map, ".env");
        file_env = map;

        const port: u16 = blk: {
            const raw = getEnv("PORT") orelse break :blk 8100;
            break :blk try std.fmt.parseInt(u16, raw, 10);
        };
        const convex_url = try requireEnv(allocator, "CONVEX_URL");
        errdefer allocator.free(convex_url);
        const clerk_issuer = try requireEnv(allocator, "CLERK_JWT_ISSUER_DOMAIN");
        errdefer allocator.free(clerk_issuer);

        return .{
            .port = port,
            .convex_url = convex_url,
            .clerk_issuer = clerk_issuer,
        };
    }

    pub fn deinit(self: *Settings, allocator: std.mem.Allocator) void {
        allocator.free(self.convex_url);
        allocator.free(self.clerk_issuer);
        if (file_env) |*map| {
            var it = map.iterator();
            while (it.next()) |e| {
                allocator.free(e.key_ptr.*);
                allocator.free(e.value_ptr.*);
            }
            map.deinit();
            file_env = null;
        }
    }
};

fn getEnv(key: []const u8) ?[]const u8 {
    if (std.posix.getenv(key)) |v| return v;
    if (file_env) |map| return map.get(key);
    return null;
}

fn requireEnv(allocator: std.mem.Allocator, key: []const u8) ![]u8 {
    const value = getEnv(key) orelse {
        std.log.err("missing required env var {s}", .{key});
        return error.MissingEnv;
    };
    return try allocator.dupe(u8, value);
}

fn loadDotEnvFile(allocator: std.mem.Allocator, map: *std.StringHashMap([]const u8), path: []const u8) !void {
    const file = std.fs.cwd().openFile(path, .{}) catch return;
    defer file.close();

    const content = try file.readToEndAlloc(allocator, 64 * 1024);
    defer allocator.free(content);

    var lines = std.mem.splitScalar(u8, content, '\n');
    while (lines.next()) |raw_line| {
        const line = std.mem.trim(u8, raw_line, " \t\r");
        if (line.len == 0 or line[0] == '#') continue;
        const eq = std.mem.indexOfScalar(u8, line, '=') orelse continue;
        const key = std.mem.trim(u8, line[0..eq], " \t");
        var value = std.mem.trim(u8, line[eq + 1 ..], " \t");
        if (value.len >= 2 and ((value[0] == '"' and value[value.len - 1] == '"') or
            (value[0] == '\'' and value[value.len - 1] == '\'')))
        {
            value = value[1 .. value.len - 1];
        }
        // Process env wins; first file entry wins within file_env.
        if (std.posix.getenv(key) != null) continue;
        if (map.contains(key)) continue;
        const k = try allocator.dupe(u8, key);
        errdefer allocator.free(k);
        const v = try allocator.dupe(u8, value);
        errdefer allocator.free(v);
        try map.put(k, v);
    }
}
