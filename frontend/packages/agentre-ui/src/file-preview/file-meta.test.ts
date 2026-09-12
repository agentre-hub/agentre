import { describe, expect, it } from "vitest";

import { basename, dirname } from "./file-meta";

describe("basename / dirname", () => {
  it("splits POSIX relPaths into name and directory prefix", () => {
    expect(basename("src/lib/utils.ts")).toBe("utils.ts");
    expect(dirname("src/lib/utils.ts")).toBe("src/lib/");
  });

  // Windows 会话的 relPath 用 "\" 分隔（previewable 的 toRelPath 按 cwd 的分隔符
  // 切）：只认 "/" 的话文件名照样切得出来、目录前缀却恒为空串，header 里那一栏
  // 会无声消失。
  it("recognises the Windows separator in both halves", () => {
    expect(basename("src\\lib\\utils.ts")).toBe("utils.ts");
    expect(dirname("src\\lib\\utils.ts")).toBe("src\\lib\\");
  });

  it("gives an empty directory prefix for a file at the root", () => {
    expect(basename("README.md")).toBe("README.md");
    expect(dirname("README.md")).toBe("");
  });
});
