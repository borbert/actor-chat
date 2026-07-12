const std = @import("std");
const Allocator = std.mem.Allocator;
const rsa = std.crypto.Certificate.rsa;

pub const AuthError = error{
    JwksFetchFailed,
    InvalidToken,
    MissingKid,
    UnknownKid,
    BadClaims,
    OutOfMemory,
};

const RsaKey = struct {
    kid: []u8,
    n: []u8,
    e: []u8,

    fn deinit(self: *RsaKey, allocator: Allocator) void {
        allocator.free(self.kid);
        allocator.free(self.n);
        allocator.free(self.e);
    }
};

/// Validates Clerk-issued JWTs against the instance JWKS (RS256, aud="convex").
pub const TokenValidator = struct {
    allocator: Allocator,
    issuer: []u8,
    jwks_url: []u8,
    http: *std.http.Client,
    keys: std.ArrayList(RsaKey),
    mutex: std.Thread.Mutex = .{},

    pub fn init(allocator: Allocator, http: *std.http.Client, issuer: []const u8) AuthError!*TokenValidator {
        const self = allocator.create(TokenValidator) catch return error.OutOfMemory;
        errdefer allocator.destroy(self);

        const trimmed = std.mem.trimRight(u8, issuer, "/");
        const issuer_owned = allocator.dupe(u8, trimmed) catch return error.OutOfMemory;
        errdefer allocator.free(issuer_owned);
        const jwks_url = std.fmt.allocPrint(allocator, "{s}/.well-known/jwks.json", .{trimmed}) catch return error.OutOfMemory;
        errdefer allocator.free(jwks_url);

        self.* = .{
            .allocator = allocator,
            .issuer = issuer_owned,
            .jwks_url = jwks_url,
            .http = http,
            .keys = .{},
        };

        self.refreshKeys() catch return error.JwksFetchFailed;
        return self;
    }

    pub fn deinit(self: *TokenValidator) void {
        for (self.keys.items) |*k| k.deinit(self.allocator);
        self.keys.deinit(self.allocator);
        self.allocator.free(self.issuer);
        self.allocator.free(self.jwks_url);
        self.allocator.destroy(self);
    }

    /// Validate a token; returns the Clerk subject (caller frees).
    pub fn validate(self: *TokenValidator, token: []const u8) AuthError![]u8 {
        const parts = splitJwt(token) orelse return error.InvalidToken;
        const header_json = decodeB64Url(self.allocator, parts.header) catch return error.InvalidToken;
        defer self.allocator.free(header_json);
        const payload_json = decodeB64Url(self.allocator, parts.payload) catch return error.InvalidToken;
        defer self.allocator.free(payload_json);
        const signature = decodeB64Url(self.allocator, parts.signature) catch return error.InvalidToken;
        defer self.allocator.free(signature);

        const header = std.json.parseFromSlice(struct {
            alg: []const u8,
            kid: ?[]const u8 = null,
        }, self.allocator, header_json, .{ .ignore_unknown_fields = true, .allocate = .alloc_always }) catch return error.InvalidToken;
        defer header.deinit();

        if (!std.mem.eql(u8, header.value.alg, "RS256")) return error.InvalidToken;
        const kid = header.value.kid orelse return error.MissingKid;

        const signing_input_len = parts.header.len + 1 + parts.payload.len;
        const signing_input = self.allocator.alloc(u8, signing_input_len) catch return error.OutOfMemory;
        defer self.allocator.free(signing_input);
        @memcpy(signing_input[0..parts.header.len], parts.header);
        signing_input[parts.header.len] = '.';
        @memcpy(signing_input[parts.header.len + 1 ..], parts.payload);

        var verified = self.verifyWithCachedKey(kid, signing_input, signature);
        if (!verified) {
            self.refreshKeys() catch {};
            verified = self.verifyWithCachedKey(kid, signing_input, signature);
        }
        if (!verified) return error.UnknownKid;

        const claims = std.json.parseFromSlice(struct {
            sub: []const u8,
            iss: []const u8,
            aud: std.json.Value,
            exp: i64,
        }, self.allocator, payload_json, .{ .ignore_unknown_fields = true, .allocate = .alloc_always }) catch return error.BadClaims;
        defer claims.deinit();

        if (!std.mem.eql(u8, claims.value.iss, self.issuer)) return error.BadClaims;
        if (!audienceOk(claims.value.aud)) return error.BadClaims;
        const now = std.time.timestamp();
        if (claims.value.exp < now) return error.BadClaims;

        return self.allocator.dupe(u8, claims.value.sub) catch return error.OutOfMemory;
    }

    fn verifyWithCachedKey(self: *TokenValidator, kid: []const u8, signing_input: []const u8, signature: []const u8) bool {
        self.mutex.lock();
        defer self.mutex.unlock();
        for (self.keys.items) |key| {
            if (!std.mem.eql(u8, key.kid, kid)) continue;
            return verifyRs256(key.n, key.e, signing_input, signature);
        }
        return false;
    }

    fn refreshKeys(self: *TokenValidator) !void {
        var response_body: std.Io.Writer.Allocating = .init(self.allocator);
        defer response_body.deinit();

        const result = try self.http.fetch(.{
            .location = .{ .url = self.jwks_url },
            .method = .GET,
            .response_writer = &response_body.writer,
        });
        if (result.status.class() != .success) return error.JwksFetchFailed;

        const parsed = try std.json.parseFromSlice(struct {
            keys: []struct {
                kid: ?[]const u8 = null,
                kty: []const u8,
                alg: ?[]const u8 = null,
                n: ?[]const u8 = null,
                e: ?[]const u8 = null,
            },
        }, self.allocator, response_body.written(), .{ .ignore_unknown_fields = true, .allocate = .alloc_always });
        defer parsed.deinit();

        var fresh: std.ArrayList(RsaKey) = .{};
        errdefer {
            for (fresh.items) |*k| k.deinit(self.allocator);
            fresh.deinit(self.allocator);
        }

        for (parsed.value.keys) |jwk| {
            if (jwk.kid == null or jwk.n == null or jwk.e == null) continue;
            if (!std.mem.eql(u8, jwk.kty, "RSA")) continue;
            if (jwk.alg) |alg| if (!std.mem.eql(u8, alg, "RS256")) continue;

            const n = try decodeB64Url(self.allocator, jwk.n.?);
            errdefer self.allocator.free(n);
            const e = try decodeB64Url(self.allocator, jwk.e.?);
            errdefer self.allocator.free(e);
            const kid_owned = try self.allocator.dupe(u8, jwk.kid.?);
            errdefer self.allocator.free(kid_owned);

            try fresh.append(self.allocator, .{ .kid = kid_owned, .n = n, .e = e });
        }

        self.mutex.lock();
        defer self.mutex.unlock();
        for (self.keys.items) |*k| k.deinit(self.allocator);
        self.keys.deinit(self.allocator);
        self.keys = fresh;
    }
};

