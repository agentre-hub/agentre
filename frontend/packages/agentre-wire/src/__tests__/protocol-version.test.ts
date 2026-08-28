import { existsSync, readFileSync } from "node:fs";
import { resolve } from "node:path";
import { describe, expect, it } from "vitest";

import { PROTOCOL_VERSION } from "../protocol-version";

// 本包既被自己的 vitest 跑,也被宿主 app 的 vitest 一并收走,cwd 不同 ——
// public-boundary.test.ts 已经踩过同一个坑,这里沿用它的解析方式。
function locateManifest(): string {
  const found = [
    resolve(process.cwd(), "packages/agentre-wire/package.json"),
    resolve(process.cwd(), "package.json"),
  ].find((candidate) => existsSync(candidate));
  if (!found) throw new Error("agentre-wire package.json not found");
  return found;
}

describe("PROTOCOL_VERSION", () => {
  it("given the package manifest owns the protocol version, when the exported constant is read, then the two are byte identical", () => {
    const manifest = JSON.parse(readFileSync(locateManifest(), "utf8")) as {
      name?: string;
      version?: string;
    };

    expect(manifest.name).toBe("@agentre-hub/agentre-wire");
    expect(manifest.version).toBeTruthy();
    expect(PROTOCOL_VERSION).toBe(manifest.version);
  });
});
