// facts.ts — 活动行渲染需要的「这一步实际做了什么」的纯提取层。零 JSX,便于单测。
//
// 判据本身不在这里:三档权重来自 canonical-tool/tier 的 tier(),图标分类来自
// toolCategory() —— 全程不查工具名表(raw/summary.ts 立下的规矩)。这里只做两件
// 事:把 canonical / result 里已有的事实(增删行、退出码、结果规模)取出来,以及
// 决定这一步用哪一档字重。

import { relativizePath, summarizeRawTool } from "@agentre-ai/agentre-ui";

import type { ChatBlockData } from "@/stores/chat-streams-store";

import { commandResultOf } from "../canonical-tool/command-result";
import { displayName, tier } from "../canonical-tool/tier";
import type { CanonicalDTO } from "../canonical-tool/types";
import { isFailedStep, type ActivityStep } from "../transcript-rows";

/** 三档视觉权重:读最轻、中性居中、写最重。出组档不会进活动块,兜底按中性处理。 */
export type ActivityWeight = "neutral" | "read" | "write";

export function stepWeight(step: ActivityStep): ActivityWeight {
  // 思考与只读探查同为「不改变工程」的活动,取最轻的一档。
  if (step.type === "thinking") return "read";
  const block = step.toolBlock;
  if (!block) return "neutral";
  const t = tier(block);
  return t === "read" || t === "write" ? t : "neutral";
}

export function canonicalOf(block?: ChatBlockData): CanonicalDTO | undefined {
  return (block as { canonical?: CanonicalDTO } | undefined)?.canonical;
}

// PendingOutcome —— 一步没有 tool_result 时按什么归属报它。
// 「没有标记 = 成功」只有在还没成功的步骤都带标记时才成立(spec 决策 10:只有
// 运行中 / 失败 / 待审批有标记),所以没有结果的一步一定要报点什么:
//   - running:还在跑(转录里的默认 —— 结果随时可能到);
//   - failed / canceled / unknown:所在的 run 已经终结,这一步始终没配到结果,
//     归属不明。沿用旧 AgentSpawnStepCard 的同一套判据(见 agent-spawn/card.tsx
//     的 unmatchedOutcome),不是新语义。
export type PendingOutcome = "running" | "failed" | "canceled" | "unknown";

/** 一步执行完之后可报出来的事实。空值一律表示「这一步没有这项事实」。 */
export type StepFacts = {
  /** 结果被标成错误 —— 与组头红色失败计数同一判据(resultBlock.isError)。 */
  failed: boolean;
  /** 这一步还没有结果时的归属;有结果就是 undefined。 */
  pending?: PendingOutcome;
  /**
   * run_in_background 的命令此刻还在后台跑。它的 tool_result 是**启动 ACK**,
   * 一到就有结果,所以不能靠 pending 判(与 raw/card.tsx 的 bgRunning 同一判据:
   * 看后台 subagent 的状态)。没有这个标记,一个还在跑的后台任务在转录里与
   * 跑完的一步完全同形。
   */
  backgroundRunning?: boolean;
  /** 后台任务 id —— 与「后台任务」面板里的那一条对得上。 */
  backgroundTaskId?: string;
  /** 非零退出码。命令类结果的 JSON 形状里才有。 */
  exitCode?: number;
  /** file.edit / file.write 的增删行。 */
  plus?: number;
  minus?: number;
  /** 展开体要渲染的结果正文(命令结果取 output,其余取原文)。 */
  resultText: string;
  /** 单行结果的预览,长度封顶 PREVIEW_MAX_CHARS(多行结果改报规模)。 */
  preview?: string;
  /** 多行结果的行数 —— 折叠态报规模而不是把整段结果塞进行里。 */
  lines?: number;
};

// 行尾预览的长度上限。预览是**折叠态就在 DOM 里**的那段文字,而结果原文没有大小
// 上限(单行 JSON / base64 / 压缩过的一行输出动辄几 MB)。整段塞进一个
// whitespace-nowrap 的行尾,浏览器就得为一个折叠着的行去量一条几 MB 宽的行盒 ——
// 正是「折叠态不 mount 结果文本」要挡掉的开销。行尾可见区只有 max-w-56,这个上限
// 远超它能显示的字数;真正的省略号由 CSS truncate 画,完整原文点开就在展开体里。
const PREVIEW_MAX_CHARS = 200;

