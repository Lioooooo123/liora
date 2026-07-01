#!/usr/bin/env node
"use strict";

const { spawnSync } = require("node:child_process");
const { existsSync } = require("node:fs");
const { join, resolve } = require("node:path");

const packageRoot = resolve(__dirname, "../..");
const executable = process.platform === "win32" ? "liora.exe" : "liora";
const binaryPath = join(packageRoot, "bin", executable);

if (!existsSync(binaryPath)) {
  console.error(`Liora binary is missing at ${binaryPath}.`);
  console.error("Run `npm rebuild -g @lioooooo123/liora` or reinstall the package.");
  process.exit(1);
}

const result = spawnSync(binaryPath, process.argv.slice(2), { stdio: "inherit" });
if (result.error) {
  console.error(result.error.message);
  process.exit(1);
}

process.exit(result.status ?? 0);
