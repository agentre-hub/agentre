import { fireEvent, render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, it, expect } from "vitest";

import type { TranscriptBlock } from "../../dto";
import { agentreUiResources } from "../../../i18n";
import { TranscriptUIStateProvider } from "../../transcript-ui-state";

import { AgentSpawnCard, shortenModelName } from "./card";

// 卡片文案已随 canonical 子树搬进共享包,只剩包这一份;宿主 common.json 里再也
// 没有 canonical.*,断言必须改看包的 bundle 才还盯着真正生效的那份文案。
const enCommon = agentreUiResources.en;
const zhCommon = agentreUiResources["zh-CN"];

// expandCard 点击卡片头部展开详情区。折叠态下 agent-spawn-meta-* 节点本就常驻
// DOM(展开动画靠 CSS grid-template-rows,不是 display:none),getByTestId 在
// 折叠时一样查得到——只查 testid 证明不了值真的进了"要展开才可见"的区域。这里
// 改为先点击、断言 aria-hidden 确实从 "true" 翻到 "false",再把查询限定在这个
// 展开容器内,把"展开后可见"这个契约钉住;jsdom 不做布局,像素级可见性验证
// 留给人工复核。
// extractShrinkFactor 读出一个元素 Tailwind flex-shrink 类锁定的因子:
// `shrink-0` → 0(明确不可收缩);`shrink-[N]`(整数或小数) → N;裸 `shrink` →
// 1(默认因子);三种都不命中 → null(该元素完全没有可识别的 shrink 类)。调
// 用方必须自己决定 null 是否算失败——不像之前 `?? "1"` 那样把"找不到"和
// "就是 1"混同,那会让"整个删掉 shrink 类"这种破坏也悄悄通过。模型徽标的
// 让位因子取值 <1(比极简进度更晚让位),所以这里的方括号分支支持小数,不只
// 整数。
function extractShrinkFactor(className: string): number | null {
  if (/(?:^|\s)shrink-0(?:\s|$)/.test(className)) return 0;
  const bracket = /(?:^|\s)shrink-\[([0-9]*\.?[0-9]+)\](?:\s|$)/.exec(
    className,
  );
  if (bracket) return Number(bracket[1]);
  if (/(?:^|\s)shrink(?:\s|$)/.test(className)) return 1;
  return null;
}

function expandCard(container: HTMLElement): HTMLElement {
  const details = container.querySelector('[data-slot="agent-spawn-details"]');
  if (!details) throw new Error("Expected agent-spawn-details container");
  expect(details).toHaveAttribute("aria-hidden", "true");
  fireEvent.click(screen.getByRole("button", { expanded: false }));
  expect(details).toHaveAttribute("aria-hidden", "false");
  return details as HTMLElement;
}

