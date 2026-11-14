import { describe, expect, it } from "vitest";

import type { TranscriptBlock } from "../../dto";

import { isMemoryWriteCall, parseMemoryWrite } from "./parse";

const use = (command: unknown): TranscriptBlock =>
  ({
    type: "tool_use",
    toolName: "Bash",
    toolInput: { command },
  }) as unknown as TranscriptBlock;

const result = (text: string): TranscriptBlock =>
  ({ type: "tool_result", text }) as unknown as TranscriptBlock;

describe("isMemoryWriteCall", () => {
  it.each([
    'agrctl create memory --summary "x"',
    'cd /repo && agrctl update memory m58 --detail "y"',
    "agrctl delete memory 58",
  ])("recognizes %s", (command) => {
    expect(isMemoryWriteCall(use(command))).toBe(true);
  });

  it.each([
    `/usr/bin/zsh -lc "/root/code/memory-round/desktop/build/bin/agrctl create memory --summary '用户偏好简短的中文回复'"`,
    `/usr/bin/zsh -lc "agrctl update memory 3 --detail 'x'"`,
    `bash -lc 'agrctl delete memory 3'`,
    "/usr/local/bin/agrctl create memory --summary x",
  ])("recognizes a wrapped or path-prefixed call %s", (command) => {
    expect(isMemoryWriteCall(use(command))).toBe(true);
  });

  // macOS 上装好的 agrctl 在 ~/Library/Application Support/agentre/bin/ 下，`command -v agrctl`
  // 给出的路径带空格，agent 照抄时要引起来或转义空格。
  it.each([
    `"/Users/u/Library/Application Support/agentre/bin/agrctl" create memory --summary x`,
    `'/Users/u/Library/Application Support/agentre/bin/agrctl' delete memory 3`,
    `/Users/u/Library/Application\\ Support/agentre/bin/agrctl update memory 3 --detail x`,
    `/bin/zsh -lc '"/Users/u/Library/Application Support/agentre/bin/agrctl" create memory --summary x'`,
    `/bin/zsh -lc "\\"/Users/u/Library/Application Support/agentre/bin/agrctl\\" create memory --summary 'x'"`,
  ])("recognizes a call through a quoted or escaped path %s", (command) => {
    expect(isMemoryWriteCall(use(command))).toBe(true);
  });

  it.each([
    `echo "/Users/u/Library/Application Support/agentre/bin/agrctl" create memory`,
    `"/x y/agrctl create memory"`,
    `"/x y/notagrctl" create memory`,
  ])("ignores a quoted non-call %s", (command) => {
    expect(isMemoryWriteCall(use(command))).toBe(false);
  });

  it.each([
    `zsh -lc "echo agrctl create memory"`,
    `/usr/bin/zsh -lc "echo '/x/agrctl create memory'"`,
    `echo /x/agrctl create memory`,
  ])("ignores a wrapped non-call %s", (command) => {
    expect(isMemoryWriteCall(use(command))).toBe(false);
  });

  // codex 用双引号包外壳时按 shell 规则转义了 \ $ ` "，内层的 `Application\ Support` 在外壳里是
  // `Application\\ Support`。
  it("recognizes an escaped-space path inside a double-quoted codex wrapper", () => {
    const command = `/bin/zsh -lc "/Users/u/Library/Application\\\\ Support/agentre/bin/agrctl update memory 3 --detail 'x'"`;
    expect(isMemoryWriteCall(use(command))).toBe(true);
  });

  it.each([
    `/bin/zsh -c "agrctl create memory --summary 'x'"`,
    `bash -c 'agrctl delete memory 3'`,
    `/bin/zsh -lc 'agrctl create memory --summary "it'"'"'s"'`,
    "NO_COLOR=1 agrctl create memory --summary x",
    `LANG=C FOO="a b" /usr/local/bin/agrctl delete memory 3`,
    "agrctl create Memory --summary x",
    "git status; agrctl create memory --summary x",
    "agrctl create memory --summary x | cat",
  ])(
    "recognizes a call behind -c, env assignments or a separator %s",
    (command) => {
      expect(isMemoryWriteCall(use(command))).toBe(true);
    },
  );

  // Windows 上装的是 agrctl.exe，路径分隔符是反斜杠；PowerShell 用 & 调用带引号的路径。
  it.each([
    "agrctl.exe create memory --summary x",
    `"C:\\Users\\u\\AppData\\Roaming\\agentre\\bin\\agrctl.exe" create memory --summary x`,
    `& 'C:\\Users\\u\\AppData\\Roaming\\agentre\\bin\\agrctl.exe' delete memory 3`,
    `C:\\Users\\u\\AppData\\Roaming\\agentre\\bin\\agrctl.exe update memory 3 --detail x`,
  ])("recognizes a Windows call %s", (command) => {
    expect(isMemoryWriteCall(use(command))).toBe(true);
  });

  it.each([
    `echo "; agrctl create memory"`,
    `echo 'a | agrctl delete memory 3'`,
    `git commit -m "docs (agrctl create memory)"`,
    "echo x # ; agrctl create memory",
    `/bin/zsh -lc "echo \\"; agrctl create memory\\""`,
  ])(
    "ignores a separator that sits inside quotes or a comment %s",
    (command) => {
      expect(isMemoryWriteCall(use(command))).toBe(false);
    },
  );

  // 转录里每条命令都过这个判据；反斜杠转义的分隔符不能让判据退化成平方级。
  it("stays linear on a long command full of escaped separators", () => {
    const command = "grep agrctl" + "\\|a".repeat(60_000);
    const start = performance.now();
    expect(isMemoryWriteCall(use(command))).toBe(false);
    expect(performance.now() - start).toBeLessThan(500);
  });

  it.each([
    "agrctl list memory --query lint",
    "agrctl create agent --name x",
    'echo "agrctl create memory"',
    "ls -la",
  ])("ignores %s", (command) => {
    expect(isMemoryWriteCall(use(command))).toBe(false);
  });

  it("ignores a block without a command string", () => {
    expect(isMemoryWriteCall(use(undefined))).toBe(false);
    expect(isMemoryWriteCall({ type: "tool_use" } as TranscriptBlock)).toBe(
      false,
    );
  });
});

