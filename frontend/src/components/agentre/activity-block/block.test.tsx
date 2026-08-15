import { fireEvent, render, screen, within } from "@testing-library/react";
import { describe, expect, it } from "vitest";

import type { ActivityStep, ActivitySummary } from "../transcript-rows";
import {
  TranscriptUIStateProvider,
  type TranscriptBlock,
} from "@agentre-ai/agentre-ui";

import { ActivityBlock } from "./block";

// 活动块组件测试。组头汇总 / 三档字重 / 行内就地展开体 / 失败行 / 运行态自动展开
// 都在这里钉住 —— 判据本身(tier / toolCategory / summary)由上游纯函数负责,
// 这里只验「渲染成了什么、点开看得到什么、折叠时没 mount 什么」。

function toolStep(
  key: string,
  toolBlock: Partial<TranscriptBlock>,
  resultBlock?: Partial<TranscriptBlock>,
): ActivityStep {
  return {
    resultBlock: resultBlock
      ? ({ type: "tool_result", ...resultBlock } as TranscriptBlock)
      : undefined,
    toolBlock: { type: "tool_use", ...toolBlock } as TranscriptBlock,
    type: "tool",
    uiStateKey: key,
  };
}

function thinkingStep(key: string, text: string): ActivityStep {
  return {
    block: { text, type: "thinking" } as TranscriptBlock,
    streaming: false,
    type: "thinking",
    uiStateKey: key,
  };
}

// 读层的判据是 input shape(tier() 的 path / pattern / query / url),不是工具名。
const readStep = toolStep(
  "message:1:tool:tool:read-1",
  { toolInput: { path: "/repo/chat.go" }, toolName: "Read" },
  { text: "package chat\nfunc A() {}\nfunc B() {}" },
);

const commandStep = toolStep(
  "message:1:tool:tool:bash-1",
  { toolInput: { command: "pnpm test" }, toolName: "Bash" },
  { text: '{"exitCode":0,"output":"13 passed"}' },
);

const mcpStep = toolStep(
  "message:1:tool:tool:mcp-1",
  {
    toolInput: { commands: [{ op: "set_node" }] },
    toolName: "mcp__pencil__execute",
  },
  { text: '{"ok":true}' },
);

const failedStep = toolStep(
  "message:1:tool:tool:bash-2",
  { toolInput: { command: "pnpm lint" }, toolName: "Bash" },
  { isError: true, text: '{"exitCode":1,"output":"boom"}' },
);

// 命令类结果的失败信号在结果 JSON 里(exitCode / status),不在 isError 上:
// codex 只有 item 自身失败才置 isError,一条退出码非零的命令 isError 是 false
// (pkg/codex/types.go toolResponseForItem 把 exitCode/status 放进 response,
// translator 的 IsError 只跟 item.Err 走)。
const exitOnlyFailedStep = toolStep(
  "message:1:tool:tool:bash-3",
  { toolInput: { command: "pnpm exec tsc -b" }, toolName: "Bash" },
  { text: '{"exitCode":2,"output":"2 errors","status":"completed"}' },
);

// subagent sidecar 是 wails 生成的 class 类型(带 convertValues),测试只用其中
// 两个字段,按纯数据对象构造后 cast —— 与 store 里同一手法。
function subagentState(status: string, taskId: string) {
  return { status, taskId } as unknown as TranscriptBlock["subagent"];
}

// manySteps —— 一轮长活动:每 5 步夹一条失败的命令,其余是只读探查。
// 24 步时被省略的前 18 步里正好有 3 条失败,足以钉住「省略行仍报红计数」。
function manySteps(n: number): ActivityStep[] {
  return Array.from({ length: n }, (_, i) =>
    i % 5 === 4
      ? toolStep(
          `message:1:tool:tool:many-${i}`,
          { toolInput: { command: `pnpm run step-${i}` }, toolName: "Bash" },
          { isError: true, text: '{"exitCode":1,"output":"boom"}' },
        )
      : toolStep(
          `message:1:tool:tool:many-${i}`,
          { toolInput: { path: `/repo/f-${i}.go` }, toolName: "Read" },
          { text: `line ${i}` },
        ),
  );
}