describe("AgentSpawnCard", () => {
  it("renders nothing without canonical", () => {
    const block = { type: "tool_use" } as unknown as TranscriptBlock;
    const { container } = render(<AgentSpawnCard toolBlock={block} />);
    expect(container.firstChild).toBeNull();
  });

  it("renders Agent header with description + type", () => {
    const block = {
      type: "tool_use",
      toolName: "Task",
      canonical: {
        kind: "agent.spawn",
        agentSpawn: {
          taskId: "1",
          subagentType: "code-reviewer",
          taskDescription: "review PR",
          toolUses: 3,
          totalTokens: 1200,
          status: "running",
        },
      },
    } as unknown as TranscriptBlock;
    render(<AgentSpawnCard toolBlock={block} />);
    expect(screen.getByText("Agent")).toBeDefined();
    expect(screen.getByText("review PR")).toBeDefined();
    expect(screen.getByText("code-reviewer")).toBeDefined();
    // R9: running 态下 toolUses/totalTokens 以无文案的极简形式渲染在头部
    // (状态胶囊左侧),而非旧形态的 "3 tools"/"1.2K tok" 文案。
    expect(screen.getByTestId("agent-spawn-progress").textContent).toBe(
      "3 · 1.2K",
    );
  });

  it("overlays runtime state from block.subagent onto canonical.agentSpawn", () => {
    // canonical.agentSpawn 由 translator 算静态字段(description/subagentType/prompt),
    // 运行时态(toolUses/totalTokens/durationMs/status)来自 SubagentStarted/Progress/
    // Done 事件经 mergeSubagentMeta 合并到 block.subagent;readSpawn 必须把 block.subagent
    // overlay 上去,否则 header chip 永远显示 0 工具 / 无 token。
    const block = {
      type: "tool_use",
      toolName: "Task",
      canonical: {
        kind: "agent.spawn",
        agentSpawn: {
          taskDescription: "review PR",
          subagentType: "code-reviewer",
          prompt: "please review",
        },
      },
      subagent: {
        toolUses: 5,
        totalTokens: 2400,
        status: "completed",
        durationMs: 12000,
      },
    } as unknown as TranscriptBlock;
    const { container } = render(<AgentSpawnCard toolBlock={block} />);
    // 成功态没有状态胶囊(spec 决策 10),合并进来的运行时态改由头部次级信息作证。
    expect(screen.queryByTestId("agent-spawn-status-pill")).toBeNull();
    expect(screen.getByTestId("agent-spawn-outcome")).toHaveTextContent(
      "12.0s",
    );
    // R8: 完整的工具数/tokens/耗时下沉到展开区 meta 行,不再出现在头部;
    // 展开卡片、把查询限定在展开容器内,证明它们确实进了那个区域。
    const details = expandCard(container);
    expect(
      within(details).getByTestId("agent-spawn-meta-tools").textContent,
    ).toBe("5");
    expect(
      within(details).getByTestId("agent-spawn-meta-tokens").textContent,
    ).toContain("2.4K tok");
    expect(
      within(details).getByTestId("agent-spawn-meta-duration").textContent,
    ).toContain("12.0s");
  });

  it("settles without any status marker when completed", () => {
    const block = {
      type: "tool_use",
      toolName: "Task",
      canonical: {
        kind: "agent.spawn",
        agentSpawn: {
          taskId: "1",
          status: "completed",
        },
      },
    } as unknown as TranscriptBlock;
    const result = {
      type: "tool_result",
      text: "summary text",
    } as unknown as TranscriptBlock;
    const { container } = render(
      <AgentSpawnCard toolBlock={block} resultBlock={result} />,
    );
    // 「没有标记 = 成功」:胶囊不再出现,也不能靠 spinner 或错误色顶替。
    expect(screen.queryByTestId("agent-spawn-status-pill")).toBeNull();
    expect(container.querySelector(".animate-spin")).toBeNull();
  });

  it("renders an outlined model badge from the tool-input alias (A1)", () => {
    const block = {
      type: "tool_use",
      toolName: "Task",
      canonical: {
        kind: "agent.spawn",
        agentSpawn: {
          taskId: "1",
          subagentType: "general-purpose",
          model: "haiku",
        },
      },
    } as unknown as TranscriptBlock;
    render(<AgentSpawnCard toolBlock={block} />);
    const badge = screen.getByTestId("agent-spawn-model-badge");
    expect(badge.textContent).toBe("haiku");
    // 描边芯片:transparent 底 + border-strong 描边,与实心角色芯片(bg-secondary)区分。
    expect(badge.className).toContain("border-border-strong");
    expect(badge.className).not.toContain("bg-secondary");
  });

  it("overrides the alias with the normalized runtime model once the subagent frame arrives (A2)", () => {
    const block = {
      type: "tool_use",
      toolName: "Task",
      canonical: {
        kind: "agent.spawn",
        agentSpawn: {
          taskId: "1",
          model: "haiku",
        },
      },
      subagent: {
        model: "claude-haiku-4-5-20251001",
      },
    } as unknown as TranscriptBlock;
    render(<AgentSpawnCard toolBlock={block} />);
    expect(screen.getByTestId("agent-spawn-model-badge").textContent).toBe(
      "haiku-4-5",
    );
  });

  it("shows only the model badge when subagentType is absent (A3)", () => {
    // 缺失与降级规则 2:subagent_type 缺失但模型存在时,只渲染模型徽标。
    const block = {
      type: "tool_use",
      toolName: "Task",
      canonical: {
        kind: "agent.spawn",
        agentSpawn: { taskId: "1" },
      },
      subagent: { model: "claude-opus-5" },
    } as unknown as TranscriptBlock;
    render(<AgentSpawnCard toolBlock={block} />);
    expect(screen.getByTestId("agent-spawn-model-badge").textContent).toBe(
      "opus-5",
    );
    // 角色芯片不应出现在头部——subagentType 缺失时头部唯一渲染的身份徽标是
    // 模型。断言渲染结果本身(角色芯片的 testid 找不到),而非某个样式类是否
    // 出现:换掉角色芯片的底色 token 不该让这条用例继续通过,因为它证明的是
    // "芯片没有被渲染",不是"芯片没有用某个特定类名"。
    expect(screen.queryByTestId("agent-spawn-role-badge")).toBeNull();
  });

  it("renders no badge when neither source has a model (A4)", () => {
    const block = {
      type: "tool_use",
      toolName: "Task",
      canonical: {
        kind: "agent.spawn",
        agentSpawn: { taskId: "1", subagentType: "general-purpose" },
      },
      subagent: { toolUses: 2 },
    } as unknown as TranscriptBlock;
    render(<AgentSpawnCard toolBlock={block} />);
    expect(screen.queryByTestId("agent-spawn-model-badge")).toBeNull();
  });

  it("shows STOPPED label when cancelled, not RUNNING/spin", () => {
    // 用户在 turn 内点 Stop 时,chat_svc 把仍 running 的 SubagentStateBlock 改成
    // "canceled" 落 DB。前端必须识别这个值否则 narrowSpawnStatus 会 drop 掉它,
    // 回退到 base.status="running",卡片继续 spin → bug。
    const block = {
      type: "tool_use",
      toolName: "Task",
      canonical: {
        kind: "agent.spawn",
        agentSpawn: {
          taskId: "1",
          taskDescription: "long running task",
          status: "running",
        },
      },
      subagent: {
        status: "canceled",
        durationMs: 4200,
      },
    } as unknown as TranscriptBlock;
    render(<AgentSpawnCard toolBlock={block} />);
    expect(screen.getByText(/STOPPED/)).toBeDefined();
    expect(screen.queryByText(/RUNNING/)).toBeNull();
  });

  it("shows a unit-less numeric progress at the status pill's left while running (R9, A10)", () => {
    const block = {
      type: "tool_use",
      toolName: "Task",
      canonical: {
        kind: "agent.spawn",
        agentSpawn: {
          taskId: "1",
          status: "running",
          toolUses: 3,
          totalTokens: 14500,
        },
      },
    } as unknown as TranscriptBlock;
    const { container } = render(<AgentSpawnCard toolBlock={block} />);
    const progress = screen.getByTestId("agent-spawn-progress");
    expect(progress.textContent).toBe("3 · 14.5K");
    // 无文案的极简进度对读屏软件是两个无名数字;补的 aria-label 让折叠态下
    // 也能读懂这两个数字分别是什么,不必强制展开卡片。断言只查数值本身,不
    // 绑定具体语言用词,避免与 locale 措辞耦合。
    const progressAriaLabel = progress.getAttribute("aria-label");
    expect(progressAriaLabel).toContain("3");
    expect(progressAriaLabel).toContain("14.5K");
    expect(progress.textContent).not.toMatch(/tok|个工具/);
    // R9: 这个元素被定义为"纯数字、无文案"。aria-label 服务读屏且不可见,
    // 符合 R9；但 title 会在鼠标悬停时可见地弹出同样的文案,等于把 R9 刚
    // 去掉的文案又从后门带回来了,必须不存在。
    expect(progress).not.toHaveAttribute("title");
    // R8/A10: 展开区 meta 行(带标签的完整值)与头部极简进度同时存在——展开
    // 卡片、把查询限定在展开容器内,证明完整值确实也进了那个区域。
    const details = expandCard(container);
    expect(
      within(details).getByTestId("agent-spawn-meta-tools").textContent,
    ).toBe("3");
    expect(
      within(details).getByTestId("agent-spawn-meta-tokens").textContent,
    ).toBe("14.5K tok");
  });

  it("hides the minimal progress once the dispatch is done (R9, A10)", () => {
    const block = {
      type: "tool_use",
      toolName: "Task",
      canonical: {
        kind: "agent.spawn",
        agentSpawn: {
          taskId: "1",
          status: "completed",
          toolUses: 3,
          totalTokens: 14500,
        },
      },
    } as unknown as TranscriptBlock;
    render(<AgentSpawnCard toolBlock={block} />);
    expect(screen.queryByTestId("agent-spawn-progress")).toBeNull();
  });

  it("gives the description a much larger shrink priority than the minimal progress, so description gives way first (R11)", () => {
    // jsdom 不做布局,像素级的"谁先被截断"验证留给人工复核(spec 测试接缝已注明)。
    // 这里锁住让位次序背后真正起作用的机制:两者若用同一个收缩因子,会按各自
    // 内容宽度成比例同时收缩,描述短、进度长时进度反而先被挤;要让描述真的先
    // 让位,描述的 flex-shrink 权重必须显著大于进度,不受两者内容长度比例影响。
    const block = {
      type: "tool_use",
      toolName: "Task",
      canonical: {
        kind: "agent.spawn",
        agentSpawn: {
          taskId: "1",
          taskDescription: "x",
          status: "running",
          toolUses: 3,
          totalTokens: 14500,
        },
      },
    } as unknown as TranscriptBlock;
    render(<AgentSpawnCard toolBlock={block} />);
    const description = screen.getByText("x");
    const progress = screen.getByTestId("agent-spawn-progress");
    const pill = screen.getByText(/RUNNING/);

    // extractShrinkFactor 显式区分三种情况而非一律 fallback 到某个默认值:
    // 没有任何 shrink 类(改动把它整个删掉)返回 null;`shrink-0`(改动关掉了
    // 可收缩性)返回 0;裸 `shrink` 返回默认因子 1;`shrink-[N]` 返回 N。
    // 之前 `?? "1"` 的写法对"删掉 shrink"和"改成 shrink-0"两种破坏都会
    // 悄悄算出 1,让契约形同虚设——这里让这两种破坏都能被下面的断言钉住。
    const descriptionShrink = extractShrinkFactor(description.className);
    const progressShrink = extractShrinkFactor(progress.className);
    expect(descriptionShrink).not.toBeNull();
    expect(progressShrink).not.toBeNull();
    // 进度必须仍然是"可收缩的"——如果它被改成 shrink-0,这里必须变红,
    // 而不是让下面的倍数比较因为"0 也小于任何正数的四分之一"而continue通过。
    expect(progressShrink).toBeGreaterThan(0);
    expect(descriptionShrink as number).toBeGreaterThan(
      (progressShrink as number) * 4,
    );
    // 状态胶囊在任何宽度下都不参与收缩(R11 后半已成立,这里一并锁住)。
    expect(pill.className).toContain("shrink-0");
  });

  it("hides the minimal progress for canceled and error states, not just completed", () => {
    const canceled = {
      type: "tool_use",
      toolName: "Task",
      canonical: {
        kind: "agent.spawn",
        agentSpawn: { taskId: "1", status: "running", toolUses: 2 },
      },
      subagent: { status: "canceled" },
    } as unknown as TranscriptBlock;
    const { unmount } = render(<AgentSpawnCard toolBlock={canceled} />);
    expect(screen.queryByTestId("agent-spawn-progress")).toBeNull();
    unmount();

    const errored = {
      type: "tool_use",
      toolName: "Task",
      canonical: {
        kind: "agent.spawn",
        agentSpawn: { taskId: "1", status: "running", toolUses: 2 },
      },
    } as unknown as TranscriptBlock;
    const result = {
      type: "tool_result",
      isError: true,
      text: "boom",
    } as unknown as TranscriptBlock;
    render(<AgentSpawnCard toolBlock={errored} resultBlock={result} />);
    expect(screen.queryByTestId("agent-spawn-progress")).toBeNull();
  });

  it("shows the uncut model value in the expanded meta row, not the shortened badge form (R8)", () => {
    const block = {
      type: "tool_use",
      toolName: "Task",
      canonical: {
        kind: "agent.spawn",
        agentSpawn: { taskId: "1", model: "haiku" },
      },
      subagent: { model: "claude-haiku-4-5-20251001" },
    } as unknown as TranscriptBlock;
    const { container } = render(<AgentSpawnCard toolBlock={block} />);
    expect(screen.getByTestId("agent-spawn-model-badge").textContent).toBe(
      "haiku-4-5",
    );
    const details = expandCard(container);
    expect(
      within(details).getByTestId("agent-spawn-meta-model").textContent,
    ).toBe("claude-haiku-4-5-20251001");
  });

  it("omits zero-value items from the meta row instead of showing a 0 placeholder (A12)", () => {
    const block = {
      type: "tool_use",
      toolName: "Task",
      canonical: {
        kind: "agent.spawn",
        agentSpawn: {
          taskId: "1",
          status: "completed",
          toolUses: 4,
          totalTokens: 0,
        },
      },
    } as unknown as TranscriptBlock;
    const { container } = render(<AgentSpawnCard toolBlock={block} />);
    const details = expandCard(container);
    expect(
      within(details).getByTestId("agent-spawn-meta-tools").textContent,
    ).toBe("4");
    expect(within(details).queryByTestId("agent-spawn-meta-tokens")).toBeNull();
    expect(
      within(details).queryByTestId("agent-spawn-meta-duration"),
    ).toBeNull();
    expect(within(details).queryByTestId("agent-spawn-meta-model")).toBeNull();
    expect(within(details).queryByText(/0 tok/)).toBeNull();
  });

  it("shows the model badge's full text without a width-independent cap when space is not the constraint (R7)", () => {
    // 修订后的 R7:横向空间充足时徽标必须完整显示裁剪后的全文,不做与可用
    // 宽度无关的固定截断(此前 max-w-[16ch] 无条件生效,720px 下也在约 16
    // 字符处切掉)。实现决策 9 仍然成立——网关接入的第三方模型名是任意串,
    // 可以很长——但"可以很长"应该靠 R11 的 flex-shrink 让位在空间不足时才
    // 裁,而不是一个跟宽度无关的硬 max-w。这里断言:不存在这种硬上限类。
    const longModel = "moonshotai/kimi-k2-0905-preview";
    const block = {
      type: "tool_use",
      toolName: "Task",
      canonical: {
        kind: "agent.spawn",
        agentSpawn: { taskId: "1" },
      },
      subagent: { model: longModel },
    } as unknown as TranscriptBlock;
    render(<AgentSpawnCard toolBlock={block} />);
    const badge = screen.getByTestId("agent-spawn-model-badge");
    // 完整原值仍在 DOM 里(截断只是视觉裁切,不是数据丢失)。
    expect(badge.textContent).toBe(longModel);
    expect(badge.className).not.toMatch(/max-w-\[/);
  });

  it("puts truncate on an inner span and overflow-hidden on the flex item, so the ellipsis actually renders (R7)", () => {
    // 上一轮的测试只断言 className 里出现过 "truncate" 这个词,抓不到
    // "truncate 加在 inline-flex 元素上不生效"这个缺陷——text-overflow:
    // ellipsis 只作用于块级容器,加在 flex 容器本身,文本子节点会变成匿名
    // flex item,省略号永远不会被绘制,实际效果是齐字硬切、没有任何"后面还
    // 有内容"的提示。同文件的 agent-spawn-progress 元素做对了(truncate 在
    // 内层 span、overflow-hidden 留在 flex 容器):这里断言模型徽标照同一
    // 结构写,而不是只查字符串里是否出现过这个类名。
    const longModel = "moonshotai/kimi-k2-0905-preview";
    const block = {
      type: "tool_use",
      toolName: "Task",
      canonical: {
        kind: "agent.spawn",
        agentSpawn: { taskId: "1" },
      },
      subagent: { model: longModel },
    } as unknown as TranscriptBlock;
    render(<AgentSpawnCard toolBlock={block} />);
    const badge = screen.getByTestId("agent-spawn-model-badge");
    // 承载 truncate 的必须是内层子节点,不是这个 flex item 本身。
    expect(badge.className).not.toMatch(/(?:^|\s)truncate(?:\s|$)/);
    // flex item 自己留 overflow-hidden + min-w-0,让它在挤压下真能收缩到 0
    // 而不是撑破容器或被内容撑宽。
    expect(badge.className).toMatch(/(?:^|\s)overflow-hidden(?:\s|$)/);
    expect(badge.className).toMatch(/(?:^|\s)min-w-0(?:\s|$)/);
    const innerSpan = badge.querySelector("span");
    expect(innerSpan).not.toBeNull();
    expect(innerSpan!.className).toMatch(/(?:^|\s)truncate(?:\s|$)/);
    expect(innerSpan!.textContent).toBe(longModel);
  });

  it("gives the model badge a smaller shrink priority than the minimal progress, so it is the last to yield (R7, R11)", () => {
    // R11 的让位次序是"任务描述 → 极简进度 → 其它",模型徽标属于"其它":
    // 排在描述与极简进度都已经让位之后才开始收缩。用同样的机制(flex-shrink
    // 权重比较)锁住这个次序,而不是像描述/进度那样只比较两者——这里额外
    // 断言模型徽标的权重明显小于极简进度的默认权重(1)。
    const block = {
      type: "tool_use",
      toolName: "Task",
      canonical: {
        kind: "agent.spawn",
        agentSpawn: {
          taskId: "1",
          status: "running",
          toolUses: 3,
          totalTokens: 14500,
        },
      },
      subagent: { model: "claude-haiku-4-5-20251001" },
    } as unknown as TranscriptBlock;
    render(<AgentSpawnCard toolBlock={block} />);
    const badge = screen.getByTestId("agent-spawn-model-badge");
    const progress = screen.getByTestId("agent-spawn-progress");
    const modelShrink = extractShrinkFactor(badge.className);
    const progressShrink = extractShrinkFactor(progress.className);
    expect(modelShrink).not.toBeNull();
    expect(progressShrink).not.toBeNull();
    // 必须仍然可收缩(不是 shrink-0,否则空间不足时不会真的参与让位)。
    expect(modelShrink as number).toBeGreaterThan(0);
    expect(progressShrink as number).toBeGreaterThan(
      (modelShrink as number) * 2,
    );
  });

  it("renders no meta row at all when every dispatch metric is empty", () => {
    const block = {
      type: "tool_use",
      toolName: "Task",
      canonical: {
        kind: "agent.spawn",
        agentSpawn: { taskId: "1", subagentType: "general-purpose" },
      },
    } as unknown as TranscriptBlock;
    const { container } = render(<AgentSpawnCard toolBlock={block} />);
    const details = expandCard(container);
    expect(within(details).queryByTestId("agent-spawn-meta-row")).toBeNull();
  });
});

function normalizedSpawnBlock({
  mode = "parallel",
  runs,
  status = "running",
}: {
  mode?: "single" | "parallel" | "chain";
  runs: Record<string, unknown>[];
  status?: string;
}): TranscriptBlock {
  return {
    type: "tool_use",
    toolName: "subagent",
    toolUseId: "toolu-parent",
    canonical: {
      kind: "agent.spawn",
      agentSpawn: {
        taskId: "toolu-parent",
        mode,
        runs: runs.map(
          ({
            id,
            index,
            agent,
            profile,
            agentSource,
            task,
            requestedModel,
          }) => ({
            id,
            index,
            agent,
            profile,
            agentSource,
            task,
            requestedModel,
          }),
        ),
      },
    },
    subagent: { mode, runs, status },
  } as unknown as TranscriptBlock;
}

function groupedChildren(
  entries: Array<{
    runId?: string;
    toolName: string;
    toolUseId: string;
    toolInput?: Record<string, unknown>;
    result?: string;
    isError?: boolean;
  }>,
) {
  const all: TranscriptBlock[] = [];
  const byRun = new Map<string, TranscriptBlock[]>();
  const fallback: TranscriptBlock[] = [];
  for (const entry of entries) {
    const target = entry.runId ? (byRun.get(entry.runId) ?? []) : fallback;
    const toolBlock = {
      type: "tool_use",
      toolName: entry.toolName,
      toolUseId: entry.toolUseId,
      subagentRunId: entry.runId,
      ...(entry.toolInput ? { toolInput: entry.toolInput } : {}),
    } as TranscriptBlock;
    target.push(toolBlock);
    all.push(toolBlock);
    if (entry.result !== undefined) {
      const resultBlock = {
        type: "tool_result",
        toolUseId: entry.toolUseId,
        text: entry.result,
        isError: entry.isError,
        subagentRunId: entry.runId,
      } as TranscriptBlock;
      target.push(resultBlock);
      all.push(resultBlock);
    }
    if (entry.runId) byRun.set(entry.runId, target);
  }
  return { all, byRun, fallback };
}

describe("AgentSpawnCard normalized runs", () => {
  it("Given one normalized run, When rendered, Then the legacy visual structure is reused with profile separate and observed model preferred", () => {
    const block = normalizedSpawnBlock({
      mode: "single",
      status: "completed",
      runs: [
        {
          id: "run-one",
          index: 0,
          profile: "review-profile",
          task: "Review the authentication flow",
          requestedModel: "haiku",
          model: "claude-sonnet-4-5-20260101",
          status: "completed",
          summary: "Review complete",
        },
      ],
    });
    const { container } = render(
      <AgentSpawnCard
        toolBlock={block}
        resultBlock={
          { type: "tool_result", text: "Top-level summary" } as TranscriptBlock
        }
        childBlocks={groupedChildren([])}
      />,
    );

    expect(screen.getByText("Agent")).toBeInTheDocument();
    expect(screen.getByTestId("agent-spawn-profile-badge")).toHaveTextContent(
      "review-profile",
    );
    expect(screen.getByTestId("agent-spawn-model-badge")).toHaveTextContent(
      "sonnet-4-5",
    );
    expect(
      screen.queryByText("review-profile", {
        selector: "[data-testid='agent-spawn-role-badge']",
      }),
    ).toBeNull();

    const details = expandCard(container);
    expect(within(details).getByText("TASK PROMPT")).toBeInTheDocument();
    expect(
      within(details).getByText("Review the authentication flow"),
    ).toBeInTheDocument();
    expect(within(details).getByText("Top-level summary")).toBeInTheDocument();
    expect(within(details).queryByText("Review complete")).toBeNull();
  });

  it("Given a normalized single snapshot omits per-run status but has terminal outer status, When rendered, Then the card settles instead of falling back to running", () => {
    const block = normalizedSpawnBlock({
      mode: "single",
      status: "completed",
      runs: [
        {
          id: "run-one",
          index: 0,
          agent: "reviewer",
          task: "Review",
        },
      ],
    });
    render(<AgentSpawnCard toolBlock={block} />);

    // 落定 = 没有任何状态标记(成功态无胶囊),而不是继续 spin 在 RUNNING。
    expect(screen.queryByTestId("agent-spawn-status-pill")).toBeNull();
    expect(screen.queryByText("RUNNING")).toBeNull();
    expect(
      screen.getByTestId("agent-spawn-card").querySelector(".animate-spin"),
    ).toBeNull();
  });

  it("Given a normalized single run without an outer result, When expanded, Then its observed output supplies the summary instead of an invented empty state", () => {
    const block = normalizedSpawnBlock({
      mode: "single",
      status: "completed",
      runs: [
        {
          id: "run-one",
          index: 0,
          agent: "reviewer",
          task: "Review the authentication flow",
          status: "completed",
          summary: "Observed child output",
        },
      ],
    });
    const { container } = render(<AgentSpawnCard toolBlock={block} />);

    const details = expandCard(container);
    expect(
      within(details).getByText("Observed child output"),
    ).toBeInTheDocument();
    expect(within(details).queryByText("No summary")).toBeNull();
  });

  it("Given an unmatched child call in a terminal normalized single run, When rendered, Then the step settles as an unknown-result marker without spinning", () => {
    const block = normalizedSpawnBlock({
      mode: "single",
      status: "completed",
      runs: [
        {
          id: "run-one",
          index: 0,
          agent: "reviewer",
          task: "Review",
          status: "completed",
        },
      ],
    });
    const { container } = render(
      <AgentSpawnCard
        toolBlock={block}
        childBlocks={groupedChildren([
          {
            runId: "run-one",
            toolName: "Bash",
            toolUseId: "unmatched-single",
          },
        ])}
      />,
    );

    const details = expandCard(container);
    // 步骤区是活动块的一行:成功态无标记,但这一步并没有成功 —— run 已终结而它
    // 始终没配到结果,归属不明,必须与「跑完了」区分开(且不再转 spinner)。
    expect(within(details).getByTestId("activity-name")).toHaveTextContent(
      "Bash",
    );
    expect(within(details).getByTestId("activity-pending")).toHaveTextContent(
      "unknown result",
    );
    expect(details.querySelector(".animate-spin")).toBeNull();
    fireEvent.click(within(details).getByRole("button", { name: /Bash/i }));
    // 结果段不编造内容:没有 tool_result 就报「等待返回」,而不是伪造一段空结果。
    expect(
      within(details).getByTestId("activity-row-body"),
    ).not.toHaveTextContent("(empty result)");
  });

  it("Given partial and unknown normalized statuses, When rendered, Then their icons are warning and neutral rather than success checks", () => {
    const partial = normalizedSpawnBlock({
      status: "partial",
      runs: [
        {
          id: "run-done",
          index: 0,
          agent: "done",
          task: "Done",
          status: "completed",
        },
        {
          id: "run-failed",
          index: 1,
          agent: "failed",
          task: "Failed",
          status: "failed",
        },
        {
          id: "run-unknown",
          index: 2,
          agent: "unknown",
          task: "Unknown",
          status: "unknown",
        },
      ],
    });
    render(<AgentSpawnCard toolBlock={partial} />);

    expect(
      screen
        .getByText("PARTIAL")
        .parentElement?.querySelector(".lucide-triangle-alert"),
    ).not.toBeNull();
    expect(
      screen
        .getByText("UNKNOWN")
        .parentElement?.querySelector(".lucide-circle-help"),
    ).not.toBeNull();
  });

  it("Given parallel runs and fallback children, When expanded, Then ordered isolated groups expose translated statuses, badges, output and fallback steps", () => {
    const block = normalizedSpawnBlock({
      status: "partial",
      runs: [
        {
          id: "run-wait",
          index: 0,
          agent: "scout",
          agentSource: "project",
          profile: "repo-reader",
          task: "Inspect models",
          requestedModel: "haiku",
          status: "waiting",
        },
        {
          id: "run-live",
          index: 1,
          agent: "reviewer",
          agentSource: "user",
          task: "Run tests",
          requestedModel: "sonnet",
          model: "claude-opus-5-20260101",
          status: "running",
        },
        {
          id: "run-done",
          index: 2,
          agent: "writer",
          task: "Summarize",
          status: "completed",
          summary: "Found the relevant models.",
        },
        {
          id: "run-failed",
          index: 3,
          agent: "checker",
          task: "Check edge cases",
          status: "failed",
          errorMessage: "Tests failed",
        },
        {
          id: "run-skipped",
          index: 4,
          agent: "planner",
          task: "Plan follow-up",
          status: "skipped",
        },
        {
          id: "run-unknown",
          index: 5,
          agent: "auditor",
          task: "Audit result",
          status: "unknown",
        },
      ],
    });
    const children = groupedChildren([
      {
        runId: "run-live",
        toolName: "Bash",
        toolUseId: "shared-call",
      },
      {
        runId: "run-done",
        toolName: "Read",
        toolUseId: "shared-call",
        result: "read done",
      },
      { toolName: "Glob", toolUseId: "missing-run" },
      { runId: "not-declared", toolName: "Grep", toolUseId: "unknown-run" },
    ]);
    const { container } = render(
      <AgentSpawnCard toolBlock={block} childBlocks={children} />,
    );

    expect(screen.getByText("Agents")).toBeInTheDocument();
    expect(screen.getByText("Parallel")).toBeInTheDocument();
    expect(screen.getByText("4/6")).toBeInTheDocument();
    expect(screen.getByText("PARTIAL")).toBeInTheDocument();

    const details = expandCard(container);
    const groups = within(details).getAllByTestId("agent-spawn-run-group");
    expect(groups.map((group) => group.getAttribute("data-run-id"))).toEqual([
      "run-wait",
      "run-live",
      "run-done",
      "run-failed",
      "run-skipped",
      "run-unknown",
    ]);
    expect(within(groups[0]).getByText("project")).toBeInTheDocument();
    expect(within(groups[0]).getByText("repo-reader")).toBeInTheDocument();
    expect(within(groups[1]).getByText("opus-5")).toBeInTheDocument();
    expect(
      within(groups[2]).getByText("Found the relevant models."),
    ).toBeInTheDocument();
    expect(within(groups[3]).getByText("Tests failed")).toBeInTheDocument();
    expect(within(groups[4]).getByText("SKIPPED")).toBeInTheDocument();
    expect(within(groups[5]).getByText("UNKNOWN")).toBeInTheDocument();
    expect(within(groups[1]).getByText("Bash")).toBeInTheDocument();
    expect(within(groups[2]).getByText("Read")).toBeInTheDocument();
    expect(within(groups[1]).queryByText("Read")).toBeNull();
    const fallback = within(details).getByTestId("agent-spawn-fallback-steps");
    expect(within(fallback).getByText("Glob")).toBeInTheDocument();
    expect(within(fallback).getByText("Grep")).toBeInTheDocument();
    // 归属不明的子调用仍单独成区,内部同样是活动块的两行(不再各自一张 step 卡),
    // 每一行都报「结果未知」—— 它们既没成功也不在跑。
    expect(within(fallback).getAllByTestId("activity-row")).toHaveLength(2);
    for (const marker of within(fallback).getAllByTestId("activity-pending")) {
      expect(marker).toHaveTextContent("unknown result");
    }
    expect(fallback.querySelector(".animate-spin")).toBeNull();
  });

  it("Given chain runs complete, fail and skip in declaration order, When rendered, Then sequence and skipped work remain explicit without invented fields", () => {
    const block = normalizedSpawnBlock({
      mode: "chain",
      status: "failed",
      runs: [
        {
          id: "run-third",
          index: 2,
          agent: "planner",
          task: "Plan follow-up",
          status: "skipped",
        },
        {
          id: "run-first",
          index: 0,
          agent: "scout",
          task: "Inspect",
          status: "completed",
          summary: "Inspected",
        },
        {
          id: "run-second",
          index: 1,
          agent: "reviewer",
          task: "Review",
          status: "failed",
          errorMessage: "Review failed",
        },
      ],
    });
    const { container } = render(<AgentSpawnCard toolBlock={block} />);

    expect(screen.getByText("Chain")).toBeInTheDocument();
    const details = expandCard(container);
    const groups = within(details).getAllByTestId("agent-spawn-run-group");
    expect(groups.map((group) => group.getAttribute("data-run-id"))).toEqual([
      "run-first",
      "run-second",
      "run-third",
    ]);
    expect(within(groups[0]).getByText("DONE")).toBeInTheDocument();
    expect(within(groups[1]).getByText("FAILED")).toBeInTheDocument();
    expect(within(groups[2]).getByText("SKIPPED")).toBeInTheDocument();
    expect(within(groups[2]).queryByTestId("agent-spawn-run-model")).toBeNull();
    expect(within(groups[2]).queryByText("Inspected")).toBeNull();
    expect(within(groups[2]).queryByText("Review failed")).toBeNull();
  });

  it.each([
    ["failed", "FAILED", "failed"],
    ["canceled", "STOPPED", "canceled"],
    ["completed", "DONE", "unknown result"],
    ["unknown", "UNKNOWN", "unknown result"],
  ])(
    "Given an unmatched child call in a %s run, When rendered, Then the run settles as %s, its step reports %s and no result text is invented",
    (runStatus, expectedRunStatus, expectedStepMarker) => {
      const block = normalizedSpawnBlock({
        status: runStatus,
        runs: [
          {
            id: "run-one",
            index: 0,
            agent: "worker",
            task: "Do work",
            status: runStatus,
          },
        ],
      });
      const { container } = render(
        <AgentSpawnCard
          toolBlock={block}
          childBlocks={groupedChildren([
            {
              runId: "run-one",
              toolName: "Bash",
              toolUseId: "unmatched",
            },
          ])}
        />,
      );
      const details = expandCard(container);
      const group = within(details).getByTestId("agent-spawn-run-group");
      // run 分组的状态胶囊不变(它是 run 分组的一部分,不受成功态去胶囊影响)。
      expect(within(group).getByText(expectedRunStatus)).toBeInTheDocument();
      expect(group.querySelector(".animate-spin")).toBeNull();
      const runToggle = within(group).getByRole("button", { name: /worker/i });
      if (runToggle.getAttribute("aria-expanded") === "false") {
        fireEvent.click(runToggle);
      }
      // 这一步按 run 的终态归属:失败 / 取消照报,其余一律「结果未知」——
      // 它没有结果,不能和跑完的一步长得一模一样。
      expect(within(group).getByTestId("activity-pending")).toHaveTextContent(
        expectedStepMarker,
      );
      fireEvent.click(within(group).getByRole("button", { name: /Bash/i }));
      const stepBody = within(group).getByTestId("activity-row-body");
      expect(stepBody).not.toHaveTextContent("(empty result)");
    },
  );

  it("Given a child result with empty text, When rendered, Then the known result settles with the existing empty-result treatment", () => {
    const block = normalizedSpawnBlock({
      status: "completed",
      runs: [
        {
          id: "run-one",
          index: 0,
          agent: "worker",
          task: "Do work",
          status: "completed",
        },
      ],
    });
    const { container } = render(
      <AgentSpawnCard
        toolBlock={block}
        childBlocks={groupedChildren([
          {
            runId: "run-one",
            toolName: "Read",
            toolUseId: "empty-result",
            result: "",
          },
        ])}
      />,
    );
    const details = expandCard(container);
    const group = within(details).getByTestId("agent-spawn-run-group");
    fireEvent.click(within(group).getByRole("button", { name: /worker/i }));
    fireEvent.click(within(group).getByRole("button", { name: /Read/i }));
    expect(within(group).getByTestId("activity-row-body")).toHaveTextContent(
      "(empty result)",
    );
  });

  it("Given untouched and manually collapsed run toggles, When progress arrives, Then only untouched activity auto-opens and controls remain keyboard accessible", async () => {
    const user = userEvent.setup();
    const waitingRuns = [
      {
        id: "run-auto",
        index: 0,
        agent: "auto",
        task: "Auto opens",
        status: "waiting",
      },
      {
        id: "run-user",
        index: 1,
        agent: "manual",
        task: "Stay collapsed",
        status: "waiting",
      },
    ];
    const { container, rerender } = render(
      <AgentSpawnCard
        toolBlock={normalizedSpawnBlock({ runs: waitingRuns })}
        childBlocks={groupedChildren([])}
      />,
    );
    expandCard(container);
    const manualToggle = screen.getByRole("button", {
      name: /manual/i,
      expanded: false,
    });
    manualToggle.focus();
    await user.keyboard("{Enter}");
    expect(manualToggle).toHaveAttribute("aria-expanded", "true");
    await user.keyboard("{Enter}");
    expect(manualToggle).toHaveAttribute("aria-expanded", "false");

    const progressedRuns = waitingRuns.map((run) => ({
      ...run,
      status: "running",
    }));
    rerender(
      <AgentSpawnCard
        toolBlock={normalizedSpawnBlock({ runs: progressedRuns })}
        childBlocks={groupedChildren([
          { runId: "run-auto", toolName: "Read", toolUseId: "auto-call" },
          { runId: "run-user", toolName: "Bash", toolUseId: "user-call" },
        ])}
      />,
    );

    expect(screen.getByRole("button", { name: /auto/i })).toHaveAttribute(
      "aria-expanded",
      "true",
    );
    expect(screen.getByRole("button", { name: /manual/i })).toHaveAttribute(
      "aria-expanded",
      "false",
    );
  });
});

describe("shortenModelName", () => {
  it("strips the claude- prefix and the trailing 8-digit date segment", () => {
    expect(shortenModelName("claude-haiku-4-5-20251001")).toBe("haiku-4-5");
  });

  it("strips the claude- prefix when there is no trailing date segment", () => {
    expect(shortenModelName("claude-opus-5")).toBe("opus-5");
  });

  it("leaves a bare alias untouched", () => {
    expect(shortenModelName("sonnet")).toBe("sonnet");
  });

  it("leaves an arbitrary third-party model name untouched", () => {
    expect(shortenModelName("glm-4.6")).toBe("glm-4.6");
  });
});

describe("progress accessibility label i18n (canonical.agentSpawn.progressAria)", () => {
  it("announces the singular English wording when there is exactly one tool, not '1 tools' (i18next plural fallback)", () => {
    // progressAria.tools 之前只有一个不带 _one/_other 后缀的裸键
    // ("{{count}} tools")。i18next 在有 count 选项时会先找 `_other`,该键
    // 没有任何后缀变体时才整体回退到裸键——所以即使 toolUses === 1(首帧
    // 进度的常见状态),读到的也是裸键 "{{count}} tools" → "1 tools"。
    const block = {
      type: "tool_use",
      toolName: "Task",
      canonical: {
        kind: "agent.spawn",
        agentSpawn: {
          taskId: "1",
          status: "running",
          toolUses: 1,
          totalTokens: 0,
        },
      },
    } as unknown as TranscriptBlock;
    render(<AgentSpawnCard toolBlock={block} />);
    const progress = screen.getByTestId("agent-spawn-progress");
    const ariaLabel = progress.getAttribute("aria-label");
    expect(ariaLabel).toBe("1 tool");
    expect(ariaLabel).not.toBe("1 tools");
  });

  it("avoids the reserved i18next `count` interpolation option for the tokens template", () => {
    // formatTokenCount 产出的是展示用字符串(如 "14.5K"),不是可数的语法数
    // 量。card.tsx 把它塞进了 i18next 的保留选项 `count`——该选项会被送进
    // Intl.PluralRules.select() 做单复数判定,当前只是靠 NaN 落到 "other"
    // 侥幸没炸。模板不应该依赖这个保留名字,这里锁住两份 locale 都已经改用
    // 普通具名占位符,不再含 "{{count}}"。
    expect(enCommon.canonical.agentSpawn.progressAria.tokens).not.toContain(
      "{{count}}",
    );
    expect(zhCommon.canonical.agentSpawn.progressAria.tokens).not.toContain(
      "{{count}}",
    );
  });

  it("still substitutes the formatted token value correctly after moving off the reserved `count` option", () => {
    // 配套回归:locale 模板换了占位符名之后,card.tsx 调用点必须传同名参数,
    // 否则插值对不上,渲染结果会原样留下 "{{value}}" 这种字面量。
    const block = {
      type: "tool_use",
      toolName: "Task",
      canonical: {
        kind: "agent.spawn",
        agentSpawn: {
          taskId: "1",
          status: "running",
          toolUses: 0,
          totalTokens: 2400,
        },
      },
    } as unknown as TranscriptBlock;
    render(<AgentSpawnCard toolBlock={block} />);
    const progress = screen.getByTestId("agent-spawn-progress");
    const ariaLabel = progress.getAttribute("aria-label");
    expect(ariaLabel).toBe("2.4K tokens");
    expect(ariaLabel).not.toMatch(/\{\{/);
  });
});

// ─── step 列表懒挂载回归测试 ────────────────────────────────────────────────
// 性能修复的契约:折叠的 AgentSpawnCard 不 mount 任何步骤 / 步骤结果文本。
// 大 subagent(150-200 个嵌套工具调用)折叠时若把整列表 + 结果文本挂进 DOM,
// transcript 在会话打开 / tab 切换 / 滚动时卡顿(实测单消息 3000+ 节点、1.7MB
// 隐藏文本)。下面钉死:折叠 → 零步骤节点;展开卡片 → 组头出现(超阈值时步骤
// 仍不 mount,展开组头才逐条挂);步骤结果文本仅在展开该条步骤时 mount。
describe("AgentSpawnCard step-list laziness (perf regression)", () => {
  // 真实的工具结果是多行的(read/grep 的大文件输出),折叠的活动行对多行结果
  // 只报规模(「N 行」)。单行结果按 spec 会在行尾留一段截断预览 —— 那是设计
  // 上的实义信息,不是懒挂载漏网,所以这里用多行样本来量「隐藏文本」。
  const manyChildSteps = (count: number): TranscriptBlock[] => {
    const all: TranscriptBlock[] = [];
    for (let i = 0; i < count; i++) {
      all.push({
        type: "tool_use",
        toolName: "Read",
        toolUseId: `call-${i}`,
      } as TranscriptBlock);
      all.push({
        type: "tool_result",
        toolUseId: `call-${i}`,
        text: `${"x".repeat(100)}\n`.repeat(100),
      } as TranscriptBlock);
    }
    return all;
  };

  it("Given a single-spawn card with many child steps, When collapsed, Then no step row or result text is mounted", () => {
    const block = normalizedSpawnBlock({
      mode: "single",
      status: "completed",
      runs: [{ id: "run-one", index: 0, status: "completed" }],
    });
    const { container } = render(
      <AgentSpawnCard
        toolBlock={block}
        childBlocks={{ all: manyChildSteps(150), byRun: new Map() }}
      />,
    );

    // 折叠态:150 条 step 一条都不进 DOM —— 连活动块的组头都不 mount,
    // 更没有 60KB 结果文本。
    expect(
      container.querySelectorAll('[data-testid="activity-block"]'),
    ).toHaveLength(0);
    expect(
      container.querySelectorAll('[data-testid="activity-row"]'),
    ).toHaveLength(0);
    expect(container.textContent).not.toContain("xxxxx");
  });

  it("Given the same card with more than 20 steps, When expanded, Then only the group header mounts until it is opened", () => {
    const block = normalizedSpawnBlock({
      mode: "single",
      status: "completed",
      runs: [{ id: "run-one", index: 0, status: "completed" }],
    });
    const { container } = render(
      <AgentSpawnCard
        toolBlock={block}
        childBlocks={{ all: manyChildSteps(150), byRun: new Map() }}
      />,
    );

    const details = expandCard(container);
    expect(
      container.querySelectorAll('[data-testid="activity-row"]'),
    ).toHaveLength(0);
    expect(container.textContent).not.toContain("xxxxx");

    fireEvent.click(within(details).getByTestId("activity-header"));
    expect(
      container.querySelectorAll('[data-testid="activity-row"]'),
    ).toHaveLength(150);
  });

  it("Given an expanded card with a collapsed step, When rendered, Then the step result text is not mounted until that step expands", () => {
    const block = normalizedSpawnBlock({
      mode: "single",
      status: "completed",
      runs: [{ id: "run-one", index: 0, status: "completed" }],
    });
    const { container } = render(
      <AgentSpawnCard
        toolBlock={block}
        childBlocks={{ all: manyChildSteps(1), byRun: new Map() }}
      />,
    );

    expandCard(container);
    const details = container.querySelector(
      '[data-slot="agent-spawn-details"]',
    ) as HTMLElement;
    // step 折叠:活动行在,参数 / 结果文本不在 DOM。
    expect(within(details).getByTestId("activity-row")).toBeInTheDocument();
    expect(within(details).queryByTestId("activity-row-body")).toBeNull();
    expect(details.textContent).not.toContain("xxxxx");

    fireEvent.click(within(details).getByRole("button", { name: /Read/i }));
    expect(within(details).getByTestId("activity-row-body")).toHaveTextContent(
      "xxxxx",
    );
  });

  it("Given an expanded single-spawn card with steps, When collapsed, Then steps stay mounted through the collapse transition and unmount only after it ends", () => {
    const block = normalizedSpawnBlock({
      mode: "single",
      status: "completed",
      runs: [{ id: "run-one", index: 0, agent: "worker", status: "completed" }],
    });
    const { container } = render(
      <AgentSpawnCard
        toolBlock={block}
        childBlocks={groupedChildren([
          {
            runId: "run-one",
            toolName: "Read",
            toolUseId: "c1",
            toolInput: { path: "./a.ts" },
            result: "a",
          },
        ])}
      />,
    );

    const details = expandCard(container);
    expect(within(details).getAllByTestId("activity-row")).toHaveLength(1);

    // 收起卡片:过渡期间步骤仍挂载(收缩动画需要内容支撑),过渡结束才卸载。
    fireEvent.click(screen.getByRole("button", { expanded: true }));
    expect(details).toHaveAttribute("aria-hidden", "true");
    expect(within(details).getAllByTestId("activity-row")).toHaveLength(1);
    fireEvent.transitionEnd(details);
    expect(within(details).queryByTestId("activity-row")).toBeNull();
  });
});

// ─── 步骤区 = 转录里的同一个活动块(spec 决策 9 / 子代理 §1-2) ───────────────
// 子代理内部递归同一形态:同一个组头、同一套活动行、同一套展开体,不再套一层
// 带边框 + 状态胶囊的 step 卡(Problem 6 的卡片墙套娃)。组头汇总必须来自
// transcript-rows 的 summarizeActivity —— 自建一个子代理专用汇总会在这里显形。
describe("AgentSpawnCard steps region as an activity block", () => {
  const singleSpawn = (status = "completed") =>
    normalizedSpawnBlock({
      mode: "single",
      status,
      runs: [{ id: "run-one", index: 0, agent: "worker", status }],
    });

  const readSteps = (count: number) =>
    groupedChildren(
      Array.from({ length: count }, (_, i) => ({
        runId: "run-one",
        toolName: "Read",
        toolUseId: `call-${i}`,
        toolInput: { path: `./f${i}.ts` },
        result: "content",
      })),
    );

  it("Given a completed dispatch with child steps, When expanded, Then the STEPS region is one activity block carrying the shared group summary instead of per-step cards", () => {
    const { container } = render(
      <AgentSpawnCard
        toolBlock={singleSpawn()}
        childBlocks={groupedChildren([
          {
            runId: "run-one",
            toolName: "Read",
            toolUseId: "c1",
            toolInput: { path: "./a.ts" },
            result: "a",
          },
          {
            runId: "run-one",
            toolName: "Read",
            toolUseId: "c2",
            toolInput: { path: "./b.ts" },
            result: "b",
          },
          {
            runId: "run-one",
            toolName: "Bash",
            toolUseId: "c3",
            toolInput: { command: "go test ./..." },
            result: "ok",
          },
        ])}
      />,
    );

    const details = expandCard(container);
    const block = within(details).getByTestId("activity-block");
    expect(within(block).getByTestId("activity-header")).toHaveTextContent(
      "3 steps",
    );
    // 类目汇总的措辞与顺序由 summarizeActivity 独家决定(转录组头同一实现)。
    const summary = within(block).getByTestId("activity-summary");
    expect(summary).toHaveTextContent("Read 2");
    expect(summary).toHaveTextContent("Commands 1");
    expect(within(block).getAllByTestId("activity-row")).toHaveLength(3);
    // 旧的 step 卡(边框 + 每步一个状态胶囊)不复存在。
    expect(within(details).queryByTestId("agent-spawn-step-status")).toBeNull();
  });

  it("Given 20 child steps, When the card is expanded, Then the activity rows are expanded by default", () => {
    const { container } = render(
      <AgentSpawnCard toolBlock={singleSpawn()} childBlocks={readSteps(20)} />,
    );
    const details = expandCard(container);
    expect(within(details).getAllByTestId("activity-row")).toHaveLength(20);
  });

  it("Given more than 20 child steps, When the card is expanded, Then only the group header mounts until the group is opened", () => {
    const { container } = render(
      <AgentSpawnCard toolBlock={singleSpawn()} childBlocks={readSteps(21)} />,
    );
    const details = expandCard(container);
    expect(within(details).getByTestId("activity-header")).toHaveTextContent(
      "21 steps",
    );
    expect(within(details).queryAllByTestId("activity-row")).toHaveLength(0);

    fireEvent.click(within(details).getByTestId("activity-header"));
    expect(within(details).getAllByTestId("activity-row")).toHaveLength(21);
  });

  // ─── 运行态与转录活动块对齐(2026-08-17 增补)────────────────────────────
  // 步骤区的默认值原本只看「步数 ≤ 20」这个活算的 fallback:卡片在子代理跑着时
  // 被打开(当时 ≤20 步),流到第 21 步的瞬间 fallback 翻 false,整个步骤区当场
  // 自己收起 —— 观感上就是「有时候展开有时候收缩」。对齐后运行中交由 running
  // 接管(自动展开 + 超 8 步只留最后 6 行,与转录同款),落定才退回 ≤20 阈值。
  describe("steps region follows the transcript running behavior", () => {
    const renderWithProvider = (toolBlock: TranscriptBlock, count: number) =>
      render(
        <TranscriptUIStateProvider>
          <AgentSpawnCard
            toolBlock={toolBlock}
            childBlocks={readSteps(count)}
          />
        </TranscriptUIStateProvider>,
      );

    it("Given a running spawn opened at 10 steps, When the steps grow past 20 while running, Then the region stays expanded and elides to the last 6 rows", () => {
      // 生产里转录恒定包在 TranscriptUIStateProvider 内(chat.tsx),fallback
      // 因此是「活」的 —— 不包 provider 的测试走 useState 初值,复现不了翻转。
      const toolBlock = singleSpawn("running");
      const { rerender, container } = renderWithProvider(toolBlock, 10);
      const details = expandCard(container);
      // 运行态从一开始就走转录的省略规则:>8 步即省略,10 步 = 省略行 + 末 6 行。
      expect(within(details).getAllByTestId("activity-row")).toHaveLength(6);
      expect(within(details).getByTestId("activity-elided")).toHaveTextContent(
        "4 earlier steps",
      );

      rerender(
        <TranscriptUIStateProvider>
          <AgentSpawnCard toolBlock={toolBlock} childBlocks={readSteps(21)} />
        </TranscriptUIStateProvider>,
      );

      // 跨过 20 步不再当场收起;省略行随着步数长高,展开态保持。
      const header = within(details).getByTestId("activity-header");
      expect(header).toHaveAttribute("aria-expanded", "true");
      expect(within(details).getAllByTestId("activity-row")).toHaveLength(6);
      expect(within(details).getByTestId("activity-elided")).toHaveTextContent(
        "15 earlier steps",
      );
    });

    it("Given a running spawn with 21 steps, When it settles, Then the region returns to the over-20 collapsed default", () => {
      const { rerender, container } = renderWithProvider(
        singleSpawn("running"),
        21,
      );
      const details = expandCard(container);
      expect(within(details).getByTestId("activity-header")).toHaveAttribute(
        "aria-expanded",
        "true",
      );

      rerender(
        <TranscriptUIStateProvider>
          <AgentSpawnCard
            toolBlock={singleSpawn("completed")}
            childBlocks={readSteps(21)}
          />
        </TranscriptUIStateProvider>,
      );

      // 落定退回决策 9 的阈值:>20 只留组头;省略是运行态专属,一并消失。
      expect(within(details).getByTestId("activity-header")).toHaveAttribute(
        "aria-expanded",
        "false",
      );
      expect(within(details).queryByTestId("activity-elided")).toBeNull();
    });
  });

  it("Given a grouped spawn with a running 21-step run, When the card is expanded, Then that run's steps elide to the last 6 rows", () => {
    const block = normalizedSpawnBlock({
      mode: "parallel",
      status: "running",
      runs: [{ id: "run-one", index: 0, agent: "worker", status: "running" }],
    });
    const { container } = render(
      <AgentSpawnCard toolBlock={block} childBlocks={readSteps(21)} />,
    );
    const details = expandCard(container);
    const runGroup = within(details).getByTestId("agent-spawn-run-group");
    expect(within(runGroup).getAllByTestId("activity-row")).toHaveLength(6);
    expect(within(runGroup).getByTestId("activity-elided")).toHaveTextContent(
      "15 earlier steps",
    );
  });

  it("Given a grouped running spawn with unattributed child steps over the elide threshold, When expanded, Then the fallback STEPS region elides too", () => {
    const block = normalizedSpawnBlock({
      mode: "parallel",
      status: "running",
      runs: [{ id: "run-one", index: 0, agent: "worker", status: "running" }],
    });
    const { container } = render(
      <AgentSpawnCard
        toolBlock={block}
        childBlocks={groupedChildren(
          Array.from({ length: 10 }, (_, i) => ({
            // 不带 runId 的子块归入 fallback 步骤区。
            toolName: "Read",
            toolUseId: `loose-${i}`,
            toolInput: { path: `./f${i}.ts` },
            result: "content",
          })),
        )}
      />,
    );
    const details = expandCard(container);
    const fallback = within(details).getByTestId("agent-spawn-fallback-steps");
    expect(within(fallback).getAllByTestId("activity-row")).toHaveLength(6);
    expect(within(fallback).getByTestId("activity-elided")).toHaveTextContent(
      "4 earlier steps",
    );
  });

  it("Given a completed dispatch, When rendered, Then the header drops the green success pill for secondary steps · tokens · duration", () => {
    const block = {
      type: "tool_use",
      toolName: "Task",
      canonical: {
        kind: "agent.spawn",
        agentSpawn: { taskId: "1", taskDescription: "review PR" },
      },
      subagent: {
        status: "completed",
        toolUses: 5,
        totalTokens: 2400,
        durationMs: 12000,
      },
    } as unknown as TranscriptBlock;
    render(<AgentSpawnCard toolBlock={block} />);

    // 成功态不再有任何状态胶囊 —— 「没有标记 = 成功」(spec 决策 10)。
    expect(screen.queryByTestId("agent-spawn-status-pill")).toBeNull();
    expect(screen.queryByText(/DONE/)).toBeNull();
    const outcome = screen.getByTestId("agent-spawn-outcome");
    expect(outcome.textContent).toBe("5 steps · 2.4K tok · 12.0s");
    // 次级信息:不占用状态色,也不占用品牌色。
    expect(outcome.className).toContain("text-subtle-foreground");
  });

  it("Given any dispatch state, When rendered, Then the subagent name and icon are neutral rather than brand-colored and non-success keeps its pill", () => {
    const block = {
      type: "tool_use",
      toolName: "Task",
      canonical: {
        kind: "agent.spawn",
        agentSpawn: { taskId: "1", status: "running" },
      },
    } as unknown as TranscriptBlock;
    const { container } = render(<AgentSpawnCard toolBlock={block} />);
    const name = screen.getByText("Agent");
    expect(name.className).not.toContain("text-primary-text");
    const icon = container.querySelector(".lucide-users");
    expect(icon).not.toBeNull();
    expect(icon?.getAttribute("class")).not.toContain("text-primary-text");
    // 非成功态的标记不受影响。
    expect(screen.getByTestId("agent-spawn-status-pill")).toHaveTextContent(
      "RUNNING",
    );
    expect(screen.queryByTestId("agent-spawn-outcome")).toBeNull();
  });

  it("Given a completed grouped dispatch, When rendered, Then its card header follows the same success rule", () => {
    const block = normalizedSpawnBlock({
      status: "completed",
      runs: [
        { id: "a", index: 0, agent: "one", task: "One", status: "completed" },
        { id: "b", index: 1, agent: "two", task: "Two", status: "completed" },
      ],
    });
    const { container } = render(<AgentSpawnCard toolBlock={block} />);
    // 卡头的成功胶囊消失;run 分组内部每个 run 的状态胶囊照旧(那是 run 分组的一部分)。
    expect(screen.queryByTestId("agent-spawn-status-pill")).toBeNull();
    expect(
      container.querySelector(".lucide-users")?.getAttribute("class"),
    ).not.toContain("text-primary-text");
    const details = expandCard(container);
    expect(
      within(details).getAllByTestId("agent-spawn-run-group"),
    ).toHaveLength(2);
  });

  // 组头是折叠态唯一的信息出口。一个已经失败终结的 run 里,没配到 tool_result 的
  // 步骤在展开态是红色失败行(pendingOutcome=failed),组头必须报出同样的数量 ——
  // 否则 21 步以上默认折叠的那一组会宣称「一个失败都没有」。
  it("Given a failed run whose steps never got a result, When the step group stays collapsed, Then its header still reports them as failures", () => {
    const { container } = render(
      <AgentSpawnCard
        toolBlock={singleSpawn("failed")}
        childBlocks={groupedChildren(
          Array.from({ length: 21 }, (_, i) => ({
            runId: "run-one",
            toolName: "Read",
            toolUseId: `call-${i}`,
            toolInput: { path: `./f${i}.ts` },
            // 最后两步没有 result —— run 已经失败终结,它们永远等不到了。
            ...(i < 19 ? { result: "content" } : {}),
          })),
        )}
      />,
    );

    const details = expandCard(container);
    const header = within(details).getByTestId("activity-header");
    expect(header).toHaveTextContent("21 steps");
    expect(within(details).queryAllByTestId("activity-row")).toHaveLength(0);
    expect(within(details).getByTestId("activity-failures")).toHaveTextContent(
      "2 failed",
    );

    // 展开后确实是那两行红的 —— 组头与行是同一判据。
    fireEvent.click(header);
    expect(
      within(details)
        .getAllByTestId("activity-row")
        .filter((row) => row.getAttribute("data-failed") === "true"),
    ).toHaveLength(2);
  });

  it("Given the new header copy, Then both locales carry it", () => {
    expect(enCommon.canonical.agentSpawn.outcome.steps_one).toBeTruthy();
    expect(enCommon.canonical.agentSpawn.outcome.steps_other).toBeTruthy();
    expect(zhCommon.canonical.agentSpawn.outcome.steps).toBeTruthy();
  });
});

describe("Subagent step params section", () => {
  it("Given an expanded step with tool input, When expanded, Then its params are listed above the result", () => {
    const block = normalizedSpawnBlock({
      mode: "single",
      status: "completed",
      runs: [{ id: "run-one", index: 0, status: "completed" }],
    });
    const { container } = render(
      <AgentSpawnCard
        toolBlock={block}
        childBlocks={groupedChildren([
          {
            runId: "run-one",
            toolName: "Read",
            toolUseId: "read-1",
            toolInput: { file_path: "./a.ts", offset: 10 },
            result: "content",
          },
        ])}
      />,
    );
    const details = expandCard(container);
    fireEvent.click(within(details).getByRole("button", { name: /Read/i }));
    const body = within(details).getByTestId("activity-row-body");
    expect(within(body).getByText("file_path")).toBeInTheDocument();
    expect(within(body).getByText("./a.ts")).toBeInTheDocument();
    expect(within(body).getByText("offset")).toBeInTheDocument();
    expect(within(body).getByText("10")).toBeInTheDocument();
    // 参数在结果之前:展开体两段的次序不因换成活动行而颠倒。
    const sections = within(body).getAllByText(/PARAMS|RESULT/);
    expect(sections.map((node) => node.textContent)).toEqual([
      "PARAMS",
      "RESULT",
    ]);
  });

  it("Given a step with a long param value, When expanded, Then the value scrolls without an expand control", () => {
    const block = normalizedSpawnBlock({
      mode: "single",
      status: "completed",
      runs: [{ id: "run-one", index: 0, status: "completed" }],
    });
    const { container } = render(
      <AgentSpawnCard
        toolBlock={block}
        childBlocks={groupedChildren([
          {
            runId: "run-one",
            toolName: "Bash",
            toolUseId: "bash-1",
            toolInput: {
              command: "echo hi",
              note: "l1\nl2\nl3\nl4\nl5\nl6",
            },
            result: "ok",
          },
        ])}
      />,
    );
    const details = expandCard(container);
    fireEvent.click(within(details).getByRole("button", { name: /Bash/i }));
    expect(
      within(details).queryByRole("button", { name: "Expand all" }),
    ).toBeNull();
    expect(within(details).getByText(/l6/)).toHaveClass(
      "max-h-64",
      "overflow-auto",
    );
  });
});
