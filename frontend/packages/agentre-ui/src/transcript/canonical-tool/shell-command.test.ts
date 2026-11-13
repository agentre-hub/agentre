import { describe, expect, it } from "vitest";

import { unwrapShellCommand } from "./shell-command";

describe("unwrapShellCommand", () => {
  it("strips an absolute-path zsh -lc double-quoted wrapper", () => {
    expect(unwrapShellCommand(`/usr/bin/zsh -lc "agrctl list memory"`)).toBe(
      "agrctl list memory",
    );
  });

  it("strips a bare bash -lc single-quoted wrapper and unescapes the quote", () => {
    expect(unwrapShellCommand(`bash -lc 'echo \\'hi\\''`)).toBe("echo 'hi'");
  });

  it("returns an unwrapped command trimmed but otherwise untouched", () => {
    expect(unwrapShellCommand("  ls -la\npwd ")).toBe("ls -la\npwd");
  });
});
