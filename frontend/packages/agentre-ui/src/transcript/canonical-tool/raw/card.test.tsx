import { render, screen, fireEvent, within } from "@testing-library/react";
import { describe, it, expect, vi } from "vitest";

import type { TranscriptBlock } from "../../dto";
import type { TranscriptPorts } from "../../ports";
import { TranscriptPortsProvider } from "../../ports-context";

import { RawToolCard } from "./card";

// 只有未决的 toolPermission 才挂 ToolPermissionOverlay,而 overlay 从
// TranscriptPortsProvider 取动作端口 —— 因此只有那一条用例需要注入端口。
const ports: TranscriptPorts = {
  answerToolPermission: vi.fn().mockResolvedValue(undefined),
  answerUserQuestion: vi.fn().mockResolvedValue(undefined),
  answerToolApproval: vi.fn().mockResolvedValue(undefined),
  resolveExecApproval: vi.fn().mockResolvedValue({ status: "resolved" }),
  resolvePlanAction: vi.fn().mockResolvedValue(undefined),
};

const bashUse = (overrides: Partial<TranscriptBlock> = {}): TranscriptBlock =>
  ({
    type: "tool_use",
    toolName: "Bash",
    toolInput: { command: "ls -la" },
    ...overrides,
  }) as unknown as TranscriptBlock;

const result = (overrides: Partial<TranscriptBlock> = {}): TranscriptBlock =>
  ({
    type: "tool_result",
    text: "hi\n",
    ...overrides,
  }) as unknown as TranscriptBlock;

describe("RawToolCard header", () => {
  it("shows the tool name and a one-line summary", () => {
    render(<RawToolCard toolBlock={bashUse()} />);
    expect(screen.getByText("Bash")).toBeDefined();
    expect(screen.getByText(/ls -la/)).toBeDefined();
  });

  it("relativizes file paths against cwd", () => {
    render(
      <RawToolCard
        toolBlock={bashUse({
          toolName: "Read",
          toolInput: { file_path: "/root/app/foo.ts" },
        })}
        cwd="/root/app"
      />,
    );
    expect(screen.getByText("./foo.ts")).toBeDefined();
  });

  it("uses 'Bash' label when input has a command field, regardless of toolName", () => {
    render(
      <RawToolCard
        toolBlock={bashUse({
          toolName: "shell",
          toolInput: { command: "uname" },
        })}
      />,
    );
    expect(screen.getByText("Bash")).toBeDefined();
  });
});

describe("RawToolCard status mark", () => {
  it("Given 等待返回, When 渲染, Then 卡头是转圈而不是 RUNNING 文字", () => {
    render(<RawToolCard toolBlock={bashUse()} />);
    const card = screen.getByTestId("raw-tool-card");
    expect(within(card).getByRole("img", { name: "Running" })).toHaveAttribute(
      "data-state",
      "running",
    );
    expect(screen.queryByText("RUNNING")).toBeNull();
  });

  it("Given 结果已回, When 渲染, Then 成功不挂标记也不写 DONE", () => {
    render(<RawToolCard toolBlock={bashUse()} resultBlock={result()} />);
    const card = screen.getByTestId("raw-tool-card");
    expect(within(card).queryByRole("img")).toBeNull();
    expect(screen.queryByText("DONE")).toBeNull();
  });

  it("Given resultBlock.isError, When 渲染, Then 卡头是红三角而不是 ERROR 文字", () => {
    render(
      <RawToolCard
        toolBlock={bashUse()}
        resultBlock={result({ isError: true, text: "boom" })}
      />,
    );
    const card = screen.getByTestId("raw-tool-card");
    expect(within(card).getByRole("img", { name: "Failed" })).toHaveAttribute(
      "data-state",
      "failed",
    );
    expect(screen.queryByText("ERROR")).toBeNull();
  });
});

