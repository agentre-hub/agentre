// transcript-rows: chat transcript 的「block → 渲染项 → 虚拟行」纯函数层,零 React 依赖。
// renderMessageBlocks 的配对状态机抽取到这里,让行级虚拟化(每个 RenderItem 一个
// 虚拟行)能在不碰 JSX 的前提下单测配对 / 合并 / skip / FIFO / 归集 / key 稳定性。
import {
  commandResultOf,
  isFailedCommandResult,
} from "./canonical-tool/command-result";
import type { AgentSpawnChildBlocks } from "./canonical-tool/props";
import { tier, toolCategory, type ToolCategory } from "./canonical-tool/tier";
import type { CanonicalDTO } from "./canonical-tool/types";
import type {
  TranscriptBlock,
  TranscriptLocalCommand,
  TranscriptMessage,
} from "./dto";

// isSubagentCanonical 替代旧 isSubagentTool(name) — name-based 检测改为读
// canonical.kind。translator 在 emit 时已经把 Task/Agent/collabAgent 工具识别成
// canonical.agentSpawn,这里直接 dispatch。
export function isSubagentCanonical(block: {
  canonical?: { kind?: string };
}): boolean {
  return block.canonical?.kind === "agent.spawn";
}

// isAskUserQuestionToolName 旧 tool-summary.ts 同名;此处过滤掉 AskUserQuestion 类工具的
// tool_use 块,避免与 ask_user_question 独立 block 渲染的 UserAskCard 重复出卡。
export function isAskUserQuestionToolName(
  toolName: string | undefined,
): boolean {
  if (!toolName) return false;
  const name = toolName.toLowerCase();
  return name === "askuserquestion" || name === "ask_user_question";
}

export function isRenderablePlanBlock(block: TranscriptBlock): boolean {
  const canonical = block.canonical;
  if (canonical?.kind !== "plan.update" || !canonical.planUpdate) return false;
  const actions = canonical.planUpdate.actions ?? [];
  if (actions.length > 0) return true;
  const text = canonical.planUpdate.text ?? block.text ?? "";
  const steps = canonical.planUpdate.steps ?? [];
  return text.trim().length > 0 && steps.length === 0;
}

type MutableAgentSpawnChildBlocks = Omit<AgentSpawnChildBlocks, "byRun"> & {
  byRun: Map<string, TranscriptBlock[]>;
};

export type RenderItem =
  // streaming=true 标记这是「流式途中正在生长」的文本项 —— 用 StreamingMarkdown
  // 增量渲染(已定稿 block memo 跳过、只重解析活跃尾巴);持久化文本仍走整段 MarkdownText。
  | { text: string; type: "text"; streaming?: boolean }
  | { block: TranscriptBlock; type: "plan" }
  | {
      block: TranscriptBlock;
      startedAt?: number;
      streaming: boolean;
      type: "thinking";
    }
  | {
      resultBlock?: TranscriptBlock;
      toolBlock?: TranscriptBlock;
      // childBlocks 仅 canonical.agent.spawn 需要(parent → run 归集),其它工具留空。
      childBlocks?: AgentSpawnChildBlocks;
      type: "tool";
    }
  | {
      block: TranscriptBlock;
      type: "image";
    }
  | {
      block: TranscriptBlock;
      // _consumed 标记此条审批已被 merge 到某条 tool_use 卡上,buildRenderItems 返回前
      // 会被过滤掉。未 resolved / resolved-denied 的审批不会被标记,保留为独立卡。
      _consumed?: boolean;
      type: "tool_permission_request";
    }
  | { block: TranscriptBlock; type: "tool_approval" }
  | { block: TranscriptBlock; type: "exec_approval" }
  | { block: TranscriptBlock; type: "unknown" }
  | { block: TranscriptBlock; type: "notice" }
  | { block: TranscriptBlock; type: "compact_boundary" };

// ─── 活动块 ─────────────────────────────────────────────────────────────────
// 一段连续的「思考 / 只读探查 / 中性 / 写 / 命令 / 失败」折成一个活动块:一个
// RenderItem = 一个 TranscriptRow = 一个虚拟行,折叠态只出组头,展开态出 steps。
// 正文与出组项(子代理 / 审批 / 提问 / 计划…)打断聚合,时间顺序原样保留。

/** 组头汇总的类目。思考不是工具调用,其余由 canonical-tool/tier 的 toolCategory 判定。 */
export type ActivityCategory = "thinking" | ToolCategory;

/** 组头汇总的固定输出顺序(位置可预期),失败计数不在其中——它不参与截断。 */
export const ACTIVITY_CATEGORY_ORDER: readonly ActivityCategory[] = [
  "thinking",
  "read",
  "edit",
  "write",
  "command",
  "other",
];

/** 组头最多列这么多类,再多的折成一个省略号(truncated)。 */
export const ACTIVITY_SUMMARY_MAX_PARTS = 4;

export type ActivitySummaryPart = {
  category: ActivityCategory;
  /** 本类目的步数。 */
  count: number;
  /** 仅 edit:本组改到的文件数(同一路径改多次算一个)。 */
  files?: number;
  /** 仅 edit:本组增行合计。 */
  plus?: number;
  /** 仅 edit:本组删行合计。 */
  minus?: number;
};

export type ActivitySummary = {
  /** 组内步数(= steps.length),组头的「N 步」。 */
  steps: number;
  /** 固定顺序、已截断到 ACTIVITY_SUMMARY_MAX_PARTS 的类目汇总。 */
  parts: ActivitySummaryPart[];
  /** 还有类目没进 parts —— 组头在末尾补省略号。 */
  truncated: boolean;
  /** 组内失败步数。永不参与截断:折叠态不得让发生过的事消失。 */
  failures: number;
};

/** 组内一步。形状与它单独成行时完全一致,展开体因此能复用同一套渲染。 */
export type ActivityStep = (
  | Extract<RenderItem, { type: "thinking" }>
  | Extract<RenderItem, { type: "tool" }>
) & { uiStateKey: string };

export type ActivityRenderItem = {
  steps: ActivityStep[];
  summary: ActivitySummary;
  type: "activity";
  /**
   * 这一段还会继续长:后面只跟着一段**还在流的思考**,而流式思考一落定就并回
   * 这个块。渲染层判「这一组此刻是否在跑」时必须把它算进去 —— 否则思考一开始流,
   * 块就不再是消息末行,整组被当成已落定而自动收起,思考结束又展开(来回抖动)。
   */
  growing?: boolean;
};

/** 渲染层拿到的活动块 —— 与其它渲染项一样带 uiStateKey(折叠态挂在它上面)。 */
export type VisibleActivityItem = ActivityRenderItem & { uiStateKey: string };

