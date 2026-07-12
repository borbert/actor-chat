const std = @import("std");
const Allocator = std.mem.Allocator;

pub const ConvexError = error{
    HttpFailed,
    FunctionFailed,
    DecodeFailed,
    OutOfMemory,
};

pub const SentMessage = struct {
    id: []u8,
    created_at: f64,

    pub fn deinit(self: *SentMessage, allocator: Allocator) void {
        allocator.free(self.id);
    }
};

pub const ConvexUser = struct {
    id: []u8,

    pub fn deinit(self: *ConvexUser, allocator: Allocator) void {
        allocator.free(self.id);
    }
};

/// Minimal Convex HTTP client (query/mutation).
pub const Client = struct {
    allocator: Allocator,
    base_url: []const u8,
    http: *std.http.Client,
    token: ?[]const u8 = null,
    owns_base: bool = true,
    owns_token: bool = false,

    pub fn init(allocator: Allocator, http: *std.http.Client, base_url: []const u8) !Client {
        const trimmed = std.mem.trimRight(u8, base_url, "/");
        return .{
            .allocator = allocator,
            .base_url = try allocator.dupe(u8, trimmed),
            .http = http,
            .token = null,
            .owns_base = true,
            .owns_token = false,
        };
    }

    pub fn deinit(self: *Client) void {
        if (self.owns_base) self.allocator.free(self.base_url);
        if (self.owns_token) if (self.token) |t| self.allocator.free(t);
    }

    /// Clone that authenticates as the bearer of `token`.
    pub fn withAuth(self: *const Client, token: []const u8) Client {
        return .{
            .allocator = self.allocator,
            .base_url = self.base_url,
            .http = self.http,
            .token = token,
            .owns_base = false,
            .owns_token = false,
        };
    }

    pub fn query(self: *Client, path: []const u8, args_json: []const u8) ConvexError![]u8 {
        return self.call("query", path, args_json);
    }

    pub fn mutation(self: *Client, path: []const u8, args_json: []const u8) ConvexError![]u8 {
        return self.call("mutation", path, args_json);
    }

    fn call(self: *Client, kind: []const u8, path: []const u8, args_json: []const u8) ConvexError![]u8 {
        const url = std.fmt.allocPrint(self.allocator, "{s}/api/{s}", .{ self.base_url, kind }) catch return error.OutOfMemory;
        defer self.allocator.free(url);

        const quoted_path = jsonString(self.allocator, path) catch return error.OutOfMemory;
        defer self.allocator.free(quoted_path);

        var payload_list: std.ArrayList(u8) = .{};
        defer payload_list.deinit(self.allocator);
        payload_list.appendSlice(self.allocator, "{\"path\":") catch return error.OutOfMemory;
        payload_list.appendSlice(self.allocator, quoted_path) catch return error.OutOfMemory;
        payload_list.appendSlice(self.allocator, ",\"args\":") catch return error.OutOfMemory;
        payload_list.appendSlice(self.allocator, args_json) catch return error.OutOfMemory;
        payload_list.appendSlice(self.allocator, ",\"format\":\"json\"}") catch return error.OutOfMemory;
        const payload = payload_list.items;

        var response_body: std.Io.Writer.Allocating = .init(self.allocator);
        defer response_body.deinit();

        var auth_buf: [8192]u8 = undefined;
        var headers_buf: [1]std.http.Header = undefined;
        var headers: []const std.http.Header = &.{};
        if (self.token) |token| {
            const auth = std.fmt.bufPrint(&auth_buf, "Bearer {s}", .{token}) catch return error.HttpFailed;
            headers_buf[0] = .{ .name = "authorization", .value = auth };
            headers = headers_buf[0..1];
        }

        const result = self.http.fetch(.{
            .location = .{ .url = url },
            .method = .POST,
            .payload = payload,
            .extra_headers = headers,
            .headers = .{
                .content_type = .{ .override = "application/json" },
            },
            .response_writer = &response_body.writer,
        }) catch return error.HttpFailed;

        if (result.status.class() != .success) return error.HttpFailed;
        return parseConvexValue(self.allocator, response_body.written()) catch |err| switch (err) {
            error.FunctionFailed => return error.FunctionFailed,
            else => return error.DecodeFailed,
        };
    }

    pub fn parseSentMessage(allocator: Allocator, value_json: []const u8) ConvexError!SentMessage {
        const parsed = std.json.parseFromSlice(struct {
            @"_id": []const u8,
            createdAt: f64,
        }, allocator, value_json, .{ .allocate = .alloc_always, .ignore_unknown_fields = true }) catch return error.DecodeFailed;
        defer parsed.deinit();
        return .{
            .id = allocator.dupe(u8, parsed.value.@"_id") catch return error.OutOfMemory,
            .created_at = parsed.value.createdAt,
        };
    }

    pub fn parseUser(allocator: Allocator, value_json: []const u8) ConvexError!ConvexUser {
        const parsed = std.json.parseFromSlice(struct {
            @"_id": []const u8,
        }, allocator, value_json, .{ .allocate = .alloc_always, .ignore_unknown_fields = true }) catch return error.DecodeFailed;
        defer parsed.deinit();
        return .{ .id = allocator.dupe(u8, parsed.value.@"_id") catch return error.OutOfMemory };
    }
};

fn jsonString(allocator: Allocator, s: []const u8) ![]u8 {
    return try std.json.Stringify.valueAlloc(allocator, s, .{});
}

fn parseConvexValue(allocator: Allocator, body: []const u8) ![]u8 {
    const parsed = try std.json.parseFromSlice(struct {
        status: []const u8,
        value: ?std.json.Value = null,
        errorMessage: ?[]const u8 = null,
    }, allocator, body, .{ .ignore_unknown_fields = true, .allocate = .alloc_always });
    defer parsed.deinit();

    if (!std.mem.eql(u8, parsed.value.status, "success")) {
        return error.FunctionFailed;
    }
    const value = parsed.value.value orelse return error.FunctionFailed;
    return try std.json.Stringify.valueAlloc(allocator, value, .{});
}