fn audienceOk(aud: std.json.Value) bool {
    return switch (aud) {
        .string => |s| std.mem.eql(u8, s, "convex"),
        .array => |arr| for (arr.items) |item| {
            if (item == .string and std.mem.eql(u8, item.string, "convex")) break true;
        } else false,
        else => false,
    };
}

fn splitJwt(token: []const u8) ?struct { header: []const u8, payload: []const u8, signature: []const u8 } {
    const d1 = std.mem.indexOfScalar(u8, token, '.') orelse return null;
    const d2 = std.mem.indexOfScalarPos(u8, token, d1 + 1, '.') orelse return null;
    if (std.mem.indexOfScalarPos(u8, token, d2 + 1, '.') != null) return null;
    return .{
        .header = token[0..d1],
        .payload = token[d1 + 1 .. d2],
        .signature = token[d2 + 1 ..],
    };
}

fn decodeB64Url(allocator: Allocator, input: []const u8) ![]u8 {
    const Decoder = std.base64.url_safe_no_pad.Decoder;
    const out_len = try Decoder.calcSizeForSlice(input);
    const out = try allocator.alloc(u8, out_len);
    errdefer allocator.free(out);
    try Decoder.decode(out, input);
    return out;
}

fn verifyRs256(n: []const u8, e: []const u8, message: []const u8, signature: []const u8) bool {
    const public_key = rsa.PublicKey.fromBytes(e, n) catch return false;
    return switch (signature.len) {
        256 => verifyModulus(256, signature, message, public_key),
        384 => verifyModulus(384, signature, message, public_key),
        512 => verifyModulus(512, signature, message, public_key),
        else => false,
    };
}

fn verifyModulus(comptime modulus_len: usize, signature: []const u8, message: []const u8, public_key: rsa.PublicKey) bool {
    var sig: [modulus_len]u8 = undefined;
    @memcpy(&sig, signature[0..modulus_len]);
    rsa.PKCS1v1_5Signature.verify(modulus_len, sig, message, public_key, std.crypto.hash.sha2.Sha256) catch return false;
    return true;
}