function summaryOf(partial: Partial<ActivitySummary> = {}): ActivitySummary {
  return {
    failures: 0,
    parts: [],
    steps: 0,
    truncated: false,
    ...partial,
  };
}

function renderBlock(props: Partial<Parameters<typeof ActivityBlock>[0]> = {}) {
  const steps = props.steps ?? [readStep, commandStep];
  return render(
    <TranscriptUIStateProvider>
      <ActivityBlock
        steps={steps}
        summary={props.summary ?? summaryOf({ steps: steps.length })}
        uiStateKey={props.uiStateKey ?? "message:1:activity:tool:read-1"}
        {...props}
      />
    </TranscriptUIStateProvider>,
  );
}

describe("ActivityBlock 组头(折叠态)", () => {
  it("Given 一组已落定的活动, When 折叠渲染, Then 组头报出步数、固定顺序的汇总与红色失败计数", () => {
    renderBlock({
      steps: [
        thinkingStep("k-think", "hmm"),
        readStep,
        commandStep,
        failedStep,
      ],
      summary: summaryOf({
        failures: 1,
        parts: [
          { category: "thinking", count: 2 },
          { category: "read", count: 8 },
          { category: "edit", count: 1, files: 1, minus: 4, plus: 18 },
          { category: "command", count: 1 },
        ],
        steps: 10,
        truncated: true,
      }),
    });

    const header = screen.getByTestId("activity-header");
    expect(header.tagName).toBe("BUTTON");
    expect(header).toHaveAttribute("aria-expanded", "false");
    expect(within(header).getByText("10 steps")).toBeInTheDocument();
    expect(within(header).getByText(/Thinking 2/)).toBeInTheDocument();
    expect(within(header).getByText(/Read 8/)).toBeInTheDocument();
    // 写操作报对象规模:改了几个文件 + 增删行,而不是只报一个步数。
    expect(within(header).getByText(/1 file/)).toBeInTheDocument();
    expect(within(header).getByText("+18")).toBeInTheDocument();
    expect(within(header).getByText("−4")).toBeInTheDocument();
    // 截断省略号在,失败计数在省略号之后、且用 status-error 着色。
    expect(within(header).getByText("…")).toBeInTheDocument();
    const failures = within(header).getByTestId("activity-failures");
    expect(failures).toHaveTextContent("1 failed");
    expect(failures.className).toContain("text-status-error");
    // 失败计数不在会被裁掉的汇总段里 —— 组头挤不下时先裁类目,红标永远在。
    expect(screen.getByTestId("activity-summary").contains(failures)).toBe(
      false,
    );
  });

  it("Given 折叠态, Then 组内步骤的结果文本不进 DOM(行级虚拟化/懒挂载不回归)", () => {
    renderBlock({ steps: [readStep, commandStep] });

    expect(screen.queryByTestId("activity-row")).toBeNull();
    expect(screen.queryByText(/func A/)).toBeNull();
    expect(screen.queryByText(/13 passed/)).toBeNull();
  });

  // 单条不成组:一段活动只有一步时不套「1 步」的壳,直接渲染那一行活动行。
  it("Given 一段活动只有一步, Then 不出组头,直接就是那一行活动行", () => {
    renderBlock({ steps: [readStep], summary: summaryOf({ steps: 1 }) });

    expect(screen.queryByTestId("activity-header")).toBeNull();
    const rows = screen.getAllByTestId("activity-row");
    expect(rows).toHaveLength(1);
    expect(within(rows[0]).getByTestId("activity-name")).toHaveTextContent(
      "Read",
    );
    // 那一行仍是就地可展开的:折叠不等于信息丢失。
    expect(rows[0]).toHaveAttribute("aria-expanded", "false");
    fireEvent.click(rows[0]);
    const body = screen.getByTestId("activity-row-body");
    expect(within(body).getByText("path")).toBeInTheDocument();
    expect(within(body).getByText(/func A/)).toBeInTheDocument();
  });

  // 单条不成组的那一行上面没有组头、左边没有时间轴竖线 —— 组内那套「箭头 hover
  // 才显形」在这里没有任何东西替它说明「这一行能点开」,于是它必须自己显形。
  it("Given 一段活动只有一步, Then 那一行的展开箭头常显", () => {
    renderBlock({ steps: [readStep], summary: summaryOf({ steps: 1 }) });

    const chevron = within(screen.getByTestId("activity-row")).getByTestId(
      "activity-chevron",
    );
    expect(chevron.getAttribute("class")).not.toContain("opacity-0");
  });

  it("Given 组内的活动行, Then 展开箭头仍是 hover / 聚焦才显形(组头已经说明了可展开)", () => {
    renderBlock({ steps: [readStep, commandStep] });
    fireEvent.click(screen.getByTestId("activity-header"));

    const rows = screen.getAllByTestId("activity-row");
    expect(
      within(rows[0]).getByTestId("activity-chevron").getAttribute("class"),
    ).toContain("opacity-0");
  });

  it("Given 单步且这一组正在跑, Then 仍是那一行活动行(没有壳可展开)", () => {
    renderBlock({
      running: true,
      steps: [readStep],
      summary: summaryOf({ steps: 1 }),
    });

    expect(screen.queryByTestId("activity-header")).toBeNull();
    expect(screen.getAllByTestId("activity-row")).toHaveLength(1);
  });

  it("Given 组头, When 点开再点收, Then aria-expanded 跟随并展开/收起时间轴", () => {
    renderBlock({ steps: [readStep, commandStep] });
    const header = screen.getByTestId("activity-header");

    fireEvent.click(header);
    expect(header).toHaveAttribute("aria-expanded", "true");
    expect(screen.getAllByTestId("activity-row")).toHaveLength(2);

    fireEvent.click(header);
    expect(header).toHaveAttribute("aria-expanded", "false");
    // 收缩动画需要内容保持挂载(grid 过渡才有高度可收),过渡结束后才卸载,
    // 保住折叠态零挂载的性能约定。
    expect(screen.getAllByTestId("activity-row")).toHaveLength(2);
    fireEvent.transitionEnd(header.nextElementSibling as HTMLElement);
    expect(screen.queryByTestId("activity-row")).toBeNull();
  });
});

