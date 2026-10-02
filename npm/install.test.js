"use strict";

const assert = require("node:assert/strict");
const { test } = require("node:test");
const {
  platformTarget,
  assetName,
  releaseAssetUrl,
  sha256,
  expectedChecksum,
} = require("./install.js");

test("platformTarget maps supported platforms to goreleaser targets", () => {
  assert.deepEqual(platformTarget("darwin", "arm64"), { goos: "darwin", goarch: "arm64" });
  assert.deepEqual(platformTarget("darwin", "x64"), { goos: "darwin", goarch: "amd64" });
  assert.deepEqual(platformTarget("linux", "arm64"), { goos: "linux", goarch: "arm64" });
  assert.deepEqual(platformTarget("linux", "x64"), { goos: "linux", goarch: "amd64" });
});

test("platformTarget rejects unsupported platforms", () => {
  assert.equal(platformTarget("win32", "x64"), null);
  assert.equal(platformTarget("linux", "riscv64"), null);
});

test("assetName and releaseAssetUrl follow goreleaser layout", () => {
  const target = platformTarget("darwin", "arm64");
  assert.equal(assetName("1.2.3", target), "tackroom_1.2.3_darwin_arm64.tar.gz");
  assert.equal(
    releaseAssetUrl("1.2.3", assetName("1.2.3", target)),
    "https://github.com/yourconscience/tackroom/releases/download/v1.2.3/tackroom_1.2.3_darwin_arm64.tar.gz",
  );
});

test("sha256 computes the expected digest", () => {
  assert.equal(sha256(Buffer.from("abc")), "ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad");
});

test("expectedChecksum finds the matching asset line", () => {
  const checksums = [
    "1111111111111111111111111111111111111111111111111111111111111111  tackroom_1.2.3_darwin_amd64.tar.gz",
    "2222222222222222222222222222222222222222222222222222222222222222  tackroom_1.2.3_darwin_arm64.tar.gz",
    "",
  ].join("\n");
  assert.equal(
    expectedChecksum(checksums, "tackroom_1.2.3_darwin_arm64.tar.gz"),
    "2222222222222222222222222222222222222222222222222222222222222222",
  );
  assert.equal(expectedChecksum(checksums, "tackroom_1.2.3_linux_arm64.tar.gz"), null);
});
