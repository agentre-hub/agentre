/**
 * 批量清理对话历史这一面的宿主边界（规格 2026-11-12）。
 *
 * 与 `session-import/ports.ts` 同一条路子：包只认这里的 view 与 port，不认识桌面端的
 * Wails 绑定，也不认识 agentre-server 的 REST。id 一律是字符串 —— 桌面端的会话 /
 * Agent / 项目 id 是数字，server 那侧是 syncId，包原样送回宿主。
 *
 * 「把时间档换成截止时间、把 `all` 换成不筛」是宿主的事：包只报用户选了什么。
 */

/** 最后活动早于多久之前；`all` = 不限时间。 */
export type CleanupBefore = "30d" | "90d" | "180d" | "all";

export const CLEANUP_ALL = "all";

export interface CleanupFilter {
  before: CleanupBefore;
  /** Agent id，或 `"all"`。 */
  agentId: string;
  /** 项目 id，或 `"all"`。 */
  projectId: string;
}

export interface CleanupOption {
  id: string;
  label: string;
}

/** 一份副本：确认框里一行。 */
export interface CleanupCopyView {
  /** local = 这台电脑；account = 账号里保存的；machine = 执行端机器。 */
  kind: "local" | "account" | "machine";
  /** machine 的机器名；local / account 由包自己出文案，可不填。 */
  label: string;
  /** 仅 machine：此刻是否在线。 */
  online?: boolean;
  /** 仅 machine：未登录时经局域网配对直接删除。 */
  lanDirect?: boolean;
  count: number;
}

export interface CleanupPreviewView {
  /** 将被清理的数量（= ids.length）。 */
  count: number;
  /** 因进行中 / 等你操作而保留的数量。 */
  retained: number;
  /** 截止日期（unix 毫秒）；时间档为「全部」时为 null。 */
  cutoffDate: number | null;
  /** 确认那一刻计数里的对话；执行就送这批。 */
  ids: string[];
  copies: CleanupCopyView[];
}

export interface CleanupFailureView {
  id: string;
  /** 宿主已本地化的原因。 */
  reason: string;
}

export interface CleanupPendingView {
  /** 离线机器名。 */
  label: string;
  count: number;
}

export interface CleanupExecuteView {
  deleted: number;
  failed: CleanupFailureView[];
  /** 离线、等它下次上线再删的。 */
  pending: CleanupPendingView[];
  /** 桌面端：因此关闭的标签页数。 */
  closedTabs?: number;
}

export interface SessionCleanupPorts {
  preview(filter: CleanupFilter): Promise<CleanupPreviewView>;
  /** 只删这些 id；宿主执行时重新判断并保留进行中的。 */
  execute(ids: string[]): Promise<CleanupExecuteView>;
  agents: CleanupOption[];
  projects: CleanupOption[];
  /**
   * 确认框因完成而关闭时调用一次，带这一轮累计的结果（含重试）。toast / 关标签页
   * 归宿主。部分失败后用户点「关闭」时，只要删掉过东西也会调用。
   */
  onDone?(result: CleanupExecuteView): void;
}