describe("ActivityBlock 活动行(展开态)", () => {
  it("Given 展开的时间轴, Then 读 / 中性 / 写三档以字重与前景色区分,且 MCP 名字拆成 server · tool", () => {
    renderBlock({ steps: [readStep, mcpStep, commandStep] });
    fireEvent.click(screen.getByTestId("activity-header"));

    const rows = screen.getAllByTestId("activity-row");
    expect(rows).toHaveLength(3);
    expect(rows[0]).toHaveAttribute("data-weight", "read");
    expect(rows[1]).toHaveAttribute("data-weight", "neutral");
    expect(rows[2]).toHaveAttribute("data-weight", "write");

    const name = (row: HTMLElement) => within(row).getByTestId("activity-name");
    expect(name(rows[0]).className).toContain("text-muted-foreground");
    expect(name(rows[1]).className).toContain("font-medium");
    expect(name(rows[2]).className).toContain("font-semibold");
    expect(name(rows[1])).toHaveTextContent("pencil · execute");
  });

  it("Given 一行活动, When 点开, Then 就地出现参数与结果两段", () => {
    renderBlock({ steps: [readStep, commandStep] });
    fireEvent.click(screen.getByTestId("activity-header"));

    const row = screen.getAllByTestId("activity-row")[0];
    expect(row).toHaveAttribute("aria-expanded", "false");
    fireEvent.click(row);

    expect(row).toHaveAttribute("aria-expanded", "true");
    const body = screen.getByTestId("activity-row-body");
    expect(within(body).getByText("path")).toBeInTheDocument();
    expect(within(body).getByText("/repo/chat.go")).toBeInTheDocument();
    expect(within(body).getByText(/func A/)).toBeInTheDocument();
  });

  it("Given 一步 file.edit, When 点开, Then 展开体是既有 diff 渲染而不是参数 JSON", () => {
    const edit = toolStep(
      "message:1:tool:tool:edit-1",
      {
        canonical: {
          fileEdit: {
            files: [
              {
                hunks: [
                  {
                    lines: [
                      { new: 1, op: "+", text: "const next = 1;" },
                      { old: 1, op: "-", text: "const prev = 0;" },
                    ],
                    newLines: 1,
                    newStart: 1,
                    oldLines: 1,
                    oldStart: 1,
                  },
                ],
                kind: "modified",
                minus: 1,
                path: "/repo/a.ts",
                plus: 1,
              },
            ],
          },
          kind: "file.edit",
        } as unknown as TranscriptBlock["canonical"],
        toolInput: { new_string: "const next = 1;", old_string: "x" },
        toolName: "Edit",
      },
      { text: "ok" },
    );
    renderBlock({ steps: [edit, readStep] });
    fireEvent.click(screen.getByTestId("activity-header"));
    fireEvent.click(screen.getAllByTestId("activity-row")[0]);

    const body = screen.getByTestId("activity-row-body");
    expect(within(body).getByTestId("file-edit-diff-scroll")).toBeDefined();
    expect(within(body).getByText("const next = 1;")).toBeInTheDocument();
  });

  it("Given 一步 file.write, When 点开, Then 展开体是文件内容", () => {
    const write = toolStep(
      "message:1:tool:tool:write-1",
      {
        canonical: {
          fileWrite: {
            bytes: 24,
            content: "line one\nline two",
            lines: 2,
            path: "/repo/new.ts",
          },
          kind: "file.write",
        } as unknown as TranscriptBlock["canonical"],
        toolInput: { content: "line one\nline two", file_path: "/repo/new.ts" },
        toolName: "Write",
      },
      { text: "ok" },
    );
    renderBlock({ steps: [write, readStep] });
    fireEvent.click(screen.getByTestId("activity-header"));
    fireEvent.click(screen.getAllByTestId("activity-row")[0]);

    const writeBody = within(
      screen.getByTestId("activity-row-body"),
    ).getByTestId("activity-file-write");
    // 「既有的文件内容渲染」= FileWriteCard 今天的带行号内容区,不是一段裸代码块。
    const content = within(writeBody).getByTestId("file-write-content-scroll");
    expect(content).toHaveTextContent("line two");
    expect(within(content).getByText("2")).toBeInTheDocument();
  });

  it("Given 一步 file.write 内容被截断, When 点开, Then 截断条与「复制完整内容」都在", () => {
    const write = toolStep(
      "message:1:tool:tool:write-2",
      {
        canonical: {
          fileWrite: {
            bytes: 999_999,
            content: "kept one\nkept two",
            lines: 4200,
            path: "/repo/big.ts",
            truncated: true,
          },
          kind: "file.write",
        } as unknown as TranscriptBlock["canonical"],
        toolInput: { content: "kept one\nkept two", file_path: "/repo/big.ts" },
        toolName: "Write",
      },
      { text: "ok" },
    );
    renderBlock({ steps: [write, readStep] });
    fireEvent.click(screen.getByTestId("activity-header"));
    fireEvent.click(screen.getAllByTestId("activity-row")[0]);

    // 折叠前看得到的东西展开后必须一样看得到:少了截断条,用户不知道自己
    // 读到的是被砍过的内容,也拿不到完整原文。
    const writeBody = within(
      screen.getByTestId("activity-row-body"),
    ).getByTestId("activity-file-write");
    expect(within(writeBody).getByText(/4200/)).toBeInTheDocument();
    expect(
      within(writeBody).getByRole("button", { name: /Copy full content/i }),
    ).toBeInTheDocument();
  });

  it("Given 一步失败, Then 该行红色并带 exit N, 但默认不展开", () => {
    renderBlock({ steps: [readStep, failedStep] });
    fireEvent.click(screen.getByTestId("activity-header"));

    const failed = screen.getAllByTestId("activity-row")[1];
    expect(failed).toHaveAttribute("aria-expanded", "false");
    expect(failed).toHaveAttribute("data-failed", "true");
    expect(within(failed).getByTestId("activity-name").className).toContain(
      "text-status-error",
    );
    expect(within(failed).getByText("exit 1")).toBeInTheDocument();
    expect(screen.queryByText("boom")).toBeNull();
  });

  // 「没有标记 = 成功」的前提是所有没成功的步骤都带标记。命令失败的判据一直是
  // RawToolCard 的那一条(退出码非零 / status 是 failed|error|interrupted),
  // 只看 isError 会让一条 tsc 报错的命令与成功的一步完全同形。
  it("Given 一步命令以非零退出码结束、结果却没标 isError, Then 该行仍按失败呈现", () => {
    renderBlock({ steps: [readStep, exitOnlyFailedStep] });
    fireEvent.click(screen.getByTestId("activity-header"));

    const row = screen.getAllByTestId("activity-row")[1];
    expect(row).toHaveAttribute("data-failed", "true");
    expect(within(row).getByText("exit 2").className).toContain(
      "text-status-error",
    );
  });

  // codex 的每一次文件改动都是 file_change 工具,input 形如
  // {changes:[{path,kind,diff}]} —— 没有 path/file_path 键,summarizeRawTool 会
  // 落到「首个键=JSON」的兜底,把整段 unified diff 塞进折叠行的行首摘要。
  // 那既丢了路径(旧 FileEditCard 卡头一直显示它),又把一段无上限的文本挂进
  // 一个 whitespace-nowrap 的行盒里。
  it("Given 一步是 codex 的 file_change, Then 行摘要是文件路径而不是整段 diff", () => {
    const diff = `@@ -1,2 +1,2 @@\n-${"old ".repeat(20_000)}\n+${"new ".repeat(20_000)}`;
    const codexEdit = toolStep("message:1:tool:tool:fc-1", {
      canonical: {
        fileEdit: {
          files: [
            {
              hunks: [],
              kind: "modified",
              minus: 1,
              path: "/repo/internal/app/chat.go",
              plus: 1,
            },
          ],
        },
        kind: "file.edit",
      } as unknown as TranscriptBlock["canonical"],
      toolInput: {
        changes: [{ diff, kind: "update", path: "/repo/internal/app/chat.go" }],
      },
      toolName: "file_change",
    });
    renderBlock({ cwd: "/repo", steps: [readStep, codexEdit] });
    fireEvent.click(screen.getByTestId("activity-header"));

    const row = screen.getAllByTestId("activity-row")[1];
    expect(row).toHaveTextContent("./internal/app/chat.go");
    expect(row.textContent ?? "").not.toContain(diff);
    expect((row.textContent ?? "").length).toBeLessThan(400);
  });

  // 行尾预览是折叠态就在 DOM 里的那段文字。结果原文没有大小上限(单行 JSON /
  // base64 / 压缩过的一行输出动辄几 MB),整段塞进一个 whitespace-nowrap 的行尾
  // 就等于让浏览器为一个折叠着的行去量一条几 MB 宽的行盒 —— 正是「折叠态不 mount
  // 结果文本」要挡掉的开销。
  it("Given 一步的结果是超长单行, Then 行尾预览有上限,整段结果不进折叠行的 DOM", () => {
    const huge = `head ${"x".repeat(50_000)} tail`;
    renderBlock({
      steps: [
        readStep,
        toolStep(
          "message:1:tool:tool:huge",
          { toolInput: { command: "jq -c . big.json" }, toolName: "Bash" },
          { text: huge },
        ),
      ],
    });
    fireEvent.click(screen.getByTestId("activity-header"));

    const row = screen.getAllByTestId("activity-row")[1];
    expect(row.textContent ?? "").not.toContain(huge);
    expect((row.textContent ?? "").length).toBeLessThan(400);
    // 截断的是预览,不是结果:点开仍然拿得到完整原文。
    fireEvent.click(row);
    expect(screen.getByTestId("activity-row-body").textContent).toContain(huge);
  });

  it("Given 一步思考, When 点开, Then 展开体是思考正文", () => {
    renderBlock({
      steps: [thinkingStep("k-think", "check the store"), readStep],
    });
    fireEvent.click(screen.getByTestId("activity-header"));

    const row = screen.getAllByTestId("activity-row")[0];
    expect(screen.queryByText("check the store")).toBeNull();
    fireEvent.click(row);
    expect(screen.getByText("check the store")).toBeInTheDocument();
  });
});