export function stepFacts(
  step: ActivityStep,
  pendingOutcome: PendingOutcome = "running",
): StepFacts {
  if (step.type === "thinking") {
    const text = step.block.text ?? "";
    return { failed: false, resultText: text };
  }
  const result = step.resultBlock;
  const command = commandResultOf(result);
  const resultText = command ? command.output : (result?.text ?? "");
  const canonical = canonicalOf(step.toolBlock);
  const pending = result ? undefined : pendingOutcome;
  const facts: StepFacts = {
    ...backgroundFacts(step.toolBlock),
    exitCode:
      typeof command?.exitCode === "number" && command.exitCode !== 0
        ? command.exitCode
        : undefined,
    // run 以失败终结、这一步又没有结果 —— 按失败行呈现(旧 step 卡同一判据)。
    // 判据本身在 transcript-rows 的 isFailedStep,组头失败计数用的是同一个。
    failed: isFailedStep(step, pendingOutcome === "failed"),
    pending,
    resultText,
  };
  if (canonical?.kind === "file.edit") {
    const files = canonical.fileEdit?.files ?? [];
    facts.plus = files.reduce((a, f) => a + (f.plus ?? 0), 0);
    facts.minus = files.reduce((a, f) => a + (f.minus ?? 0), 0);
    return facts;
  }
  if (canonical?.kind === "file.write") {
    facts.plus = canonical.fileWrite?.lines ?? 0;
    return facts;
  }
  const trimmed = resultText.trim();
  if (!trimmed) return facts;
  const lineCount = countLines(trimmed);
  if (lineCount > 1) facts.lines = lineCount;
  else facts.preview = trimmed.slice(0, PREVIEW_MAX_CHARS);
  return facts;
}

// countLines 数行不切片:split("\n") 会给一段 5 万行的结果实例化 5 万个子串,
// 而这里只要一个数字。
function countLines(text: string): number {
  let lines = 1;
  for (let i = text.indexOf("\n"); i !== -1; i = text.indexOf("\n", i + 1)) {
    lines++;
  }
  return lines;
}

// backgroundFacts 与 raw/card.tsx 的 bgRunning 同源:run_in_background 的命令
// 立刻拿到启动 ACK,所以「还在不在跑」只能看后台 subagent 的状态,不能看有没有
// 结果。终态之外(含状态缺失 = 刚起还没回报)一律算还在跑。
function backgroundFacts(
  block?: ChatBlockData,
): Pick<StepFacts, "backgroundRunning" | "backgroundTaskId"> {
  const input = block?.toolInput as Record<string, unknown> | undefined;
  if (input?.run_in_background !== true) return {};
  const status = block?.subagent?.status;
  if (status === "completed" || status === "failed" || status === "canceled") {
    return {};
  }
  return {
    backgroundRunning: true,
    backgroundTaskId: block?.subagent?.taskId ?? undefined,
  };
}

// ─── 行首标签 ────────────────────────────────────────────────────────────────

/**
 * 一步在行首怎么称呼自己。活动行与组头的运行态尾巴共用 —— 两处各拼一遍必然漂移
 * (一处认得 canonical、另一处不认,同一步在展开前后叫两个名字)。
 */
export type StepLabel = {
  /** 工具名。shell 形态统一叫 Bash(与 RawToolCard 同一条规矩:认 input 不认工具名)。 */
  name: string;
  /** 一行以内的摘要。fileCount 有值时由调用方改用 i18n 的「N 个文件」。 */
  summary: string;
  /** 仅多文件 file.edit:改到几个文件(文案在组件层,这里不引 i18n)。 */
  fileCount?: number;
};

// 行首摘要的长度上限。摘要与行尾预览一样是**折叠态就在 DOM 里**的文字,而工具入参
// 没有大小上限 —— summarizeRawTool 认不出语义字段时会退到「首个键=JSON」,一次
// 几百 KB 的写入 / 一段 unified diff 会整段进到一个 whitespace-nowrap 的行盒里。
const SUMMARY_MAX_CHARS = 200;

export function stepLabel(block?: ChatBlockData, cwd?: string): StepLabel {
  const input = block?.toolInput as Record<string, unknown> | undefined;
  // RawToolCard 的同一条规矩:input 里有 command 就是 shell 形态,标签用 Bash
  // (codex 的 command_execution 与 claudecode 的 Bash 同形)。
  const name =
    typeof input?.command === "string"
      ? "Bash"
      : displayName(block?.toolName ?? "tool");
  // canonical.file.edit 的路径只在 canonical 里:codex 的 file_change input 是
  // {changes:[{path,kind,diff}]},没有 path/file_path 键,summarizeRawTool 会退到
  // 「首个键=JSON」把整段 diff 当摘要 —— 路径丢了(旧 FileEditCard 卡头一直显示它),
  // 还把无上限的 diff 挂进折叠行。
  const canonical = canonicalOf(block);
  if (canonical?.kind === "file.edit") {
    const files = canonical.fileEdit?.files ?? [];
    if (files.length === 1) {
      return { name, summary: relativizePath(files[0].path, cwd) };
    }
    if (files.length > 1) return { fileCount: files.length, name, summary: "" };
  }
  const summary = summarizeRawTool(block?.toolName ?? "", input, { cwd });
  return {
    name,
    summary:
      summary.length > SUMMARY_MAX_CHARS
        ? summary.slice(0, SUMMARY_MAX_CHARS)
        : summary,
  };
}