// VisibleRenderItem = 过滤掉已 merge 审批、并把连续可折叠项聚合成活动块之后的
// 渲染项 + 预计算的 uiStateKey。uiStateKey 进 TranscriptUIStateContext 持久化
// 折叠态,格式:message:${messageId}:${type}:${identity ?? visibleIdx}——聚合前
// 就已经算好,所以组内步骤的 key 与「它单独成行」时字节级一致(折叠态零迁移)。
export type VisibleRenderItem = (RenderItem | ActivityRenderItem) & {
  uiStateKey: string;
};

export type BuildRenderItemsArgs = {
  messageId: number;
  blocks?: TranscriptBlock[];
  /** 本轮仍在生长的尾巴文本,合并进末尾 text 项并标记 streaming。 */
  liveTail?: string;
  /** 流式中累积的 thinking 增量,合成一张排在 liveBlocks 之前的 thinking 卡。 */
  liveThinking?: string;
  liveThinkingStartedAt?: number | null;
  // 本轮 turn 已"冻结但还没持久化"的块(text / tool_use / tool_result),由
  // chat-streams-store 维护。和 persisted blocks 拼成一个完整顺序 —— 关键:
  // 流式途中遇到 tool_use 时,store 会把当下的 liveDelta 先冻成 text block 推
  // 到 liveBlocks 尾,所以真实顺序就是 [persisted..., ...liveBlocks, liveDelta]。
  liveBlocks?: TranscriptBlock[];
};