describe("parseMemoryWrite", () => {
  const call = use('agrctl create memory --summary "x"');

  it.each([
    ["memory m7 created (agent 阿澈)", "created", { kind: "agent" }],
    ["memory m7 updated (global)", "updated", { kind: "global" }],
    [
      "memory m7 archived (project Harbor/App)",
      "archived",
      { kind: "project", name: "Harbor/App" },
    ],
    [
      "memory m7 already exists (department 研发 / 前端)",
      "exists",
      { kind: "department", name: "研发 / 前端" },
    ],
  ])("reads the result line %s", (line, action, layer) => {
    expect(parseMemoryWrite(call, result(line + "\n"))).toEqual({
      id: 7,
      action,
      layer,
      summary: "x",
    });
  });

  it("reads the line out of a JSON command result and skips the over-budget line", () => {
    const text = JSON.stringify({
      exitCode: 0,
      output:
        "memory m12 created (global)\nover budget: global is 40 characters over its memory budget; merge or shorten the memories there\n",
    });
    expect(parseMemoryWrite(call, result(text))?.id).toBe(12);
  });

  it("returns null while there is no result, or the command failed", () => {
    expect(parseMemoryWrite(call, undefined)).toBeNull();
    expect(parseMemoryWrite(call, result("error: forbidden"))).toBeNull();
    expect(
      parseMemoryWrite(
        call,
        result(
          JSON.stringify({ exitCode: 1, output: "memory m7 created (global)" }),
        ),
      ),
    ).toBeNull();
  });

  describe("the summary written by the command", () => {
    const line = result("memory m7 created (global)\n");

    it.each([
      [
        'agrctl create memory --summary "Run make lint before commit"',
        "Run make lint before commit",
      ],
      ['agrctl create memory --summary="用 pnpm" --detail x', "用 pnpm"],
      [
        `cd /repo && agrctl create memory --detail d --summary '提交前先跑 lint' && echo ok`,
        "提交前先跑 lint",
      ],
      [
        `/bin/zsh -lc "agrctl create memory --summary 'wrapped one'"`,
        "wrapped one",
      ],
      // agrctl 去掉 flag 前面任意个 "-"（ctlcmd/args.go parseArgs），单横线写法也是同一个 flag。
      ['agrctl create memory -summary "one dash"', "one dash"],
      ["agrctl create memory -summary=单横线 --detail x", "单横线"],
    ])("Given %s, Then the write carries %s", (command, summary) => {
      expect(parseMemoryWrite(use(command), line)?.summary).toBe(summary);
    });

    it("Given a command without --summary, Then the write has no summary", () => {
      const write = parseMemoryWrite(
        use("agrctl update memory 7 --detail only-detail"),
        result("memory m7 updated (global)\n"),
      );
      expect(write).not.toBeNull();
      expect(write?.summary).toBeUndefined();
    });

    it("Given two writes in one command, Then the first result line takes the first command's summary", () => {
      const command =
        'agrctl create memory --summary "first" && agrctl create memory --summary "second"';
      const both = result(
        "memory m7 created (global)\nmemory m8 created (global)\n",
      );
      expect(parseMemoryWrite(use(command), both)?.summary).toBe("first");
      expect(parseMemoryWrite(use(command), both)?.id).toBe(7);
    });

    it("Given the first write has no --summary, Then it does not borrow the second's", () => {
      const command =
        'agrctl update memory 7 --detail d && agrctl create memory --summary "second"';
      const write = parseMemoryWrite(
        use(command),
        result("memory m7 updated (global)\nmemory m8 created (global)\n"),
      );
      expect(write?.summary).toBeUndefined();
    });
  });
});
