// frontend/src/lib/attention-display.ts
//
// attention reason → UI 投影的**宿主适配层**。
//
// 判定与投影本身住在共享包里（`@agentre-hub/agentre-ui` 的 session-index/attention），
// 两端共用一份 —— "红还是橙"、"pill 写什么"、"组头取哪一档色" 都在那儿。这里只剩
// 宿主独有的那一件事：把包的 `agentreUi` namespace 绑到本仓库这个 i18next 实例上，
// 好让既有调用方继续用单参数的 `reasonToPillText(reason)`。
import {
  AGENTRE_UI_NAMESPACE,
  reasonToPillText as reasonToPillTextWith,
  type AttentionReason,
} from "@agentre-hub/agentre-ui";

import i18n from "@/i18n";

export {
  reasonToDisplayStatus,
  strongestAttentionTone,
} from "@agentre-hub/agentre-ui";

/**
 * 包里那些纯函数认的取词器：本仓库的 i18next 实例 + 包自己的 namespace。
 *
 * 绑定只写这一处。包不建实例（见包 `src/i18n` 的说明），所以每个调用点都得自己
 * 补上 `{ ns }` —— 漏掉的那次会静默落到宿主的 defaultNS 上，取不到就把 key 原样
 * 显示给用户。
 */
export function uiT(key: string): string {
  return i18n.t(key, { ns: AGENTRE_UI_NAMESPACE });
}

export function reasonToPillText(
  reason: AttentionReason | null,
): string | null {
  return reasonToPillTextWith(reason, uiT);
}