describe("RawToolCard command_execution result parsing", () => {
  const cmdUse = bashUse({
    toolName: "command_execution",
    toolInput: { command: "echo ok" },
  });

  it("Given 退出码 0, When 渲染, Then 不挂标记也不写 EXIT 0, 输出照常解析", () => {
    render(
      <RawToolCard
        toolBlock={cmdUse}
        resultBlock={result({
          text: JSON.stringify({
            exitCode: 0,
            output: "ok\n",
            status: "success",
          }),
        })}
      />,
    );
    expect(screen.queryByText("EXIT 0")).toBeNull();
    expect(
      within(screen.getByTestId("raw-tool-card")).queryByRole("img"),
    ).toBeNull();
    fireEvent.click(screen.getByRole("button"));
    expect(screen.getByText(/^ok$/m)).toBeDefined();
    expect(screen.queryByText(/"exitCode"/)).toBeNull();
  });

  it("Given 非零退出码, When 渲染, Then 红三角后跟 exit N 且整卡为错误态", () => {
    render(
      <RawToolCard
        toolBlock={cmdUse}
        resultBlock={result({
          text: JSON.stringify({ exitCode: 1, output: "" }),
        })}
      />,
    );
    const card = screen.getByTestId("raw-tool-card");
    expect(within(card).getByRole("img", { name: "Failed" })).toBeDefined();
    expect(within(card).getByText("exit 1")).toBeDefined();
    expect(screen.queryByText("EXIT 1")).toBeNull();
    expect(screen.getByTestId("raw-tool-card").className).toMatch(
      /bg-destructive-soft/,
    );
  });

  it("flags failed/interrupted status as error", () => {
    render(
      <RawToolCard
        toolBlock={cmdUse}
        resultBlock={result({
          text: JSON.stringify({ output: "", status: "failed" }),
        })}
      />,
    );
    const card = screen.getByTestId("raw-tool-card");
    expect(within(card).getByRole("img", { name: "Failed" })).toBeDefined();
    expect(within(card).queryByText(/^exit /)).toBeNull();
  });
});

describe("RawToolCard expansion", () => {
  it("starts collapsed; params and result are hidden", () => {
    render(
      <RawToolCard
        toolBlock={bashUse({
          toolInput: { command: "echo hi", timeout: 5000 },
        })}
        resultBlock={result({ text: "hi\n" })}
      />,
    );
    expect(screen.queryByText("timeout")).toBeNull();
    expect(screen.queryByText(/^hi$/m)).toBeNull();
  });

  it("expanding reveals params (key=value entries) and result body", () => {
    render(
      <RawToolCard
        toolBlock={bashUse({
          toolInput: { command: "echo hi", timeout: 5000 },
        })}
        resultBlock={result({ text: "hi\n" })}
      />,
    );
    fireEvent.click(screen.getByRole("button"));
    expect(screen.getByText("timeout")).toBeDefined();
    expect(screen.getByText("5000")).toBeDefined();
    expect(screen.getByText(/^hi$/m)).toBeDefined();
  });

  it("renders an empty-params placeholder when input is empty", () => {
    render(
      <RawToolCard
        toolBlock={bashUse({ toolInput: {} as Record<string, unknown> })}
      />,
    );
    fireEvent.click(screen.getByRole("button"));
    expect(screen.getByText("No parameters")).toBeDefined();
  });

  it("Given an expanded card, When collapsed, Then the body stays mounted through the collapse transition and unmounts after it ends", () => {
    render(
      <RawToolCard
        toolBlock={bashUse({
          toolInput: { command: "echo hi", timeout: 5000 },
        })}
        resultBlock={result({ text: "hi\n" })}
      />,
    );
    const header = screen.getByRole("button", { expanded: false });
    fireEvent.click(header);
    expect(header).toHaveAttribute("aria-expanded", "true");
    const grid = header.nextElementSibling as HTMLElement;
    expect(within(grid).getByText("timeout")).toBeDefined();

    fireEvent.click(header);
    expect(header).toHaveAttribute("aria-expanded", "false");
    // 收缩动画期间内容仍挂载(高度过渡需要内容支撑),过渡结束才卸载。
    expect(within(grid).getByText("timeout")).toBeDefined();
    fireEvent.transitionEnd(grid);
    expect(within(grid).queryByText("timeout")).toBeNull();
  });
});

