import { describe, expect, it } from "vitest";

import { pathBasename } from "./work-root-path";

describe("pathBasename", () => {
  it.each([
    ["/work/demo-app", "demo-app"],
    ["/work/demo-app/", "demo-app"],
    ["C:\\code\\app\\.worktrees\\feat", "feat"],
    ["C:\\code\\app\\", "app"],
    ["demo", "demo"],
    // 没有可显示的一段时原样交回，不出空标签。
    ["/", "/"],
  ])("Given %s, Then the label is %s", (path, name) => {
    expect(pathBasename(path)).toBe(name);
  });
});
