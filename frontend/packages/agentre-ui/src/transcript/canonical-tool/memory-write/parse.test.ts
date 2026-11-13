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

  it.each([
    `zsh -lc "echo agrctl create memory"`,
    `/usr/bin/zsh -lc "echo '/x/agrctl create memory'"`,
    `echo /x/agrctl create memory`,
  ])("ignores a wrapped non-call %s", (command) => {
    expect(isMemoryWriteCall(use(command))).toBe(false);
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
});

describe("parseMemoryWrite on the real Codex create block", () => {
  it("reads the first output line of the JSON command result", () => {
    const codexResult = result(
      JSON.stringify({
        exitCode: 0,
        output: "memory m1 created (agent CEO 助手)\n",
        status: "completed",
      }),
    );
    expect(parseMemoryWrite(use("x"), codexResult)).toEqual({
      id: 1,
      action: "created",
      layer: { kind: "agent" },
    });
  });
});
