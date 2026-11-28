#!/usr/bin/env node
"use strict";

const fs = require("fs");
const path = require("path");
const { spawnSync } = require("child_process");
const { install } = require("../install.js");

const binary = path.join(__dirname, "tackroom");

// The postinstall script downloads the binary. Package managers that skip
// install scripts (bun, --ignore-scripts, a future npm default) leave it
// missing, so the first run downloads it with the same checksum-verified code.
async function ensureBinary() {
  if (fs.existsSync(binary)) {
    return;
  }
  console.error("Downloading the tackroom binary (first run)...");
  try {
    await install();
  } catch (error) {
    console.error(`The tackroom binary could not be downloaded: ${error.message}`);
    console.error("Other ways to install tackroom:");
    console.error("  curl -fsSL https://raw.githubusercontent.com/yourconscience/tackroom/main/scripts/install.sh | sh");
    console.error("  brew install yourconscience/tap/tackroom");
    process.exit(1);
  }
}

ensureBinary().then(() => {
  const result = spawnSync(binary, process.argv.slice(2), { stdio: "inherit" });
  if (result.error) {
    console.error(result.error.message);
    process.exit(1);
  }
  process.exit(result.status === null ? 1 : result.status);
});
