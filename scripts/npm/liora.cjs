#!/usr/bin/env node
"use strict";

const { spawnSync } = require("node:child_process");
const { existsSync } = require("node:fs");
const { join, resolve } = require("node:path");

const packageRoot = resolve(__dirname, "../..");
const executable = process.platform === "win32" ? "liora.exe" : "liora";
const binaryPath = join(packageRoot, "bin", executable);

if (!existsSync(binaryPath)) {
  const build = spawnSync(process.execPath, [join(__dirname, "build.cjs")], {
    stdio: "inherit",
    env: process.env,
  });
  if (build.error) {
    console.error(build.error.message);
    process.exit(1);
  }
  if (build.status !== 0) {
    process.exit(build.status ?? 1);
  }
}

const result = spawnSync(binaryPath, process.argv.slice(2), { stdio: "inherit" });
if (result.error) {
  console.error(result.error.message);
  process.exit(1);
}

process.exit(result.status ?? 0);