export function buildRenderItems({
  messageId,
  blocks = [],
  liveTail = "",
  liveThinking = "",
  liveThinkingStartedAt,
  liveBlocks = [],
}: BuildRenderItemsArgs): VisibleRenderItem[] {
  // 预扫一遍把 subagent 内部 block 先按外层 tool_use_id,再按 run id 归集;
  // 缺失 run id 的块仍保留在 all 中,主流程会 skip,由父卡作为 unmatched step 渲染。
  const childrenByParent = new Map<string, MutableAgentSpawnChildBlocks>();
  const collectChildren = (b: TranscriptBlock) => {
    if (!b.parentToolUseId) return;
    const grouped = childrenByParent.get(b.parentToolUseId) ?? {
      all: [],
      byRun: new Map<string, TranscriptBlock[]>(),
    };
    grouped.all.push(b);
    if (b.subagentRunId) {
      const runBlocks = grouped.byRun.get(b.subagentRunId) ?? [];
      runBlocks.push(b);
      grouped.byRun.set(b.subagentRunId, runBlocks);
    }
    childrenByParent.set(b.parentToolUseId, grouped);
  };
  blocks.forEach(collectChildren);
  liveBlocks.forEach(collectChildren);

  const items: RenderItem[] = [];
  const pendingToolIndexes = new Map<string, number>();
  const pendingAnonymousToolIndexes: number[] = [];
  // SKIPPED_TOOL_INDEX 给 AskUserQuestion 的 tool_use 占位用:tool_use 本身不入 items,
  // 但要让后续的 tool_result 在 pendingToolIndexes 里查到这个哨兵,从而一同 skip。
  const SKIPPED_TOOL_INDEX = -1;
  // pendingPermsByTool 按 toolName 维护"已审批通过、还在等匹配 tool_use"的 perm RenderItem
  // 下标 (FIFO)。匹配到 tool_use 时把 perm 标记 _consumed,merge 到那条 tool item。
  // 这是协议上唯一可行的关联方式 —— ChatBlockToolPermission 没有 toolUseId 字段,
  // can_use_tool control_request 也不携带未来的 tool_use_id。
  const pendingPermsByTool = new Map<string, number[]>();

  function appendText(text: string, streaming = false) {
    if (!text) return;
    const last = items.at(-1);
    if (last?.type === "text") {
      last.text += text;
      // 与前一个已冻结的 text 段合并后,整段都按流式尾巴处理 ——
      // StreamingMarkdown 会把已冻结的前缀也切成 memo 命中的定稿块,只重解析真尾巴。
      if (streaming) last.streaming = true;
      return;
    }
    items.push({ text, type: "text", streaming });
  }

  const consumeBlock = (b: TranscriptBlock) => {
    // subagent 内部 block 已经被归集到父 AgentSpawnCard 的 childBlocks,不再同级渲染。
    if (b.parentToolUseId) return;
    switch (b.type) {
      case "text":
        appendText(b.text ?? "");
        break;
      case "thinking":
        items.push({ block: b, streaming: false, type: "thinking" });
        break;
      case "image":
        items.push({ block: b, type: "image" });
        break;
      case "plan":
        // Most plan.update blocks are progress data for TaskProgressBar only.
        // Actionable plan blocks carry canonical.actions and need the shared
        // PlanCard in the transcript.
        if (isRenderablePlanBlock(b)) {
          items.push({ block: b, type: "plan" });
        }
        break;
      case "tool_use": {
        // AskUserQuestion 类工具的 tool_use 不渲染独立卡 —— ask_user_question block
        // 已经把交互界面接管掉。占位 SKIPPED_TOOL_INDEX 让后续 tool_result 也 skip。
        if (isAskUserQuestionToolName(b.toolName)) {
          if (b.toolUseId)
            pendingToolIndexes.set(b.toolUseId, SKIPPED_TOOL_INDEX);
          break;
        }
        // ExitPlanMode 同理 —— PlanApproveCard(plan.approve_request canonical)已经
        // 承担"批准执行计划"的完整渲染,后续 CLI 真正调用 ExitPlanMode 冒出的 tool_use
        // 是协议余响,再渲染一张卡只会和 PlanApproveCard 视觉重复。break 前不入
        // pendingPermsByTool 队列也意味着 PlanApproveCard 不会被 merge 隐藏。
        if (b.toolName === "ExitPlanMode") {
          if (b.toolUseId)
            pendingToolIndexes.set(b.toolUseId, SKIPPED_TOOL_INDEX);
          break;
        }
        if (isSubagentCanonical(b)) {
          // canonical.agent.spawn — 走 CanonicalToolRouter → AgentSpawnCard,childBlocks
          // 由 parent-child 归集传过去(AgentSpawnCard 内部渲染 STEPS 段)。
          const item: RenderItem = {
            childBlocks: b.toolUseId
              ? (childrenByParent.get(b.toolUseId) ?? {
                  all: [],
                  byRun: new Map(),
                })
              : { all: [], byRun: new Map() },
            toolBlock: b,
            type: "tool",
          };
          items.push(item);
          if (b.toolUseId) {
            pendingToolIndexes.set(b.toolUseId, items.length - 1);
          }
          break;
        }
        const item: RenderItem = { toolBlock: b, type: "tool" };
        // 配对消费一条审批 RenderItem —— 找最早未消费且同 toolName 的 allowed 审批。
        // 只是把那条审批标成已消费(它不再单独出现在转录里);审批本身的信息由
        // 工具块上的 toolPermission 承载,不需要再挂一份到 RenderItem 上。
        if (b.toolName) {
          const queue = pendingPermsByTool.get(b.toolName);
          if (queue && queue.length > 0) {
            const permIdx = queue.shift()!;
            const permItem = items[permIdx];
            if (permItem?.type === "tool_permission_request") {
              permItem._consumed = true;
            }
          }
        }
        items.push(item);
        if (b.toolUseId) {
          pendingToolIndexes.set(b.toolUseId, items.length - 1);
        } else {
          pendingAnonymousToolIndexes.push(items.length - 1);
        }
        break;
      }
      case "tool_result": {
        const toolIndex = b.toolUseId
          ? pendingToolIndexes.get(b.toolUseId)
          : pendingAnonymousToolIndexes.pop();
        // AskUserQuestion 的 tool_result 命中 SKIPPED_TOOL_INDEX 哨兵,直接丢弃。
        if (toolIndex === SKIPPED_TOOL_INDEX) {
          if (b.toolUseId) pendingToolIndexes.delete(b.toolUseId);
          break;
        }
        const item =
          typeof toolIndex === "number" ? items[toolIndex] : undefined;

        if (item?.type === "tool") {
          item.resultBlock = b;
          if (b.toolUseId) pendingToolIndexes.delete(b.toolUseId);
        }
        // 孤儿 tool_result:没有配对 tool_use(AskUserQuestion 历史数据 / 后端漏过滤
        // 的 PostToolUse 等都会走到这里),直接丢,不要 push 一条没有 toolBlock 的
        // 幽灵 tool 卡(toolName 会回退到默认 "tool" 把答案文本暴露出来)。
        break;
      }
      case "ask_user_question":
        // ask_user_question 走 CanonicalToolRouter — block.canonical (UserAsk)
        // 已由后端 live + replay 双路径填好,UserAskCard 直接消费。
        items.push({ toolBlock: b, type: "tool" });
        break;
      case "tool_permission_request": {
        // tool_permission_request 渲染走 CanonicalToolRouter —— ExitPlanMode
        // → canonical.plan.approve_request → PlanApproveCard;其它工具
        // → canonical.tool.permission → ToolPermissionCard。两条 canonical 都由后端
        // dispatcher_emitter + replay 填好。RenderItem.type 保留 "tool_permission_request"
        // 让 merge 到下方同 toolName tool_use 卡的逻辑可识别。
        items.push({ block: b, type: "tool_permission_request" });
        const idx = items.length - 1;
        const perm = b.toolPermission;
        // 只有 resolved + allowed 才参与 merge:未决态用户还要操作、denied 没有下游 tool_use。
        if (perm?.resolved && perm.allowed && perm.toolName) {
          const queue = pendingPermsByTool.get(perm.toolName) ?? [];
          queue.push(idx);
          pendingPermsByTool.set(perm.toolName, queue);
        }
        break;
      }
      case "tool_approval":
        // 内置写工具审批卡:不走 CanonicalToolRouter,直接按 block.type 路由到
        // ToolApprovalCard(transcript-row-view)。持久化/overlay 与 live 两路都到这里 ——
        // block.toolApproval.status 自身就是 truth(后端 finalize 已把悬空 pending 落成
        // expired),前端不按会话活跃度推断。
        items.push({ block: b, type: "tool_approval" });
        break;
      case "exec_approval":
        // Gateway approval resolution is deliberately not paired with a
        // tool_result: approval terminal and command execution terminal are
        // separate protocol lifecycles.
        items.push({ block: b, type: "exec_approval" });
        break;
      case "compact_boundary":
        // CLI 通报上下文已压缩 (manual /compact 或 auto)。在 transcript 中嵌一条
        // 分隔卡片;最后一条 compact_boundary 之前的所有内容会被 ChatTranscript 顶层
        // 折叠成"查看历史"按钮。
        items.push({ block: b, type: "compact_boundary" });
        break;
      case "notice":
        // 持久 notice（含供应商回退等）走 transcript-row-view 的 notice 分支：
        // 结构化 providerKey 由后端投影填充（走 t() 文案）；旧数据/其它来源的
        // 非结构化 notice 同分支回退到 Text 原样渲染。
        items.push({ block: b, type: "notice" });
        break;
      default:
        items.push({ block: b, type: "unknown" });
        break;
    }
  };
  blocks.forEach(consumeBlock);

  // 合成 thinking 排在本轮 liveBlocks(含已冻结的 thinking/text/tool)之后 ——
  // store 在 tool_use/plan/ask 等边界把上一段 liveThinking 冻进 liveBlocks,
  // 所以 liveBlocks 里已经按真实时间顺序含了前几轮的思考;这里只剩当前轮还没
  // 冻结的 liveThinking(thinking→text 尚未遇到下一个边界),排在末尾的 text 前。
  // 摆错位置会出现「第 2 轮思考压在第 1 轮工具卡上方」的视觉错乱。
  // streaming 判定:当前轮一旦冒出非思考输出(text 开始流到 liveTail),思考就
  // 结束。liveBlocks 里有前几轮的工具不意味着本轮思考已结束,不能再用它判断。
  liveBlocks.forEach(consumeBlock);
  if (liveThinking) {
    items.push({
      block: { text: liveThinking, type: "thinking" } as TranscriptBlock,
      startedAt: liveThinkingStartedAt ?? undefined,
      streaming: !liveTail,
      type: "thinking",
    });
  }
  // liveTail 是本轮仍在生长的尾巴文本 —— 标记 streaming,走 StreamingMarkdown 增量渲染。
  appendText(liveTail, true);

  // 被 merge 到下方 tool_use 卡的审批 RenderItem 不再独立渲染。
  const visible: (RenderItem & { uiStateKey: string })[] = items
    .filter(
      (item) => !(item.type === "tool_permission_request" && item._consumed),
    )
    .map((item, idx) =>
      Object.assign(item, {
        uiStateKey: itemUIStateKey(messageId, item, idx),
      }),
    );
  return markGrowingActivity(groupActivityItems(messageId, visible));
}

