import { readFileSync, existsSync } from "node:fs";
import { resolve } from "node:path";
import { describe, expect, it } from "vitest";

function locateSource(): string {
  const found = [
    resolve(process.cwd(), "packages/agentre-wire/src"),
    resolve(process.cwd(), "src"),
  ].find((candidate) => existsSync(`${candidate}/index.ts`));

  if (!found) {
    throw new Error("agentre-wire source not found from either workspace root");
  }

  return found;
}

function locatePackage(): string {
  const found = [
    resolve(process.cwd(), "packages/agentre-wire"),
    process.cwd(),
  ].find((candidate) => existsSync(`${candidate}/package.json`));

  if (!found) {
    throw new Error(
      "agentre-wire package not found from either workspace root",
    );
  }

  return found;
}

const src = locateSource();
const packageRoot = locatePackage();

describe("agentre-wire public boundary", () => {
  it("is independently buildable from a Git subdirectory", () => {
    const manifest = JSON.parse(
      readFileSync(`${packageRoot}/package.json`, "utf8"),
    ) as { devDependencies?: Record<string, string> };
    expect(manifest.devDependencies?.["@bufbuild/protobuf"]).toBe("2.14.0");
    expect(existsSync(`${packageRoot}/dist/index.js`)).toBe(true);
  });

  it("publishes only the typed Protobuf transport boundary", () => {
    expect(existsSync(`${src}/envelope.ts`)).toBe(false);
    expect(existsSync(`${src}/codec.ts`)).toBe(false);
    const barrel = readFileSync(`${src}/index.ts`, "utf8");
    expect(barrel).not.toContain('export * from "./envelope"');
    expect(barrel).not.toContain('export * from "./codec"');
    expect(barrel).toContain('export * from "./rpc"');
  });

  it("owns one unversioned Protobuf schema package", () => {
    const protoPath = `${packageRoot}/proto/agentre/wire/wire.proto`;
    expect(existsSync(protoPath)).toBe(true);
    expect(existsSync(`${packageRoot}/proto/agentre/wire/v1`)).toBe(false);

    const proto = readFileSync(protoPath, "utf8");
    expect(proto).toContain("package agentre.wire;");
    expect(proto).toContain(
      'option go_package = "github.com/agentre-hub/agentre/pkg/wire/agentrewire;agentrewire";',
    );
    expect(proto).not.toMatch(/agentre\.wire\.v\d+|agentrewire\/v\d+/);

    expect(existsSync(`${src}/gen/agentre/wire/wire_pb.ts`)).toBe(true);
    expect(existsSync(`${src}/gen/agentre/wire/v1`)).toBe(false);
    // 生成的 Go 不再在本包里留一份拷贝：它直接落进独立 module
    // github.com/agentre-hub/agentre/pkg/wire，由桌面仓与 agentre-server 共同 import。
    expect(existsSync(`${packageRoot}/gen`)).toBe(false);
    expect(
      existsSync(`${packageRoot}/../../../pkg/wire/agentrewire/wire.pb.go`),
    ).toBe(true);
  });
});
