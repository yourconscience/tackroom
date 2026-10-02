"use strict";

// Downloads the tackroom binary for this platform from GitHub Releases and
// verifies its sha256 against the published checksums.txt before extraction.
// No code from the archive is executed; only the checksum-verified binary is
// unpacked next to the bin shim.

const crypto = require("crypto");
const fs = require("fs");
const https = require("https");
const os = require("os");
const path = require("path");
const { spawnSync } = require("child_process");

const REPO = "yourconscience/tackroom";
const MAX_REDIRECTS = 5;

function platformTarget(platform = process.platform, arch = process.arch) {
  const goos = platform === "darwin" ? "darwin" : platform === "linux" ? "linux" : null;
  const goarch = arch === "arm64" ? "arm64" : arch === "x64" ? "amd64" : null;
  if (!goos || !goarch) {
    return null;
  }
  return { goos, goarch };
}

function assetName(version, target) {
  return `tackroom_${version}_${target.goos}_${target.goarch}.tar.gz`;
}

function releaseAssetUrl(version, name) {
  return `https://github.com/${REPO}/releases/download/v${version}/${name}`;
}

function sha256(buffer) {
  return crypto.createHash("sha256").update(buffer).digest("hex");
}

function expectedChecksum(checksumsText, filename) {
  for (const line of checksumsText.split("\n")) {
    const parts = line.trim().split(/\s+/);
    if (parts.length === 2 && parts[1] === filename) {
      return parts[0];
    }
  }
  return null;
}

function fetchBuffer(url, redirects = 0) {
  return new Promise((resolve, reject) => {
    https
      .get(url, (response) => {
        if (response.statusCode >= 300 && response.statusCode < 400 && response.headers.location) {
          response.resume();
          if (redirects >= MAX_REDIRECTS) {
            reject(new Error(`too many redirects fetching ${url}`));
            return;
          }
          fetchBuffer(new URL(response.headers.location, url).toString(), redirects + 1).then(resolve, reject);
          return;
        }
        if (response.statusCode !== 200) {
          response.resume();
          reject(new Error(`${response.statusCode} ${response.statusMessage} fetching ${url}`));
          return;
        }
        const chunks = [];
        response.on("data", (chunk) => chunks.push(chunk));
        response.on("end", () => resolve(Buffer.concat(chunks)));
        response.on("error", reject);
      })
      .on("error", reject);
  });
}

async function install(options = {}) {
  const pkg = JSON.parse(fs.readFileSync(path.join(__dirname, "package.json"), "utf8"));
  const version = options.version || process.env.TACKROOM_VERSION || pkg.version;
  const target = platformTarget(options.platform, options.arch);
  if (!target) {
    throw new Error(
      `tackroom has no prebuilt binary for ${options.platform || process.platform}/${options.arch || process.arch}; use brew or scripts/install.sh instead`,
    );
  }

  const name = assetName(version, target);
  const [archive, checksums] = await Promise.all([
    fetchBuffer(releaseAssetUrl(version, name)),
    fetchBuffer(releaseAssetUrl(version, "checksums.txt")),
  ]);

  const want = expectedChecksum(checksums.toString("utf8"), name);
  const got = sha256(archive);
  if (!want || want !== got) {
    throw new Error(`checksum mismatch for ${name}: expected ${want || "<missing>"}, got ${got}`);
  }

  const binDir = path.join(__dirname, "bin");
  fs.mkdirSync(binDir, { recursive: true });
  const tmpArchive = path.join(os.tmpdir(), `${name}.${process.pid}`);
  try {
    fs.writeFileSync(tmpArchive, archive);
    const extract = spawnSync("tar", ["-xzf", tmpArchive, "-C", binDir], { stdio: "pipe", encoding: "utf8" });
    if (extract.error) {
      throw extract.error;
    }
    if (extract.status !== 0) {
      throw new Error(`tar exited ${extract.status}: ${extract.stderr}`);
    }
  } finally {
    fs.rmSync(tmpArchive, { force: true });
  }

  const binary = path.join(binDir, "tackroom");
  fs.chmodSync(binary, 0o755);
  return binary;
}

module.exports = { platformTarget, assetName, releaseAssetUrl, sha256, expectedChecksum, install };

if (require.main === module) {
  install()
    .then((binary) => {
      console.log(`tackroom installed to ${binary}`);
    })
    .catch((error) => {
      console.error(`tackroom postinstall failed: ${error.message}`);
      console.error("The CLI still works via brew or scripts/install.sh; see the README.");
      process.exit(1);
    });
}