// markGrowingActivity —— 末尾那段还在流的思考不进组(它是那一刻唯一承载 live tail
// 的表面),于是它会短暂地排在活动块**后面**;等它落定,isFoldableStep 就放行,
// 它并回同一个块。把这段临时错位标在活动块上,渲染层才不会据「后面还有一行」
// 断定这一组已经落定。被别的出组项(子代理 / 审批 / 正文)隔开时不标 —— 那种情况
// 下这一段是真的结束了,不会再有东西并回来。
function markGrowingActivity(items: VisibleRenderItem[]): VisibleRenderItem[] {
  const last = items.at(-1);
  if (last?.type !== "thinking" || !last.streaming) return items;
  const prev = items.at(-2);
  if (prev?.type === "activity") prev.growing = true;
  return items;
}

// isFoldableStep:这一项能不能折进活动块。
//   - 思考进组(extended thinking 下 think/tool 高频交替,让思考打断聚合等于不聚合),
//     但**正在流的思考不进组** —— 它是那一刻唯一承载 live tail 的表面。
//   - 工具按 tier 判档:出组档(子代理 / 提问 / 计划审批 / 工具权限)永不进组。
//   - tool_use 之外的 block(ask_user_question 等)即使 canonical 缺失也不进组 ——
//     阻塞用户的卡片折叠等于把会话卡死,不能依赖 canonical 是否齐全。
//   - 其余项(正文 / 计划 / 审批 / 图片 / notice / compact / unknown)一律打断聚合。
function isFoldableStep(
  item: RenderItem & { uiStateKey: string },
): item is ActivityStep {
  if (item.type === "thinking") return !item.streaming;
  if (item.type !== "tool") return false;
  const block = item.toolBlock;
  if (!block || block.type !== "tool_use") return false;
  return tier(block) !== "out";
}

// groupActivityItems 线性扫一遍可见项:连续可折叠项攒成一段,遇到别的项就先结束
// 当前段再原样输出那一项 —— 顺序与输入完全一致,任何情况下都不跨越出组项合并。
//
// 一步的段也发活动项,不退回原来那张整卡:一条 assistant 消息只由「正文 / 活动块
// / 出组卡片 / 脚注」四种东西组成,落单的一次调用同样是活动行。「单条不成组」
// (不套「1 步」的壳)是渲染层的事 —— ActivityBlock 见到一步就只出那一行。行模型
// 保持单一形态还顺带保住了 key 稳定性:流式里一段从 1 步长到 N 步时行 key 不变,
// 不会整行 remount。
function groupActivityItems(
  messageId: number,
  items: (RenderItem & { uiStateKey: string })[],
): VisibleRenderItem[] {
  const out: VisibleRenderItem[] = [];
  let run: ActivityStep[] = [];
  let runStartIdx = 0;

  const flush = () => {
    if (run.length === 0) return;
    out.push(makeActivityItem(messageId, run, runStartIdx));
    run = [];
  };

  items.forEach((item, idx) => {
    if (isFoldableStep(item)) {
      if (run.length === 0) runStartIdx = idx;
      run.push(item);
      return;
    }
    flush();
    out.push(item);
  });
  flush();
  return out;
}

// 活动块的 identity 取**首步**的身份(缺身份时回退首步的可见下标)。首步在流式
// 生长中不变,组头 key 因此不随新步骤追加而漂移 —— 用户展开的块不会在轮次中途
// 因为 key 变了而重新折叠。
function makeActivityItem(
  messageId: number,
  steps: ActivityStep[],
  runStartIdx: number,
): VisibleActivityItem {
  const first = steps[0];
  const firstBlock = first.type === "tool" ? first.toolBlock : first.block;
  const identity = stableBlockIdentity(firstBlock) ?? runStartIdx;
  return {
    steps,
    summary: summarizeActivity(steps),
    type: "activity",
    uiStateKey: `message:${messageId}:activity:${identity}`,
  };
}

// isFailedStep —— 「这一步失败了没有」的唯一判据,组头失败计数与活动行的红色标记
// 共用它。没有结果的一步只有在调用方声明「这一轮已经以失败终结」(unresolvedFailed)
// 时才算失败 —— 转录里的一轮默认还在跑,没结果 ≠ 失败。
// 两处各写一遍必然漂移:组头会宣称零失败,而展开后是几行红的。
//
// 有结果时 isError 只是失败信号之一:命令类工具把退出码 / status 写在结果 JSON 里,
// 一条 exit 1 的命令 isError 是 false(见 command-result 的 isFailedCommandResult)。
// RawToolCard 一直是按这两条一起判的,活动块只认 isError 就等于把它降级了。
export function isFailedStep(
  step: ActivityStep,
  unresolvedFailed = false,
): boolean {
  if (step.type === "thinking") return false;
  const result = step.resultBlock;
  if (!result) return unresolvedFailed;
  return !!result.isError || isFailedCommandResult(commandResultOf(result));
}

// summarizeActivity 汇总组头:类目计数按固定顺序输出并截断,写操作额外报出对象
// 规模(改到几个文件 / 增删多少行),失败计数单列且不参与截断。
// unresolvedFailed 透传给 isFailedStep —— 见那里的注释。
export function summarizeActivity(
  steps: ActivityStep[],
  unresolvedFailed = false,
): ActivitySummary {
  const counts = new Map<ActivityCategory, number>();
  const editedPaths = new Set<string>();
  let plus = 0;
  let minus = 0;
  let failures = 0;

  for (const step of steps) {
    if (step.type === "thinking") {
      counts.set("thinking", (counts.get("thinking") ?? 0) + 1);
      continue;
    }
    const block = step.toolBlock;
    if (!block) continue;
    const category = toolCategory(block);
    counts.set(category, (counts.get(category) ?? 0) + 1);
    for (const patch of fileEditPatches(block)) {
      editedPaths.add(patch.path);
      plus += patch.plus ?? 0;
      minus += patch.minus ?? 0;
    }
    if (isFailedStep(step, unresolvedFailed)) failures++;
  }

  const all: ActivitySummaryPart[] = ACTIVITY_CATEGORY_ORDER.filter((c) =>
    counts.has(c),
  ).map((category) => {
    const count = counts.get(category) ?? 0;
    return category === "edit"
      ? { category, count, files: editedPaths.size, minus, plus }
      : { category, count };
  });

  return {
    failures,
    parts: all.slice(0, ACTIVITY_SUMMARY_MAX_PARTS),
    steps: steps.length,
    truncated: all.length > ACTIVITY_SUMMARY_MAX_PARTS,
  };
}

