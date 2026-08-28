import type { TFunction } from "i18next";

import type { LostChangeView } from "./use-sync-status";

// entity_type 字面量,与 internal/pkg/syncwire.Kind* 逐字一致。
const ENTITY_LABEL_KEYS: Record<string, string> = {
  project: "sync.lostChanges.entity.project",
  department: "sync.lostChanges.entity.department",
  agent: "sync.lostChanges.entity.agent",
  agent_backend: "sync.lostChanges.entity.agentBackend",
  agent_exec_target: "sync.lostChanges.entity.execTarget",
  project_agent: "sync.lostChanges.entity.projectMember",
  project_location: "sync.lostChanges.entity.projectLocation",
};

// reason 字面量,与 syncqueue_entity.Reason* 逐字一致。
const REASON_LABEL_KEYS: Record<string, string> = {
  overwritten: "sync.lostChanges.reason.overwritten",
  discarded: "sync.lostChanges.reason.discarded",
  rejected: "sync.lostChanges.reason.rejected",
};

export function entityLabel(entityType: string, t: TFunction): string {
  const key = ENTITY_LABEL_KEYS[entityType];
  return key ? t(key) : entityType;
}

export function reasonLabel(reason: string, t: TFunction): string {
  const key = REASON_LABEL_KEYS[reason];
  return key ? t(key) : reason;
}

/**
 * 从载荷正文里摘一个人类可读的标题片段。多数对象类型的载荷带 `name`;
 * `project_location` 只有 `path`(项目名 / 机器名的解析属于同步元数据,不在
 * 这份载荷里,所以退而求其次显示路径)。解析失败或两者都没有时返回空串,
 * 调用方只显示对象类型。
 */
export function payloadTitleDetail(payloadJSON: string): string {
  try {
    const parsed = JSON.parse(payloadJSON) as Record<string, unknown>;
    if (typeof parsed.name === "string" && parsed.name) return parsed.name;
    if (typeof parsed.path === "string" && parsed.path) return parsed.path;
  } catch {
    // 不是合法 JSON:没有可摘的标题片段。
  }
  return "";
}

export function lostChangeTitle(row: LostChangeView, t: TFunction): string {
  const label = entityLabel(row.entityType, t);
  const detail = payloadTitleDetail(row.payloadJSON);
  return detail ? `${label} · ${detail}` : label;
}

/** 展开视图里那份 JSON 正文格式化;解析失败时原样返回(不该发生,但不崩)。 */
export function formatPayload(payloadJSON: string): string {
  try {
    return JSON.stringify(JSON.parse(payloadJSON), null, 2);
  } catch {
    return payloadJSON;
  }
}
