import { describe, expect, it } from "vitest";

import type { TranscriptBlock } from "@agentre-ai/agentre-ui";

import { displayName, tier } from "./tier";

// canonical 字面量在测试里天然缺 wailsjs 生成类的 convertValues 方法;
// 沿用其余 canonical-tool 卡片测试同一手法(agent-spawn/card.test.tsx:44)
// 经 unknown 收窄绕过。
function block(partial: Record<string, unknown>): TranscriptBlock {
  return { type: "tool_use", ...partial } as unknown as TranscriptBlock;
}

describe("tier", () => {
  // ① canonical.kind 命中出组:阻塞用户的卡片永不进组(Hard invariant 3)。
  it("canonical.kind agent.spawn → out", () => {
    expect(
      tier(block({ canonical: { kind: "agent.spawn", agentSpawn: {} } })),
    ).toBe("out");
  });

  it("canonical.kind user.ask → out", () => {
    expect(
      tier(
        block({
          canonical: {
            kind: "user.ask",
            userAsk: { requestId: "r1", questions: [] },
          },
        }),
      ),
    ).toBe("out");
  });

  it("canonical.kind plan.approve_request → out", () => {
    expect(
      tier(
        block({
          canonical: {
            kind: "plan.approve_request",
            planApprove: { requestId: "r1", planText: "" },
          },
        }),
      ),
    ).toBe("out");
  });

  it("canonical.kind tool.permission → out", () => {
    expect(
      tier(
        block({
          canonical: {
            kind: "tool.permission",
            toolPermission: { requestId: "r1", toolName: "Bash" },
          },
        }),
      ),
    ).toBe("out");
  });

  // ② canonical.kind 命中写层。
  it("canonical.kind file.write → write", () => {
    expect(
      tier(
        block({
          canonical: {
            kind: "file.write",
            fileWrite: { path: "a.ts", content: "", lines: 0, bytes: 0 },
          },
        }),
      ),
    ).toBe("write");
  });

  it("canonical.kind file.edit → write", () => {
    expect(
      tier(
        block({ canonical: { kind: "file.edit", fileEdit: { files: [] } } }),
      ),
    ).toBe("write");
  });

  // ③ 无 canonical 时按 input shape 探测,与 summary.ts 同源。
  it("input.command present (no canonical) → write", () => {
    expect(tier(block({ toolInput: { command: "ls -la" } }))).toBe("write");
  });

  it("input.cmd present (no canonical) → write", () => {
    expect(tier(block({ toolInput: { cmd: "echo hi" } }))).toBe("write");
  });

  it("input.content present (no canonical) → write", () => {
    expect(tier(block({ toolInput: { content: "hello" } }))).toBe("write");
  });

  it("input.edits present (no canonical) → write", () => {
    expect(
      tier(block({ toolInput: { edits: [{ oldText: "a", newText: "b" }] } })),
    ).toBe("write");
  });

  it("input.old_string present (no canonical) → write", () => {
    expect(
      tier(block({ toolInput: { old_string: "a", new_string: "b" } })),
    ).toBe("write");
  });

  it("input.path present (no canonical) → read", () => {
    expect(tier(block({ toolInput: { path: "/tmp/a.ts" } }))).toBe("read");
  });

  // 定位语义字段与 summary.ts 的 PATH_KEYS 同源 —— claudecode 的 Read 用
  // file_path,只认 path 会把最常见的只读探查误判成中性层。
  it("input.file_path present (no canonical) → read", () => {
    expect(
      tier(
        block({ toolInput: { file_path: "/tmp/a.ts", limit: 20, offset: 0 } }),
      ),
    ).toBe("read");
  });

  it("input.file / input.filename present (no canonical) → read", () => {
    expect(tier(block({ toolInput: { file: "/tmp/a.ts" } }))).toBe("read");
    expect(tier(block({ toolInput: { filename: "a.ts" } }))).toBe("read");
  });

  // 写入语义字段先于定位字段命中:Write 的 {file_path, content} 仍是写层。
  it("write-shaped input wins over a co-present locator field", () => {
    expect(
      tier(block({ toolInput: { content: "x", file_path: "/tmp/a.ts" } })),
    ).toBe("write");
  });

  it("input.pattern present (no canonical) → read", () => {
    expect(tier(block({ toolInput: { pattern: "*.ts" } }))).toBe("read");
  });

  it("input.query present (no canonical) → read", () => {
    expect(tier(block({ toolInput: { query: "foo" } }))).toBe("read");
  });

  it("input.url present (no canonical) → read", () => {
    expect(tier(block({ toolInput: { url: "https://example.com" } }))).toBe(
      "read",
    );
  });

  // ④ 都不匹配落中性层 —— 认不出来的工具不假装它是只读(design decision 6)。
  it("unrecognized shape (no canonical, no known keys) → neutral, not read", () => {
    expect(tier(block({ toolInput: { foo: "bar" } }))).toBe("neutral");
  });

  it("no canonical and no toolInput at all → neutral", () => {
    expect(tier(block({}))).toBe("neutral");
  });

  it("empty toolInput object → neutral", () => {
    expect(tier(block({ toolInput: {} }))).toBe("neutral");
  });

  // 顺序:canonical.kind 优先于 input shape —— 即便 input 看起来像写操作,
  // 一个已知的出组 kind 也不应被 shape 探测反超。
  it("canonical.kind out-group wins over write-shaped input", () => {
    expect(
      tier(
        block({
          canonical: {
            kind: "tool.permission",
            toolPermission: { requestId: "r1", toolName: "Bash" },
          },
          toolInput: { command: "rm -rf /" },
        }),
      ),
    ).toBe("out");
  });
});

describe("displayName", () => {
  it('mcp__server__tool → "server · tool"', () => {
    expect(displayName("mcp__github__create_issue")).toBe(
      "github · create_issue",
    );
  });

  it("mcp__server__tool with underscores in tool name keeps them", () => {
    expect(displayName("mcp__playwright__browser_click")).toBe(
      "playwright · browser_click",
    );
  });

  it("plain tool name passes through unchanged", () => {
    expect(displayName("Bash")).toBe("Bash");
  });

  it("name that merely starts with mcp__ but has no third segment passes through unchanged", () => {
    expect(displayName("mcp__onlyserver")).toBe("mcp__onlyserver");
  });
});