// fileEditPatches:后端 translator 已把每次编辑算成按文件的 patch(路径 + 增删行),
// 组头的「改 N 个文件 +P −M」直接汇总它,不在前端重算 diff。
function fileEditPatches(block: TranscriptBlock) {
  const canonical = (block as { canonical?: CanonicalDTO }).canonical;
  if (canonical?.kind !== "file.edit") return [];
  return canonical.fileEdit?.files ?? [];
}

// itemUIStateKey 的 type 段沿用旧 renderMessageBlocks 渲染层的命名
// (tool_permission_request 项历史上写作 "permission"),identity 优先块身份、
// 回退 visible 下标 —— 与旧实现字节级一致,卡片折叠态零迁移。
function itemUIStateKey(
  messageId: number,
  item: RenderItem,
  visibleIdx: number,
): string {
  const type =
    item.type === "tool_permission_request" ? "permission" : item.type;
  const block =
    item.type === "tool"
      ? item.toolBlock
      : item.type === "text"
        ? undefined
        : item.block;
  const identity = stableBlockIdentity(block) ?? visibleIdx;
  return `message:${messageId}:${type}:${identity}`;
}

// ─── 行模型 ──────────────────────────────────────────────────────────────────
// 一行 = 一个 RenderItem + 消息分片标志。chrome(头像/名字/时间戳/AutoTriggerBanner
// /meta footer/indicator/error)由渲染层按 isFirstOfMessage/isLastOfMessage 挂在
// 行内,不单独成行 —— 纯文本消息恰好一行,DOM 形态与 message 级虚拟化几乎一致。

// placeholder:blocks 为空(占位 assistant)或全部被 skip 的消息仍要渲染消息 chrome
// (头像行 + typing indicator 落点),发射一个无内容行。
export type TranscriptRowItem =
  | VisibleRenderItem
  | { type: "placeholder"; uiStateKey?: undefined }
  // local_command:!command 起的临时本地命令条目(F4 store),按 createdAt 归并
  // 进 transcript,渲染成 LocalCommandCard。无 message 引用(messageId 哨兵 -1)。
  | {
      type: "local_command";
      entry: TranscriptLocalCommand;
      uiStateKey?: undefined;
    };

export type TranscriptRow = {
  /** 虚拟器 getItemKey + 测量缓存键。复用 uiStateKey(含 messageId,item 级唯一)。 */
  key: string;
  messageId: number;
  /**
   * 行渲染需要的消息引用(角色/时间戳/meta tokens/errorText)。local_command 行
   * 无对应消息,字段缺省 —— 渲染层在读取前已按 item.type 提前返回。
   */
  message?: TranscriptMessage;
  /**
   * R17:非本机发出的用户消息的来源设备标识。仅当调用方在 sourceByMessageId
   * 里给这条用户消息提供了来源时才有值(本机发出的恒为 undefined —— 呈现与
   * 今天完全一致)。是设备属性,不是会话事件,渲染层把它放在角色标签之后。
   */
  sourceDevice?: string;
  item: TranscriptRowItem;
  /** 首行渲染头像 + 名字 + 时间戳(以及 autonomous banner)。 */
  isFirstOfMessage: boolean;
  /** 末行渲染 footer(meta/copy/edit)+ RetryNotice + TypingIndicator + ErrorCard。 */
  isLastOfMessage: boolean;
  autonomous: boolean;
};

export type TranscriptRowsResult = {
  rows: TranscriptRow[];
  /** messageId → 该消息首行下标;scrollToMessage / anchor 回退用。 */
  firstRowIndexByMessageId: Map<number, number>;
  /** 行 key → 下标;行级 anchor 精确恢复用。 */
  rowIndexByKey: Map<string, number>;
};

/**
 * R17:从消息数组里收集「非本机发出的用户消息 → 来源设备显示名」,交给
 * buildTranscriptRows 的 sourceByMessageId。
 *
 * 规则:角色必须是 user、消息上带了 sourceDevice(daemon 盖的提交方指纹)、且它 !=
 * 本机指纹 —— 三者都满足才进表。本机发出的消息 sourceDevice 恒等于本机指纹,被跳过,
 * 输出与不传逐项一致(单客户端界面零变化);localFingerprint 未就绪时保守输出空表。
 *
 * 显示名优先取 daemon 上报的设备名(sourceDeviceName);没名字就回退到指纹本身 ——
 * 它仍是这台设备的标识。
 */
export function buildSourceByMessageId(
  messages: readonly TranscriptMessage[],
  localFingerprint: string | undefined,
): Map<number, string> {
  const out = new Map<number, string>();
  if (!localFingerprint) return out;
  for (const m of messages) {
    if (m.role !== "user") continue;
    const dev = m.sourceDevice;
    if (!dev || dev === localFingerprint) continue;
    out.set(m.id, m.sourceDeviceName || dev);
  }
  return out;
}

export type BuildTranscriptRowsArgs = {
  displayMessages: TranscriptMessage[];
  autonomousIds: ReadonlySet<number>;
  /**
   * 当前会话的本地命令条目(F4 store,listForSession 已按 createdAt 升序)。
   * 按 createdAt 归并进消息行之间,不重排消息;空/缺省时输出与不传逐项一致。
   */
  localCommands?: TranscriptLocalCommand[];
  /**
   * 各 assistant 消息各自的流式内容,按 messageId 索引。
   *
   * 一个会话可同时有多条流在跑(用户轮 / 后台任务完成的自主续轮 / 后台 subagent
   * 活动轮),它们绑在不同的 assistant 消息上,所以这里必须是**表**而不是单个
   * liveTargetId —— 早先的单目标契约下,后开的那条流会让先开那条的消息瞬间掉回
   * 持久化态(用户可见症状:「已输出内容清空回退」,sess-1950)。
   *
   * 表里有 key 的消息即为 live:每 chunk 现场重建行,绕过 cache。
   */
  liveByMessageId?: ReadonlyMap<number, LiveRowContent>;
  /**
   * 实例级行缓存(WeakMap,键是消息对象)。persisted 消息的 blocks 引用稳定 →
   * 缓存命中返回同一 row 对象数组 → 行组件 React.memo 恒命中;reload 换对象引用
   * 自然失效。live 消息(liveByMessageId 里有 key 的)每 chunk 内容都在变,
   * 绕过缓存现场重建。
   */
  cache?: WeakMap<TranscriptMessage, TranscriptRow[]>;
  /**
   * R17:用户消息 id → 来源设备标识。只在多客户端、消息确实由他端发出时才有
   * 条目;本机发出的消息不在表里,输出与不传逐项一致(单客户端界面零变化)。
   */
  sourceByMessageId?: ReadonlyMap<number, string>;
};

