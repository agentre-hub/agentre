import type { ChatBlockData } from "@/stores/chat-streams-store";

import type { chat_svc } from "../../../../wailsjs/go/models";

import type {
  BackgroundTask,
  BackgroundTaskKind,
  BackgroundTaskStatus,
} from "./types";

// deriveBackgroundTasks 从历史消息 + 当前 live blocks 中提取所有后台任务。
// 收真正的后台任务：kind 为 local_bash(后台 Bash)或 local_agent(后台 subagent),
// **且** 通过 isBackground(kind, run_in_background) 判定为后台。真实 CLI 对*每一次*
// Bash / 每个 subagent 都发 task 帧(不只是后台的),所以光看 kind 会把前台 bash /
// 前台 subagent 也收进来 —— 是否后台要看工具入参 run_in_background,但 Bash 与 Agent
// 默认相反(Bash 默认前台需显式 true;Agent 默认后台缺省即后台),判据按 kind 分叉,
// 详见 isBackground。按 toolUseId dedupe：live 覆盖 history（live 更新）。
// VisitableBlock 是 visit 只需读取的最小结构投影。subagent 直接复用生成的
// ChatBlockSubagent，让 ChatBlockData（subagent: ChatBlockSubagent）无需 cast
// 即可传入；持久化 chat_svc.ChatBlock 走双重 cast 投影。toolInput 是工具 raw 入参
// （wire DTO view.ChatBlock.toolInput / live ChatBlockData.toolInput）。
type VisitableBlock = {
  type?: string;
  toolUseId?: string;
  toolInput?: Record<string, unknown>;
  subagent?: chat_svc.ChatBlockSubagent;
};

export function deriveBackgroundTasks(
  messages: chat_svc.ChatMessage[],
  liveBlocks: ChatBlockData[],
  clearedToolUseIds?: ReadonlySet<string>,
): BackgroundTask[] {
  const byId = new Map<string, BackgroundTask>();

  const visit = (block: VisitableBlock | undefined, startedAt?: number) => {
    if (!block || block.type !== "tool_use") return;
    const sa = block.subagent;
    const toolUseId = block.toolUseId;
    if (!toolUseId || !sa) return;
    // 只收后台 Bash(local_bash)与后台 subagent(local_agent);空/未知 kind(旧帧)
    // 排除,避免展示无法归类的神秘块。
    if (sa.kind !== "local_bash" && sa.kind !== "local_agent") return;
    // CLI 对每一次 Bash / 每个 subagent 都发 task 帧,kind 不足以判定「后台」;还要看工具
    // 入参 run_in_background —— 但两种工具的默认相反,判据必须按 kind 分叉:
    //   - Bash 默认前台,仅显式 run_in_background===true 为后台;
    //   - Agent(subagent)默认后台,run_in_background 缺省即后台,仅显式 ===false 才是
    //     前台(同步)。(真实 CLI 实测:后台 Agent 根本不带此入参。)
    if (!isBackground(sa.kind, block.toolInput?.run_in_background)) return;
    if (clearedToolUseIds?.has(toolUseId)) return;
    const prev = byId.get(toolUseId);
    byId.set(toolUseId, {
      toolUseId,
      taskId: sa.taskId || prev?.taskId,
      kind: sa.kind as BackgroundTaskKind,
      description: sa.taskDescription ?? prev?.description ?? "",
      status: mapStatus(sa.status),
      startedAt: startedAt ?? prev?.startedAt,
      durationMs:
        (typeof sa.durationMs === "number" ? sa.durationMs : undefined) ??
        prev?.durationMs,
      summary: (sa.summary || undefined) ?? prev?.summary,
    });
  };

  // 先处理历史消息 (history)，再处理 live blocks (live wins on conflict)。
  // 历史 block 是 chat_svc.ChatBlock，走 task-progress/derive 同款双重 cast
  // 投影到 VisitableBlock。startedAt 取自消息的 createtime (epoch ms)。
  for (const m of messages) {
    for (const b of m.blocks ?? [])
      visit(b as unknown as VisitableBlock, m.createtime);
  }
  // liveBlocks 是 ChatBlockData，结构性满足 VisitableBlock，无需 cast。
  // live blocks 没有关联的消息，所以 startedAt 为 undefined。
  for (const b of liveBlocks) visit(b);

  return [...byId.values()];
}

// isBackground 判定一个 task 帧是否「后台」。两种工具默认相反:
//   - local_bash(Bash)默认前台,仅工具入参 run_in_background===true 为后台;
//   - local_agent(subagent / Agent 工具)默认后台,run_in_background 缺省即后台,
//     仅显式 run_in_background===false 才是前台(同步)。
function isBackground(kind: string, runInBackground: unknown): boolean {
  if (kind === "local_agent") return runInBackground !== false;
  return runInBackground === true;
}

function mapStatus(raw: string | undefined): BackgroundTaskStatus {
  if (raw === "completed") return "completed";
  if (raw === "failed") return "failed";
  // 用户停止 → canceled;英式 cancelled 由后端在边界归一,到不了这里。
  if (raw === "canceled") return "canceled";
  return "running";
}
