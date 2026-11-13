import { describe, expect, it } from "vitest";

import { unwrapShellCommand } from "./shell-command";

describe("unwrapShellCommand", () => {
  it("strips an absolute-path zsh -lc double-quoted wrapper", () => {
    expect(unwrapShellCommand(`/usr/bin/zsh -lc "agrctl list memory"`)).toBe(
      "agrctl list memory",
    );
  });

  it("strips a bare bash -lc single-quoted wrapper", () => {
    expect(unwrapShellCommand(`bash -lc 'echo "hi"'`)).toBe(`echo "hi"`);
  });

  // codex 按 shell 规则给外壳加引号：双引号里转义 \ $ ` "，单引号里的撇号写成 '"'"'。
  it("undoes the shell quoting of the wrapped script", () => {
    expect(
      unwrapShellCommand(
        `/bin/zsh -lc "echo \\$HOME \\\`id\\\` \\"a\\\\ b\\" 'x'"`,
      ),
    ).toBe(`echo $HOME \`id\` "a\\ b" 'x'`);
    expect(unwrapShellCommand(`/bin/zsh -lc 'echo "it'"'"'s"'`)).toBe(
      `echo "it's"`,
    );
  });

  it("strips a -c wrapper", () => {
    expect(unwrapShellCommand(`/bin/sh -c "git status"`)).toBe("git status");
  });

  it("leaves a shell call with more than one argument after -lc as it is", () => {
    expect(unwrapShellCommand(`sh -lc "x" "y"`)).toBe(`sh -lc "x" "y"`);
    expect(unwrapShellCommand(`bash -lc 'a' && b`)).toBe(`bash -lc 'a' && b`);
  });

  it("returns an unwrapped command trimmed but otherwise untouched", () => {
    expect(unwrapShellCommand("  ls -la\npwd ")).toBe("ls -la\npwd");
  });
});