/** LiveRowContent 是一条 assistant 消息此刻的流式内容(尚未落库的部分)。 */
export type LiveRowContent = {
  liveTail?: string;
  liveThinking?: string;
  liveThinkingStartedAt?: number | null;
  liveBlocks?: TranscriptBlock[];
};

function buildMessageRows(
  m: TranscriptMessage,
  autonomous: boolean,
  sourceDevice: string | undefined,
  live?: {
    liveTail?: string;
    liveThinking?: string;
    liveThinkingStartedAt?: number | null;
    liveBlocks?: TranscriptBlock[];
  },
): TranscriptRow[] {
  const items = buildRenderItems({
    messageId: m.id,
    blocks: m.blocks ?? undefined,
    liveTail: live?.liveTail,
    liveThinking: live?.liveThinking,
    liveThinkingStartedAt: live?.liveThinkingStartedAt,
    liveBlocks: live?.liveBlocks,
  });
  if (items.length === 0) {
    return [
      {
        autonomous,
        isFirstOfMessage: true,
        isLastOfMessage: true,
        item: { type: "placeholder" },
        key: `message:${m.id}:placeholder`,
        message: m,
        messageId: m.id,
        sourceDevice,
      },
    ];
  }
  return items.map((item, idx) => ({
    autonomous,
    isFirstOfMessage: idx === 0,
    isLastOfMessage: idx === items.length - 1,
    item,
    // uiStateKey 含 messageId 且在消息内 item 级唯一(identity 或 visible 下标),
    // 直接复用为行 key —— 它在「流式形态 → 落库形态」之间逐项相等(同一套
    // buildRenderItems 输入序列),turn 落定不会整列 remount。
    key: item.uiStateKey,
    message: m,
    messageId: m.id,
    sourceDevice,
  }));
}

function localCommandRow(entry: TranscriptLocalCommand): TranscriptRow {
  return {
    autonomous: false,
    isFirstOfMessage: true,
    isLastOfMessage: true,
    item: { entry, type: "local_command" },
    key: `localcmd:${entry.id}`,
    // 哨兵 -1:无对应消息;不进 firstRowIndexByMessageId,渲染层提前返回不读 message。
    messageId: -1,
  };
}

/** SettledTranscriptRows = 不含任何 live 内容的完整重建结果 + 行组边界。
 * 它在 ChatTranscript 里只依赖 messages(流式中稳定),可整体 memoize。 */
export type SettledTranscriptRows = TranscriptRowsResult & {
  /** messageId → 该消息行组在 rows 里的 [start, length)。live overlay 据此 splice。 */
  groupByMessageId: ReadonlyMap<number, { start: number; length: number }>;
  /** messageId → 消息对象。overlay 重建 live 行时按 id 取对象,避免每 chunk 全表扫描。 */
  messageByMessageId: ReadonlyMap<number, TranscriptMessage>;
};

/**
 * buildRows 是三层公共 API 共享的核心:带/不带 liveByMessageId 的完整重建,
 * 顺带记录行组边界(groupByMessageId)与消息索引(messageByMessageId)。
 * 带 live 时 group 长度反映 live 行(调用方不应依赖 —— 只有 settled 版本对外暴露)。
 */
function buildRows(args: BuildTranscriptRowsArgs): SettledTranscriptRows {
  const {
    displayMessages,
    autonomousIds,
    localCommands,
    liveByMessageId,
    cache,
    sourceByMessageId,
  } = args;
  // 阶段一:按 displayMessages 顺序产出各消息的行组(缓存/live 逻辑与历史一致),
  // 同时记下每组的 createtime,供阶段二归并 —— 消息组顺序原样保留,无重排可能。
  const messageGroups: { rows: TranscriptRow[]; createtime: number }[] = [];
  const messageByMessageId = new Map<number, TranscriptMessage>();
  for (const m of displayMessages) {
    messageByMessageId.set(m.id, m);
  }
  for (const m of displayMessages) {
    const autonomous = autonomousIds.has(m.id);
    // R17:来源标识只属于「非本机发出的用户消息」—— 其它角色恒 undefined。
    const sourceDevice =
      m.role === "user" ? sourceByMessageId?.get(m.id) : undefined;
    const live = liveByMessageId?.get(m.id);
    let messageRows: TranscriptRow[];
    if (live) {
      // live 消息每 chunk 重建,不读不写缓存。
      messageRows = buildMessageRows(m, autonomous, sourceDevice, live);
    } else {
      const cached = cache?.get(m);
      // autonomous 取决于前一条消息,与消息对象自身无关 —— 缓存命中但标志变了
      // (极端:上游裁剪了前面的消息而对象引用未变)就重建,避免 banner 错挂。
      // 来源标识同理:缓存命中但来源变了/新增了就重建,避免旧来源滞留在缓存里。
      if (
        cached &&
        cached[0]?.autonomous === autonomous &&
        cached[0]?.sourceDevice === sourceDevice
      ) {
        messageRows = cached;
      } else {
        messageRows = buildMessageRows(m, autonomous, sourceDevice);
        cache?.set(m, messageRows);
      }
    }
    messageGroups.push({ createtime: m.createtime, rows: messageRows });
  }

  // 阶段二:把本地命令按 createdAt 归并进消息组之间 —— 绝不重排消息。每条命令的
  // insertionIndex = createtime <= entry.createdAt 的消息组数量;按 index 归桶,
  // 扁平化时在第 i 组之前先发该桶(同桶按 createdAt 升序),收尾发末尾桶。
  const sortedCommands = (localCommands ?? [])
    .slice()
    .sort((a, b) => a.createdAt - b.createdAt);
  const commandsByInsertion = new Map<number, TranscriptLocalCommand[]>();
  for (const entry of sortedCommands) {
    let insertionIndex = 0;
    while (
      insertionIndex < messageGroups.length &&
      messageGroups[insertionIndex].createtime <= entry.createdAt
    ) {
      insertionIndex++;
    }
    const bucket = commandsByInsertion.get(insertionIndex) ?? [];
    bucket.push(entry);
    commandsByInsertion.set(insertionIndex, bucket);
  }

  const rows: TranscriptRow[] = [];
  const firstRowIndexByMessageId = new Map<number, number>();
  const groupByMessageId = new Map<number, { start: number; length: number }>();
  const rowIndexByKey = new Map<string, number>();
  const pushRow = (row: TranscriptRow) => {
    rowIndexByKey.set(row.key, rows.length);
    rows.push(row);
  };
  const flushCommands = (insertionIndex: number) => {
    for (const entry of commandsByInsertion.get(insertionIndex) ?? []) {
      pushRow(localCommandRow(entry));
    }
  };

  for (const [groupIdx, group] of messageGroups.entries()) {
    flushCommands(groupIdx);
    // 消息组首行下标对准 firstRowIndexByMessageId(只收真实消息行,messageId >= 0),
    // 同时记下行组边界供 live overlay splice。
    const firstRow = group.rows[0];
    if (firstRow && firstRow.messageId >= 0) {
      firstRowIndexByMessageId.set(firstRow.messageId, rows.length);
      groupByMessageId.set(firstRow.messageId, {
        start: rows.length,
        length: group.rows.length,
      });
    }
    for (const row of group.rows) {
      pushRow(row);
    }
  }
  // 末尾桶:晚于所有消息的命令。
  flushCommands(messageGroups.length);

  return {
    firstRowIndexByMessageId,
    groupByMessageId,
    messageByMessageId,
    rowIndexByKey,
    rows,
  };
}

