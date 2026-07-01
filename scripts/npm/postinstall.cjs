#!/usr/bin/env node
"use strict";

const { mkdirSync } = require("node:fs");
const { join, resolve } = require("node:path");
const { spawnSync } = require("node:child_process");

if (process.env.LIORA_SKIP_NPM_BUILD === "1") {
  console.log("Skipping Liora binary build because LIORA_SKIP_NPM_BUILD=1.");
  process.exit(0);
}

const packageRoot = resolve(__dirname, "../..");
const binDir = join(packageRoot, "bin");
const executable = process.platform === "win32" ? "liora.exe" : "liora";
const outputPath = join(binDir, executable);

mkdirSync(binDir, { recursive: true });

const result = spawnSync("go", ["build", "-o", outputPath, "./apps/cli"], {
  cwd: packageRoot,
  stdio: "inherit",
  env: process.env,
});

if (result.error) {
  console.error("Failed to build Liora. Install Go 1.24+ and retry.");
  console.error(result.error.message);
  process.exit(1);
}

if (result.status !== 0) {
  console.error("Failed to build Liora during npm install.");
  process.exit(result.status ?? 1);
}

console.log(`Built Liora binary at ${outputPath}`);
