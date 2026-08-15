import { render, screen, fireEvent, within } from "@testing-library/react";
import { describe, it, expect, vi } from "vitest";

import { TranscriptPortsProvider } from "@agentre-ai/agentre-ui";
import type { TranscriptPorts } from "@agentre-ai/agentre-ui";

import { RawToolCard } from "./card";
import type { ChatBlockData } from "@/stores/chat-streams-store";

// 只有未决的 toolPermission 才挂 ToolPermissionOverlay,而 overlay 从
// TranscriptPortsProvider 取动作端口 —— 因此只有那一条用例需要注入端口。
const ports: TranscriptPorts = {
  answerToolPermission: vi.fn().mockResolvedValue(undefined),
  answerUserQuestion: vi.fn().mockResolvedValue(undefined),
  answerToolApproval: vi.fn().mockResolvedValue(undefined),
  resolveExecApproval: vi.fn().mockResolvedValue({ status: "resolved" }),
  resolvePlanAction: vi.fn().mockResolvedValue(undefined),
};

const bashUse = (overrides: Partial<ChatBlockData> = {}): ChatBlockData =>
  ({
    type: "tool_use",
    toolName: "Bash",
    toolInput: { command: "ls -la" },
    ...overrides,
  }) as unknown as ChatBlockData;

const result = (overrides: Partial<ChatBlockData> = {}): ChatBlockData =>
  ({
    type: "tool_result",
    text: "hi\n",
    ...overrides,
  }) as unknown as ChatBlockData;

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

describe("RawToolCard status pill", () => {
  it("shows RUNNING while waiting for a result", () => {
    render(<RawToolCard toolBlock={bashUse()} />);
    expect(screen.getByText("RUNNING")).toBeDefined();
  });

  it("shows DONE once a result arrives", () => {
    render(<RawToolCard toolBlock={bashUse()} resultBlock={result()} />);
    expect(screen.getByText("DONE")).toBeDefined();
  });

  it("shows ERROR when resultBlock.isError", () => {
    render(
      <RawToolCard
        toolBlock={bashUse({
          toolName: "Read",
          toolInput: { file_path: "/missing" },
        })}
        resultBlock={result({ text: "ENOENT", isError: true })}
      />,
    );
    expect(screen.getByText("ERROR")).toBeDefined();
  });
});

describe("RawToolCard command_execution result parsing", () => {
  const cmdUse = bashUse({
    toolName: "command_execution",
    toolInput: { command: "echo ok" },
  });

  it("shows EXIT N pill and parsed output (not raw JSON)", () => {
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
    expect(screen.getByText("EXIT 0")).toBeDefined();
    fireEvent.click(screen.getByRole("button"));
    expect(screen.getByText(/^ok$/m)).toBeDefined();
    expect(screen.queryByText(/"exitCode"/)).toBeNull();
  });

  it("flags non-zero exit as error", () => {
    render(
      <RawToolCard
        toolBlock={cmdUse}
        resultBlock={result({
          text: JSON.stringify({ exitCode: 1, output: "" }),
        })}
      />,
    );
    expect(screen.getByText("EXIT 1")).toBeDefined();
    expect(screen.getByTestId("raw-tool-card").className).toMatch(
      /border-status-error/,
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
    expect(screen.getByText("ERROR")).toBeDefined();
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

  it("shows an Allowed badge when toolPermission resolved+allowed", () => {
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
    expect(screen.getByText("Allowed")).toBeDefined();
  });

  it("shows 'Allowed · session' when alwaysAllow is set", () => {
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
    expect(screen.getByText("Allowed · session")).toBeDefined();
  });
});