/**
 * buildSettledTranscriptRows:不含 live 内容的完整重建。rows / 两张索引图 / 行组边界
 * 只依赖 displayMessages + autonomousIds + localCommands + sourceByMessageId + cache,
 * 在流式中全部引用稳定 —— ChatTranscript 把它 memoize 在 messages 上,每 chunk 不再重建。
 */
export function buildSettledTranscriptRows(
  args: Omit<BuildTranscriptRowsArgs, "liveByMessageId">,
): SettledTranscriptRows {
  return buildRows({ ...args, liveByMessageId: undefined });
}

/**
 * applyLiveTranscriptRows:在已 memoize 的 settled 之上叠加 live 内容。
 *
 * 快速路径(live 消息是 rows 的尾部连续后缀 —— 流式常态):只重建 live 消息的行组,
 * 其余行与 settled 共享同一引用(行组件 memo 恒命中),firstRowIndexByMessageId 原样
 * 复用、rowIndexByKey 只做 O(live) 的增量替换 —— 不再每 chunk O(全量) 重建两张索引图。
 *
 * 不满足快速路径条件(live 在中间 / 尾部还有本地命令 / live id 不在消息表里)时回退
 * 到 buildRows 完整重建,输出与历史逐项一致。
 */
export function applyLiveTranscriptRows(
  settled: SettledTranscriptRows,
  args: BuildTranscriptRowsArgs,
): TranscriptRowsResult {
  const { liveByMessageId } = args;
  if (!liveByMessageId || liveByMessageId.size === 0) {
    // 无 live:零拷贝返回 settled(messages 不变时引用稳定 → 下游 useMemo/effect 不触发)。
    return {
      firstRowIndexByMessageId: settled.firstRowIndexByMessageId,
      rowIndexByKey: settled.rowIndexByKey,
      rows: settled.rows,
    };
  }

  const liveGroups: {
    content: LiveRowContent;
    length: number;
    m: TranscriptMessage;
    start: number;
  }[] = [];
  for (const [messageId, content] of liveByMessageId) {
    const m = settled.messageByMessageId.get(messageId);
    const g = settled.groupByMessageId.get(messageId);
    if (!m || !g) return buildRows({ ...args, liveByMessageId }); // live id 异常 → 回退
    liveGroups.push({ content, length: g.length, m, start: g.start });
  }
  // 按消息顺序(settled 行序)。
  liveGroups.sort((a, b) => a.start - b.start);

  // 快速路径条件:live 组构成 settled.rows 的尾部连续后缀(中间无其它消息、无尾部命令)。
  // 第一个 live 组前面可以有非 live 前缀(它在 rows 里任意位置),只要各组相邻、
  // 且最后一组的尾部正好落在 settled.rows.length 上。
  let ok = liveGroups.length > 0;
  let prevEnd = liveGroups[0]?.start ?? 0;
  for (const g of liveGroups) {
    if (g.start !== prevEnd) {
      ok = false;
      break;
    }
    prevEnd = g.start + g.length;
  }
  if (!ok || prevEnd !== settled.rows.length) {
    return buildRows({ ...args, liveByMessageId }); // 尾部有命令 / live 在中间 → 回退
  }

  const prefixLen = liveGroups[0].start;
  // 只重建 live 消息的行组(live 内容每 chunk 都在变,必须现场重建)。
  const liveRows: TranscriptRow[] = [];
  for (const { content, m } of liveGroups) {
    const autonomous = args.autonomousIds.has(m.id);
    const sourceDevice =
      m.role === "user" ? args.sourceByMessageId?.get(m.id) : undefined;
    liveRows.push(...buildMessageRows(m, autonomous, sourceDevice, content));
  }
  // 前缀与 settled 共享引用(浅拷贝数组,元素仍是同一批 row 对象)。
  const rows = settled.rows.slice(0, prefixLen).concat(liveRows);

  // rowIndexByKey:复制 settled 的,剔除被替换掉的 settled 组 key,再写入 live 行 key。
  // firstRowIndexByMessageId 在尾部后缀场景下不受影响,原样复用。
  const rowIndexByKey = new Map(settled.rowIndexByKey);
  for (const g of liveGroups) {
    for (let i = g.start; i < g.start + g.length; i++) {
      rowIndexByKey.delete(settled.rows[i].key);
    }
  }
  for (let i = 0; i < liveRows.length; i++) {
    rowIndexByKey.set(liveRows[i].key, prefixLen + i);
  }

  return {
    firstRowIndexByMessageId: settled.firstRowIndexByMessageId,
    rowIndexByKey,
    rows,
  };
}

/** 兼容入口:完整重建(等价于 settled + applyLive),供测试与一次性调用方使用。 */
export function buildTranscriptRows(
  args: BuildTranscriptRowsArgs,
): TranscriptRowsResult {
  const {
    groupByMessageId: _g,
    messageByMessageId: _m,
    ...result
  } = buildRows(args);
  return result;
}

// estimateRowSize:按 item 类型估行高,供虚拟器 estimateSize 用。真实高度由
// measureElement 动态测量覆盖,估值只影响冷跳收敛速度 —— 但如果估值系统性偏小,
// 会导致 getTotalSize() 系统性偏小,滚动条长度/贴底位置在测量前后跳变。
//
// 校准基准(2026-07-20,对话流字号/间距重构后):正文 --text-prose 从 14px/1.625
// 变成 15px/1.7,单行行高 14×1.625=22.75px → 15×1.7=25.5px,比例 25.5/22.75 ≈
// 1.1209。这一档同时是 text/placeholder 行原有估值 132(单行纯文本消息,含头像/
// 名字/时间戳/footer chrome)的缩放依据:132 × 1.1209 ≈ 148。
//
// 其余档位(折叠态卡片/thinking/图片/本地命令/compact 分隔线)虽不是"正文一行",
// 但共享同一份 chrome 增长:卡片内边距 px-3 py-2 → px-3.5 py-2.5(头部),元信息
// 字号 9/10/11px → 统一 12px/20px(--text-meta),工具卡/代码/思考正文 12px →
// 13px(--text-aux,1.65 行高)。这些增量换算成相对值与 25.5/22.75 同一量级,
// 故对全部档位统一按同一比例缩放,而不是只调 text 一档 —— 否则非文本行会重新
// 变成新的系统性偏小来源。
const ROW_SIZE_SCALE = 25.5 / 22.75;

