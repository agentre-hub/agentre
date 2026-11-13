// 桌面端「清理对话历史」的宿主适配：把 agentre-ui 的 SessionCleanupPorts（字符串 id、
// 时间档）翻译成 Wails 绑定（数字 id、截止毫秒）。包不认识 Wails，这里不认识 UI 细节。
import type {
  CleanupExecuteView,
  CleanupFilter,
  CleanupOption,
  CleanupPreviewView,
  SessionCleanupPorts,
} from "@agentre-hub/agentre-ui";
import type { TFunction } from "i18next";

import {
  ExecuteChatCleanup,
  PreviewChatCleanup,
} from "../../../../wailsjs/go/app/App";

const DAY_MS = 24 * 60 * 60 * 1000;
const BEFORE_DAYS = { "30d": 30, "90d": 90, "180d": 180 } as const;

/** 时间档 → 截止毫秒；`all` = 0（不限）。在预览时刻计算。 */
export function cutoffFor(before: CleanupFilter["before"], now: number) {
  return before === "all" ? 0 : now - BEFORE_DAYS[before] * DAY_MS;
}

function numericId(id: string): number {
  if (id === "all") return 0;
  const n = Number(id);
  return Number.isFinite(n) ? n : 0;
}

/** 后端失败原因码 → 本地化文案；未知码也给一句人话，绝不露原始码。 */
export function failureReason(code: string, t: TFunction): string {
  switch (code) {
    case "read_failed":
      return t("dataBackup.cleanup.reason.readFailed");
    case "delete_failed":
      return t("dataBackup.cleanup.reason.deleteFailed");
    default:
      return t("dataBackup.cleanup.reason.unknown");
  }
}

/** 预览 → 执行之间要记住的东西；宿主跨 ports 重建持有它（选项列表一加载 ports 就会换新）。 */
export interface DesktopCleanupMemory {
  /** 预览时记下的子 Agent 会话：执行时一并送回（它们不计数、不展示）。 */
  subagentIds: number[];
  /** 这一轮已删掉的会话 id（含重试累计）。 */
  deletedIds: number[];
}

export function newCleanupMemory(): DesktopCleanupMemory {
  return { subagentIds: [], deletedIds: [] };
}

export interface DesktopCleanupDeps {
  memory: DesktopCleanupMemory;
  t: TFunction;
  agents: CleanupOption[];
  projects: CleanupOption[];
  /** 完成时带上这一轮真正删掉的会话 id（含重试累计）。 */
  onDone: (result: CleanupExecuteView, deletedIds: number[]) => void;
  now?: () => number;
}

export function createDesktopCleanupPorts(
  deps: DesktopCleanupDeps,
): SessionCleanupPorts {
  const now = deps.now ?? Date.now;
  const mem = deps.memory;

  return {
    agents: deps.agents,
    projects: deps.projects,
    async preview(filter): Promise<CleanupPreviewView> {
      const at = now();
      const resp = await PreviewChatCleanup({
        beforeMs: cutoffFor(filter.before, at),
        agentId: numericId(filter.agentId),
        projectId: numericId(filter.projectId),
      } as never);
      mem.subagentIds = resp.subagentSessionIds ?? [];
      mem.deletedIds = [];
      return {
        count: resp.count,
        retained: resp.retained,
        cutoffDate:
          filter.before === "all" ? null : cutoffFor(filter.before, at),
        ids: (resp.sessionIds ?? []).map(String),
        copies: (resp.copies ?? []).map((c) => ({
          kind: c.kind as "local" | "account" | "machine",
          label: c.name,
          online: c.online,
          lanDirect: c.lanDirect,
          count: c.count,
        })),
      };
    },
    async execute(ids): Promise<CleanupExecuteView> {
      const resp = await ExecuteChatCleanup({
        sessionIds: ids.map(numericId),
        subagentSessionIds: mem.subagentIds,
      } as never);
      const deleted = resp.deleted ?? [];
      mem.deletedIds = [...mem.deletedIds, ...deleted];
      return {
        deleted: deleted.length,
        failed: (resp.failed ?? []).map((f) => ({
          id: String(f.id),
          reason: failureReason(f.reason, deps.t),
        })),
        pending: (resp.pending ?? []).map((p) => ({
          label:
            p.kind === "account"
              ? deps.t("dataBackup.cleanup.accountLabel")
              : p.machine,
          count: p.count,
        })),
      };
    },
    onDone(result) {
      const ids = mem.deletedIds;
      mem.deletedIds = [];
      deps.onDone(result, ids);
    },
  };
}
