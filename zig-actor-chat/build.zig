const std = @import("std");

pub fn build(b: *std.Build) void {
    // Prefer musl on Linux: system glibc on rolling distros (e.g. Arch) can
    // ship .sframe sections that Zig 0.15's linker does not yet handle.
    const default_target = b.resolveTargetQuery(.{
        .abi = if (@import("builtin").os.tag == .linux) .musl else null,
    });
    const target = b.standardTargetOptions(.{ .default_target = default_target.query });
    const optimize = b.standardOptimizeOption(.{});

    const inklist = b.createModule(.{
        .root_source_file = b.path("vendor/InkList/src/root.zig"),
        .target = target,
        .optimize = optimize,
    });

    const httpz_dep = b.dependency("httpz", .{
        .target = target,
        .optimize = optimize,
    });

    const root_mod = b.createModule(.{
        .root_source_file = b.path("src/main.zig"),
        .target = target,
        .optimize = optimize,
        .imports = &.{
            .{ .name = "InkList", .module = inklist },
            .{ .name = "httpz", .module = httpz_dep.module("httpz") },
        },
    });

    const exe = b.addExecutable(.{
        .name = "zig-actor-chat",
        .root_module = root_mod,
    });
    b.installArtifact(exe);

    const run_cmd = b.addRunArtifact(exe);
    if (b.args) |args| run_cmd.addArgs(args);
    const run_step = b.step("run", "Run the chat server");
    run_step.dependOn(&run_cmd.step);

    const unit_tests = b.addTest(.{
        .root_module = b.createModule(.{
            .root_source_file = b.path("src/main.zig"),
            .target = target,
            .optimize = optimize,
            .imports = &.{
                .{ .name = "InkList", .module = inklist },
                .{ .name = "httpz", .module = httpz_dep.module("httpz") },
            },
        }),
    });
    const run_unit_tests = b.addRunArtifact(unit_tests);
    const test_step = b.step("test", "Run unit tests");
    test_step.dependOn(&run_unit_tests.step);
}
