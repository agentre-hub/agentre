import * as React from "react";

import { useUiTranslation } from "../i18n";
import type { ProjectWriteOutcome } from "./ports";

/**
 * 一次写的结果怎么变成一句话。
 *
 * 分得出类时用包写好的那一句；分不出类时透出宿主抽好的业务文案。**两条规则一条式子**
 * —— 中继写失败按错误码分四类各给一句（把它们折成同一句「保存失败」，用户就得自己猜
 * 是该等一会儿、该换个目录还是该去开那台机器），而服务端自带文案的业务码
 * （「该 Agent 已经是这个项目的成员」）原样透出。
 *
 * 住在这里而不是设置弹窗里：新建弹窗也要写路径，也要把同一种失败说成同一句话。
 */
export function useFailureText() {
  const { t } = useUiTranslation();
  return React.useCallback(
    (outcome: Extract<ProjectWriteOutcome, { ok: false }>) => {
      const { kind, message } = outcome.failure;
      if (kind !== "unknown") return t(`projectSettings.failure.${kind}`);
      return message?.trim() || t("projectSettings.failure.unknown");
    },
    [t],
  );
}
