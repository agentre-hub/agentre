// @ts-nocheck
// 最近使用目标（spec「UI, accessibility and recent targets」决策 19）：
//
//   - 只存本机 localStorage，按执行位置指纹隔离（local / daemon:<deviceId>）；
//   - 最多五项，只保存 providerKey/modelKey —— 绝不保存名称、ModelID、API Key；
//   - 不进入账号同步（纯 UI 偏好）；
//   - 只有 target 成功持久化后才记录（recordRecentTarget 由消费方在保存成功后调用）；
//   - native/inherit（双空 key）永远不进入最近。
import type { ModelTarget } from "./types";

const RECENT_KEY_PREFIX = "agentre.modelTargetPicker.recent.v1";
const MAX_RECENT = 5;

// recentStorageKey 按场景 + 执行位置指纹隔离。executionLocation 空串 = 本机。
export function recentStorageKey(
  scenario: string,
  executionLocation: string,
): string {
  const loc =
    executionLocation && executionLocation !== ""
      ? `daemon:${executionLocation}`
      : "local";
  return `${RECENT_KEY_PREFIX}.${scenario}.${loc}`;
}

// readRecentTargets 读取最近目标；损坏数据静默忽略。只返回 providerKey/modelKey。
export function readRecentTargets(
  scenario: string,
  executionLocation: string,
): ModelTarget[] {
  try {
    const raw = window.localStorage.getItem(
      recentStorageKey(scenario, executionLocation),
    );
    if (!raw) return [];
    const arr = JSON.parse(raw);
    if (!Array.isArray(arr)) return [];
    return arr
      .filter(
        (x) =>
          x &&
          typeof x.providerKey === "string" &&
          typeof x.modelKey === "string",
      )
      .map((x) => ({
        providerKey: x.providerKey as string,
        modelKey: x.modelKey as string,
      }))
      .slice(0, MAX_RECENT);
  } catch {
    return [];
  }
}

// removeRecentTarget 移除单个最近目标（chip 的 X 按钮）。移除后不补位，长度自然缩短。
export function removeRecentTarget(
  scenario: string,
  executionLocation: string,
  target: ModelTarget,
): void {
  if (!target || target.providerKey === "") return;
  try {
    const prev = readRecentTargets(scenario, executionLocation);
    const next = prev.filter(
      (x) =>
        !(
          x.providerKey === target.providerKey && x.modelKey === target.modelKey
        ),
    );
    window.localStorage.setItem(
      recentStorageKey(scenario, executionLocation),
      JSON.stringify(next),
    );
  } catch {
    // localStorage 不可用（隐私模式等）——最近使用是纯增强，静默忽略。
  }
}

// recordRecentTarget 在 target 成功持久化后调用（消费方保存成功后）。native/inherit
// 双空 key 不进入最近；dedupe 后保持在最前并截断到 5 项。
export function recordRecentTarget(
  scenario: string,
  executionLocation: string,
  target: ModelTarget | null | undefined,
): void {
  if (!target || target.providerKey === "") return;
  try {
    const prev = readRecentTargets(scenario, executionLocation);
    const next = [
      { providerKey: target.providerKey, modelKey: target.modelKey },
      ...prev.filter(
        (x) =>
          !(
            x.providerKey === target.providerKey &&
            x.modelKey === target.modelKey
          ),
      ),
    ].slice(0, MAX_RECENT);
    window.localStorage.setItem(
      recentStorageKey(scenario, executionLocation),
      JSON.stringify(next),
    );
  } catch {
    // localStorage 不可用（隐私模式等）——最近使用是纯增强，静默忽略。
  }
}