describe("RawToolCard long content", () => {
  it("Given a long param value, When the card is open, Then it scrolls without an expand control", () => {
    render(
      <RawToolCard
        toolBlock={bashUse({
          toolInput: {
            command: "echo hi",
            note: "line1\nline2\nline3\nline4\nline5\nline6",
          },
        })}
      />,
    );
    fireEvent.click(screen.getByRole("button")); // expand card
    expect(screen.queryByRole("button", { name: "Expand all" })).toBeNull();
    expect(screen.getByText(/line6/)).toHaveClass("max-h-64", "overflow-auto");
  });

  it("pretty-prints object param values as indented JSON", () => {
    render(
      <RawToolCard
        toolBlock={bashUse({
          toolInput: { command: "echo hi", extra: { a: 1, b: [1, 2] } },
        })}
      />,
    );
    fireEvent.click(screen.getByRole("button"));
    expect(screen.getByText(/"a": 1/)).toBeDefined();
    expect(screen.getByText(/"b": \[/)).toBeDefined();
  });

  it("Given a long result body, When the card is open, Then it scrolls without an expand control", () => {
    const longResult = Array.from({ length: 20 }, (_, i) => `line${i}`).join(
      "\n",
    );
    render(
      <RawToolCard
        toolBlock={bashUse({ toolInput: { command: "echo hi" } })}
        resultBlock={result({ text: longResult })}
      />,
    );
    fireEvent.click(screen.getByRole("button"));
    expect(screen.queryByRole("button", { name: "Expand all" })).toBeNull();
    expect(screen.getByText(/line19/)).toHaveClass("max-h-64", "overflow-auto");
  });
});

describe("RawToolCard background running pill", () => {
  const bashBlock = (extra: Record<string, unknown>) =>
    ({
      type: "tool_use",
      toolName: "Bash",
      toolUseId: "tu1",
      toolInput: { command: "sleep 5", run_in_background: true },
      ...extra,
    }) as never;

  it("shows 后台运行 + task_id pill when run_in_background", () => {
    render(
      <RawToolCard
        toolBlock={bashBlock({
          subagent: {
            kind: "local_bash",
            taskId: "b3875slp0",
            status: "running",
          },
        })}
        resultBlock={undefined}
        cwd="/tmp"
        sessionId={1}
      />,
    );
    expect(screen.getByText(/后台运行|Background/)).toBeDefined();
    expect(screen.getByText("b3875slp0")).toBeDefined();
  });

  it("does NOT show the pill for a normal foreground bash", () => {
    render(
      <RawToolCard
        toolBlock={
          {
            type: "tool_use",
            toolName: "Bash",
            toolUseId: "tu2",
            toolInput: { command: "ls" },
          } as never
        }
        resultBlock={undefined}
        cwd="/tmp"
        sessionId={1}
      />,
    );
    expect(screen.queryByText(/后台运行|Background/)).toBeNull();
  });

  it("hides the 后台运行 pill once the background task has completed", () => {
    render(
      <RawToolCard
        toolBlock={bashBlock({
          subagent: {
            kind: "local_bash",
            taskId: "b3875slp0",
            status: "completed",
          },
        })}
        resultBlock={undefined}
        cwd="/tmp"
        sessionId={1}
      />,
    );
    expect(screen.queryByText(/后台运行|Background/)).toBeNull();
  });
});

describe("RawToolCard permission integration", () => {
  it("shows the unresolved permission overlay", () => {
    render(
      <TranscriptPortsProvider ports={ports}>
        <RawToolCard
          toolBlock={bashUse({
            toolInput: { command: "rm -rf /" },
            toolPermission: {
              requestId: "req-1",
              toolName: "Bash",
              toolInput: {},
              resolved: false,
            },
          })}
          sessionId={42}
        />
      </TranscriptPortsProvider>,
    );
    expect(screen.getByTestId("tool-permission-overlay")).toBeDefined();
  });

  it("Given 审批已允许, When 渲染, Then 徽标是盾牌图标, 没有英文 Allowed 文字", () => {
    render(
      <RawToolCard
        toolBlock={bashUse({
          toolPermission: {
            requestId: "r1",
            toolName: "Bash",
            toolInput: {},
            resolved: true,
            allowed: true,
          },
        })}
      />,
    );
    const badge = screen.getByTitle("This tool call has been approved");
    expect(within(badge).getByRole("img", { name: "Allowed" })).toHaveAttribute(
      "data-state",
      "allowed",
    );
    expect(screen.queryByText("Allowed")).toBeNull();
    expect(within(badge).queryByText("this session")).toBeNull();
    // 徽标换成图标本身，不再是一枚绿底胶囊。
    expect(badge).not.toHaveClass("bg-status-running-bg");
    expect(badge).not.toHaveClass("rounded");
  });

  it("Given 本会话始终允许, When 渲染, Then 盾牌后跟灰字「this session」", () => {
    render(
      <RawToolCard
        toolBlock={bashUse({
          toolPermission: {
            requestId: "r1",
            toolName: "Bash",
            toolInput: {},
            resolved: true,
            allowed: true,
            alwaysAllow: true,
          },
        })}
      />,
    );
    const badge = screen.getByTitle("This tool call has been approved");
    expect(within(badge).getByRole("img", { name: "Allowed" })).toBeDefined();
    expect(within(badge).getByText("this session")).toBeDefined();
    expect(screen.queryByText("Allowed · session")).toBeNull();
  });
});