function scaleRowSize(base: number): number {
  return Math.round(base * ROW_SIZE_SCALE);
}

export function estimateRowSize(row: TranscriptRow): number {
  switch (row.item.type) {
    case "text":
    case "placeholder":
      return scaleRowSize(132); // 148
    case "image":
      return scaleRowSize(160); // 179
    case "thinking":
      return scaleRowSize(40); // 45
    case "activity":
      // 折叠态活动块只有一行组头(一行 --text-meta + 上下内边距),与折叠态
      // thinking 同档 —— 比整张折叠卡片(下方 default 的 54)矮一档。展开态由
      // measureElement 实测覆盖。
      return scaleRowSize(40); // 45
    case "compact_boundary":
      return scaleRowSize(48); // 54
    case "notice":
      return scaleRowSize(48); // 54
    case "local_command":
      return scaleRowSize(120); // 135
    default:
      // tool / plan / tool_permission_request / unknown:折叠态卡片。
      return scaleRowSize(48); // 54
  }
}

// ─── 行间距增量(virtualizer estimateSize 用,Task 10 复审 Important 缺口补丁) ───
//
// chat.tsx 把行间距 padding 打在与 measureElement 同一个 div 上(见 chat.tsx
// rowWrapperPad 的注释:「padding 打在行 wrapper 上,跟随 measureElement 一起计入
// 行高」)——消息末行 pb-5→pb-7(20px→28px),消息内分片行 pb-2→pb-2.5(8px→10px)。
// 这意味着上面 estimateRowSize() 表里的每一档旧估值(132/160/40/48/120)本就是在
// 旧 padding(20px/8px)年代靠肉眼观测校准出来的整行高度,已经隐含烘焙了旧 padding。
// ROW_SIZE_SCALE 只覆盖字号/间距"整体变大"的乘法关系(14×1.625→15×1.7),并不知道
// padding 本身是从 20/8 加法跳到 28/10、不是等比例放大的。用 ROW_SIZE_SCALE 缩放旧
// padding 只会放大到 20×SCALE≈22.4px / 8×SCALE≈8.97px,分别比新值少 ≈5.6px / ≈1px
// —— 这正是下面两个常量要在 estimateRowSize() 之上补回的差值,与具体 item 类型无关
// (类型差异已经在 estimateRowSize 里算过了)。
const OLD_ROW_END_PADDING_PX = 20; // 重构前 pb-5(消息末行)
const OLD_ROW_MID_PADDING_PX = 8; // 重构前 pb-2(消息内分片行)
const NEW_ROW_END_PADDING_PX = 28; // chat.tsx rowWrapperPad 的 pb-7
const NEW_ROW_MID_PADDING_PX = 10; // chat.tsx rowWrapperPad 的 pb-2.5

/** 消息末行间距增量,≈5.6px(28 - 20×ROW_SIZE_SCALE)。 */
export const ROW_END_PADDING_DELTA =
  NEW_ROW_END_PADDING_PX - OLD_ROW_END_PADDING_PX * ROW_SIZE_SCALE;
/** 消息内分片行间距增量,≈1px(10 - 8×ROW_SIZE_SCALE)。 */
export const ROW_MID_PADDING_DELTA =
  NEW_ROW_MID_PADDING_PX - OLD_ROW_MID_PADDING_PX * ROW_SIZE_SCALE;

// isLastRowOfMessage:该虚拟行是否为其所属消息的最后一行(下一行不存在,或属于另一
// 条消息 / local_command)。与 chat.tsx:rowWrapperPad 选 pb-7 还是 pb-2.5 用的是
// 完全相同的边界判断——两处必须共用这一份逻辑,否则实际渲染的 padding 与虚拟化估值
// 的间距增量会各算各的,重新制造出一个新的系统性偏差。
export function isLastRowOfMessage(
  rows: readonly TranscriptRow[],
  index: number,
): boolean {
  const current = rows[index];
  const next = rows[index + 1];
  return next == null || next.messageId !== current?.messageId;
}

// estimateRowSizeWithSpacing:virtualizer estimateSize 回调用的完整估值 ——
// estimateRowSize 只负责按 item 类型估内容高度,这里再按 isLastRowOfMessage 补上
// 对应的间距增量(消息末行 / 块内行)。index 越界(row 不存在)时回退到与旧
// chat.tsx:estimateSize 兜底值一致的 148。
export function estimateRowSizeWithSpacing(
  rows: readonly TranscriptRow[],
  index: number,
): number {
  const current = rows[index];
  if (!current) return 148;
  const delta = isLastRowOfMessage(rows, index)
    ? ROW_END_PADDING_DELTA
    : ROW_MID_PADDING_DELTA;
  return Math.round(estimateRowSize(current) + delta);
}

export function stableBlockIdentity(
  block?: TranscriptBlock,
): string | undefined {
  if (!block) return undefined;
  if (block.toolUseId) return `tool:${block.toolUseId}`;
  if (block.toolPermission?.requestId) {
    return `permission:${block.toolPermission.requestId}`;
  }
  if (block.askUserQuestion?.requestId) {
    return `ask:${block.askUserQuestion.requestId}`;
  }
  if (block.toolApproval?.requestId) {
    return `tool-approval:${block.toolApproval.requestId}`;
  }
  if (block.execApproval?.id) {
    return `exec-approval:${block.execApproval.id}`;
  }
  const canonical = (block as { canonical?: unknown }).canonical;
  if (!canonical || typeof canonical !== "object") return undefined;
  const c = canonical as {
    planApprove?: { requestId?: string };
    toolPermission?: { requestId?: string };
    userAsk?: { requestId?: string };
  };
  if (c.planApprove?.requestId) return `plan:${c.planApprove.requestId}`;
  if (c.toolPermission?.requestId) {
    return `permission:${c.toolPermission.requestId}`;
  }
  if (c.userAsk?.requestId) return `ask:${c.userAsk.requestId}`;
  return undefined;
}
