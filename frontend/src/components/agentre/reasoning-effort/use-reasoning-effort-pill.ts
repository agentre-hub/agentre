import * as React from "react";

import type { ReasoningEffortValue } from "@agentre-hub/agentre-ui";
import { AGENTRE_UI_NAMESPACE } from "@agentre-hub/agentre-ui/i18n";

import i18n from "@/i18n";
import { SetChatSessionReasoningEffort } from "../../../../wailsjs/go/app/App";

import {
  pillLastSwitchFailedText,
  toastPillSwitchFailed,
} from "../model-pill/pill-switch-failure";

export interface UseReasoningEffortPillOptions {
  /**
   * >0 = 已有会话:选择立即持久化(调用 SetChatSessionReasoningEffort);0 / undefined =
   * 新建会话:纯瞬态,与 ProviderPill 在同一场景下的既有做法一致(spec 2026-09-01
   * 「新建会话」)。
   */
  sessionId?: number;
  /**
   * 会话行上已持久化的力度;空串 = 跟随后端配置。它同时是共享 Picker 的 no-op
   * 判据 —— 见 ReasoningEffortPickerProps.value 的说明。
   */
  persistedReasoningEffort?: ReasoningEffortValue;
  /** 后端配置的力度,会话行为空时由它兜底展示;空串 = 后端也没配。 */
  persistedBackendReasoningEffort?: ReasoningEffortValue;
  /**
   * 持久化切换成功后的回调(典型 reloadSession(),把后端追加的切换 notice 拉进
   * transcript;控件自身的标签已由本 hook 的乐观更新直接呈现,不依赖它)。
   */
  onSwitched?: () => void;
}

export interface UseReasoningEffortPillReturn {
  /** 会话行上的值(乐观更新);空串 = 跟随后端配置。 */
  value: ReasoningEffortValue;
  /** 后端配置的档位;空串 = 后端也未配置。 */
  backendValue: ReasoningEffortValue;
  /** 选中一档;真的变化时才调 IPC(共享 Picker 已经做过一次 no-op 短路,这里的
   *  相等判断只是双保险,避免宿主直接调用 onChange 时也打一次无意义的 IPC)。 */
  onChange: (next: ReasoningEffortValue) => void;
  /** 上次写库失败的本地化事实;弹层底部据此追加错误行(原话只在失败 toast 的详情里)。 */
  error: string | null;
}

/**
 * useReasoningEffortPill:composer 底栏「思考力度」控件的宿主状态
 * (spec 2026-09-01「会话级思考力度的选择与生效」)。
 *
 * 数据流:
 *  - 新建会话(sessionId<=0)还没有 session 行:选择只存本地 state,不发 IPC——随
 *    首条消息一并落库是发送路径自己的事,不归这个 hook。
 *  - 已有会话(sessionId>0):选中一项立即调 SetChatSessionReasoningEffort 持久化
 *    (乐观更新);失败则回滚到上一档并把原因放进 error,不吞错误
 *   (spec「失败与恢复」)。成功后调 onSwitched()。
 */
export function useReasoningEffortPill({
  sessionId,
  persistedReasoningEffort,
  persistedBackendReasoningEffort,
  onSwitched,
}: UseReasoningEffortPillOptions): UseReasoningEffortPillReturn {
  const [value, setValue] = React.useState<ReasoningEffortValue>(
    persistedReasoningEffort ?? "",
  );
  const [error, setError] = React.useState<string | null>(null);
  const backendValue = persistedBackendReasoningEffort ?? "";

  // 会话切换、或持久化值被外部改写(reload)时,把显示值同步回当前值。
  React.useEffect(() => {
    setValue(persistedReasoningEffort ?? "");
    setError(null);
  }, [sessionId, persistedReasoningEffort]);

  const onChange = React.useCallback(
    (next: ReasoningEffortValue) => {
      if (next === value) return;
      const previous = value;
      setValue(next);
      setError(null);

      if (!sessionId || sessionId <= 0) {
        // 新建会话:纯瞬态,首发消息随会话一并落库(不在这个 hook 的职责内)。
        return;
      }
      void SetChatSessionReasoningEffort({
        sessionId,
        reasoningEffort: next,
      })
        .then(() => {
          onSwitched?.();
        })
        .catch((e: unknown) => {
          console.error("[reasoning-effort-pill] switch failed", e);
          // 写库失败:回滚到上一档,toast 说清现在仍是哪一档(spec 2026-11-10 A2)。
          // 「哪一档」与控件脸上的有效档位同一规则:会话值 → 后端配置 → 默认。
          setValue(previous);
          setError(pillLastSwitchFailedText());
          const label =
            previous ||
            backendValue ||
            i18n.t(
              `${AGENTRE_UI_NAMESPACE}:reasoningEffortPicker.defaultLabel`,
            );
          toastPillSwitchFailed(
            i18n.t("chatPanel.pillSwitch.reasoningEffortFailed", { label }),
            e,
          );
        });
    },
    [value, backendValue, sessionId, onSwitched],
  );

  return {
    value,
    backendValue,
    onChange,
    error,
  };
}