// 「没有标记 = 成功」只有在「还没成功的那些步」都带标记时才成立(spec 决策 10:
// 只有运行中 / 失败 / 待审批有标记)。没有结果的一步、以及结果只是启动 ACK 的
// 后台命令,都还没有成功 —— 它们不带标记就是把发生过的事藏了。
describe("ActivityBlock 未落定的一步", () => {
  const pendingStep = toolStep("message:1:tool:tool:bash-3", {
    toolInput: { command: "go build ./..." },
    toolName: "Bash",
  });

  const backgroundStep = toolStep(
    "message:1:tool:tool:bash-4",
    {
      subagent: subagentState("running", "bg_1"),
      toolInput: { command: "pnpm dev", run_in_background: true },
      toolName: "Bash",
    },
    { text: "Command running in background with ID: bg_1" },
  );

  it("Given 一步还没有结果, Then 该行带运行中标记而不是与成功步同形", () => {
    renderBlock({ steps: [readStep, pendingStep] });
    fireEvent.click(screen.getByTestId("activity-header"));

    const rows = screen.getAllByTestId("activity-row");
    expect(within(rows[1]).getByTestId("activity-pending")).toHaveTextContent(
      "running",
    );
    // 落定的那一步仍然什么标记都没有。
    expect(within(rows[0]).queryByTestId("activity-pending")).toBeNull();
  });

  it("Given 一步是仍在后台跑的命令, Then 该行报后台运行与任务 id", () => {
    renderBlock({ steps: [readStep, backgroundStep] });
    fireEvent.click(screen.getByTestId("activity-header"));

    const marker = within(screen.getAllByTestId("activity-row")[1]).getByTestId(
      "activity-pending",
    );
    expect(marker).toHaveTextContent("Background");
    expect(marker).toHaveTextContent("bg_1");
  });

  it("Given 后台命令连启动 ACK 都还没回, Then 只报一个标记(不叠运行中 + 后台)", () => {
    renderBlock({
      steps: [
        readStep,
        toolStep("message:1:tool:tool:bash-6", {
          subagent: subagentState("running", "bg_3"),
          toolInput: { command: "pnpm dev", run_in_background: true },
          toolName: "Bash",
        }),
      ],
    });
    fireEvent.click(screen.getByTestId("activity-header"));

    // getByTestId 本身就会在出现第二个标记时报错。
    const row = screen.getAllByTestId("activity-row")[1];
    expect(within(row).getByTestId("activity-pending")).toHaveTextContent(
      "running",
    );
  });

  it("Given 后台任务已经结束, Then 该行不再报后台运行", () => {
    renderBlock({
      steps: [
        readStep,
        toolStep(
          "message:1:tool:tool:bash-5",
          {
            subagent: subagentState("completed", "bg_2"),
            toolInput: { command: "pnpm build", run_in_background: true },
            toolName: "Bash",
          },
          { text: "Command running in background with ID: bg_2" },
        ),
      ],
    });
    fireEvent.click(screen.getByTestId("activity-header"));

    expect(
      within(screen.getAllByTestId("activity-row")[1]).queryByTestId(
        "activity-pending",
      ),
    ).toBeNull();
  });

  it("Given 调用方声明这一轮已终结, Then 没配到结果的一步报结果未知且不再转圈", () => {
    const { container } = renderBlock({
      pendingOutcome: "unknown",
      steps: [readStep, pendingStep],
    });
    fireEvent.click(screen.getByTestId("activity-header"));

    expect(
      within(screen.getAllByTestId("activity-row")[1]).getByTestId(
        "activity-pending",
      ),
    ).toHaveTextContent("unknown result");
    expect(container.querySelector(".animate-spin")).toBeNull();
  });

  it("Given 这一轮以失败终结, Then 没配到结果的一步按失败行呈现", () => {
    renderBlock({ pendingOutcome: "failed", steps: [readStep, pendingStep] });
    fireEvent.click(screen.getByTestId("activity-header"));

    const row = screen.getAllByTestId("activity-row")[1];
    expect(row).toHaveAttribute("data-failed", "true");
    expect(within(row).getByTestId("activity-name").className).toContain(
      "text-status-error",
    );
  });

  it("Given 单条不成组的一步还在跑, Then 那一行同样带运行中标记", () => {
    renderBlock({ steps: [pendingStep], summary: summaryOf({ steps: 1 }) });

    expect(screen.getByTestId("activity-pending")).toHaveTextContent("running");
  });
});

