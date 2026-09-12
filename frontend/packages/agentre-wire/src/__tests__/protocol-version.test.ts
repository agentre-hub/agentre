import { existsSync, readFileSync } from "node:fs";
import { resolve } from "node:path";
import { getOption, hasOption } from "@bufbuild/protobuf";
import { describe, expect, it } from "vitest";

import {
  file_agentre_wire_wire,
  protocol_version,
} from "../gen/agentre/wire/wire_pb";
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
  it("given the schema owns the protocol version, when the constant is read, then it is the file option the descriptor carries", () => {
    // 缺失的文件选项与「填了空串」在 proto3 里是同一个零值,读出来都是 ""。
    // 而 "" 在握手的窗口比较里永远不匹配 —— 每一次握手都被拒,却没有任何编译期信号。
    expect(hasOption(file_agentre_wire_wire, protocol_version)).toBe(true);
    expect(PROTOCOL_VERSION).toBe(
      getOption(file_agentre_wire_wire, protocol_version),
    );
    expect(PROTOCOL_VERSION).toMatch(/^\d+\.\d+\.\d+$/);
  });

  it("given the package manifest only restates that version, when the two are compared, then they are byte identical", () => {
    // 本包是个真的 npm 包,消费方按 `version` pin,所以那一格不能凭空消失 —— 但它
    // 现在只是 schema 上那一格的复述,方向是 schema → package.json。抬版本时先改
    // `.proto` 再重新生成,`package.json` 跟着抬;漏抬就在这里判红。
    const manifest = JSON.parse(readFileSync(locateManifest(), "utf8")) as {
      name?: string;
      version?: string;
    };

    expect(manifest.name).toBe("@agentre-hub/agentre-wire");
    expect(manifest.version).toBe(PROTOCOL_VERSION);
  });
});