describe("ActivityBlock 运行态", () => {
  it("Given 这一组正在跑, Then 自动展开并在组头播报当前这一步", () => {
    renderBlock({
      running: true,
      steps: [
        readStep,
        toolStep("k-live", {
          toolInput: { path: "/repo/live.ts" },
          toolName: "Read",
        }),
      ],
    });

    const header = screen.getByTestId("activity-header");
    expect(header).toHaveAttribute("aria-expanded", "true");
    expect(screen.getAllByTestId("activity-row")).toHaveLength(2);
    const tail = screen.getByTestId("activity-live-tail");
    expect(tail).toHaveAttribute("aria-live", "polite");
    expect(tail).toHaveTextContent("/repo/live.ts");
    // 运行中不报耗时(轮次没结束,数字还没有意义)。
    expect(screen.queryByTestId("activity-duration")).toBeNull();
  });

  it("Given 调用方给了默认展开(子代理内部按步数阈值), Then 落定态也展开,用户仍可收起", () => {
    renderBlock({ defaultExpanded: true });

    const header = screen.getByTestId("activity-header");
    expect(header).toHaveAttribute("aria-expanded", "true");
    fireEvent.click(header);
    expect(header).toHaveAttribute("aria-expanded", "false");
  });

  it("Given 轮次结束, Then 自动收起", () => {
    const { rerender } = renderBlock({ running: true });
    expect(screen.getByTestId("activity-header")).toHaveAttribute(
      "aria-expanded",
      "true",
    );

    rerender(
      <TranscriptUIStateProvider>
        <ActivityBlock
          steps={[readStep, commandStep]}
          summary={summaryOf({ steps: 2 })}
          uiStateKey="message:1:activity:tool:read-1"
          durationMs={6600}
        />
      </TranscriptUIStateProvider>,
    );

    expect(screen.getByTestId("activity-header")).toHaveAttribute(
      "aria-expanded",
      "false",
    );
    expect(screen.getByTestId("activity-duration")).toHaveTextContent("6.6s");
  });

  // 运行中的块自动展开,于是一轮跑到几十步时它就是一堵不断长高的墙,新的一步
  // 把正文越推越远。省略只在**运行态**生效:落定后整块本来就会自动收起,用户
  // 主动点开时要的是全貌。
  describe("步数过多时省略前面几步", () => {
    it("Given 运行中且 24 步, Then 只渲染最后 6 行 + 一行省略行(报被省略部分的步数、汇总与红失败数)", () => {
      renderBlock({ running: true, steps: manySteps(24) });

      expect(screen.getAllByTestId("activity-row")).toHaveLength(6);
      const elided = screen.getByTestId("activity-elided");
      expect(elided.tagName).toBe("BUTTON");
      expect(within(elided).getByText("18 earlier steps")).toBeInTheDocument();
      // 汇总走组头那套类目 —— 被省略的 18 步里 15 次查阅、3 条命令。
      expect(within(elided).getByText(/Read 15/)).toBeInTheDocument();
      expect(within(elided).getByText(/Commands 3/)).toBeInTheDocument();
      // 失败计数照常显红:省略是收起,不是让发生过的事消失。
      const failures = within(elided).getByTestId("activity-failures");
      expect(failures).toHaveTextContent("3 failed");
      expect(failures.className).toContain("text-status-error");
      // 被省略的步骤一个都不 mount(与折叠态同一条性能约定)。
      expect(screen.queryByText("/repo/f-0.go")).toBeNull();
      expect(screen.getByText("/repo/f-23.go")).toBeInTheDocument();
    });

    it("Given 用户点省略行, Then 全部 24 行都在,省略行消失", () => {
      renderBlock({ running: true, steps: manySteps(24) });

      fireEvent.click(screen.getByTestId("activity-elided"));

      expect(screen.getAllByTestId("activity-row")).toHaveLength(24);
      expect(screen.queryByTestId("activity-elided")).toBeNull();
      expect(screen.getByText("/repo/f-0.go")).toBeInTheDocument();
    });

    it("Given 运行中但只有 8 步(不过阈值), Then 全部渲染,没有省略行", () => {
      renderBlock({ running: true, steps: manySteps(8) });

      expect(screen.getAllByTestId("activity-row")).toHaveLength(8);
      expect(screen.queryByTestId("activity-elided")).toBeNull();
    });

    it("Given 轮次已落定的 24 步, When 用户点开组头, Then 24 行全在(省略只在运行态)", () => {
      renderBlock({ steps: manySteps(24) });

      fireEvent.click(screen.getByTestId("activity-header"));

      expect(screen.getAllByTestId("activity-row")).toHaveLength(24);
      expect(screen.queryByTestId("activity-elided")).toBeNull();
    });
  });

  it("Given 用户在运行中手动收起, Then 轮次结束后仍按用户的选择", () => {
    const { rerender } = renderBlock({ running: true });
    fireEvent.click(screen.getByTestId("activity-header"));
    expect(screen.getByTestId("activity-header")).toHaveAttribute(
      "aria-expanded",
      "false",
    );

    rerender(
      <TranscriptUIStateProvider>
        <ActivityBlock
          steps={[readStep, commandStep]}
          summary={summaryOf({ steps: 2 })}
          uiStateKey="message:1:activity:tool:read-1"
        />
      </TranscriptUIStateProvider>,
    );
    expect(screen.getByTestId("activity-header")).toHaveAttribute(
      "aria-expanded",
      "false",
    );

    // 反向:落定后用户点开,保持展开(自动收起只作用于没被碰过的块)。
    fireEvent.click(screen.getByTestId("activity-header"));
    expect(screen.getByTestId("activity-header")).toHaveAttribute(
      "aria-expanded",
      "true",
    );
  });
});
