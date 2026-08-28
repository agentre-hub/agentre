// composer 里那颗模型 pill 的**触发器**：用户一眼看到的那一格。
//
// 它与选择器主体（ModelTargetPicker）分开，是因为两端共享的边界正好落在这里：
//   - 触发器与解析副行是**纯展示**——四态怎么写、跟随态副行写到什么程度，两端必须
//     一模一样，否则同一条对话在桌面端与浏览器里说的是两句话；
//   - 而拿到这份视图模型的过程（Wails 绑定 / HTTP、会话持久化、同步供应商的弹窗）
//     是宿主的事，留在各自宿主。
//
// 入参是一个平坦的视图模型（ProviderPillState），不含任何宿主类型。
import { RefreshCw, UserRound } from "lucide-react";
import * as React from "react";

import { useUiTranslation as useTranslation } from "../../i18n";
import { LlmModelLogo, LlmProviderLogo } from "../ai-brand-logo";

/**
 * 这颗 pill 此刻处在四态里的哪一个，以及它解析到了什么。
 *
 * cliLogin 只有在**确知**该 Agent 后端没绑供应商时才为真：`undefined` / `null`
 * （会话详情或新建目标还没到）是「还不知道」，绝不能当成「没绑」——否则加载中的
 * 一瞬会闪成「CLI 登录态」。
 */
export type ProviderPillState = {
  mode: "follow-agent" | "provider-default" | "fixed" | "invalid";
  providerLabel: string;
  providerType: string;
  modelLabel: string;
  resolutionLabel: string;
  dynamic: boolean;
  cliLogin: boolean;
};

/**
 * ProviderPillTrigger 是 ModelTargetPicker 的 `triggerLabel` 内容。
 *
 * 脸上写的是「**实际会跑哪个模型**」：解析出模型就写模型 ID（标识符走等宽），只解析
 * 到供应商（新建会话没有 agent model key）就退回供应商人读名；确知没绑供应商就写
 * 「CLI 自身登录态」——那才是这一轮真正的模型来源。三者都不成立 = 还不知道，不写。
 *
 * 模式不写成一行字，而是由图标（人形 / 品牌标识 / 失效态的警示三角由 Picker 画）
 * 加 ↻（跟随默认才有）表达，省下来的一行让「实际会跑哪个模型」直接上脸。
 */
export function ProviderPillTrigger({ state }: { state: ProviderPillState }) {
  const { t } = useTranslation();

  const providerLogo = state.providerType ? (
    <LlmProviderLogo
      providerType={state.providerType}
      providerName={state.providerLabel}
      className="size-3.5"
    />
  ) : null;
  const modelLogo = state.modelLabel ? (
    <LlmModelLogo
      model={state.modelLabel}
      providerType={state.providerType}
      providerName={state.providerLabel}
      className="size-3.5"
    />
  ) : (
    providerLogo
  );

  const resolvedTarget = state.modelLabel ? (
    <span className="font-mono">{state.modelLabel}</span>
  ) : state.providerLabel ? (
    state.providerLabel
  ) : state.cliLogin ? (
    t("modelTargetPicker.special.backend")
  ) : null;

  const triggerIcon =
    state.mode === "follow-agent" ? (
      <UserRound
        data-testid="follow-agent-icon"
        className="size-3.5 shrink-0 text-muted-foreground"
        aria-hidden="true"
      />
    ) : state.mode === "invalid" ? null : (
      // 失效态的警示三角由 Picker 的 invalid 分支画,这里不重复挂图标。
      modelLogo
    );

  const modeMarker = state.dynamic ? (
    <RefreshCw
      data-testid="provider-pill-dynamic-icon"
      className="size-3 shrink-0 text-primary-text"
      aria-hidden="true"
    />
  ) : null;

  const triggerText =
    state.mode === "follow-agent" ? (
      <>
        {t("modelTargetPicker.special.chat")}
        {resolvedTarget ? (
          <>
            {" · "}
            {resolvedTarget}
          </>
        ) : null}
      </>
    ) : state.mode === "invalid" ? (
      <>
        {resolvedTarget}
        {" · "}
        {t("providerPill.mode.invalid")}
      </>
    ) : (
      resolvedTarget
    );

  return (
    <>
      {triggerIcon}
      <span className="min-w-0 truncate">{triggerText}</span>
      {modeMarker}
    </>
  );
}

/**
 * ProviderPillResolution 是弹层顶部「跟随 Agent 绑定」那一项的解析副行：箭头点出
 * 「解析到」，品牌标识让绑定的供应商一眼可认，模型 ID 单独走等宽（标识符不跟人读名
 * 一起排）。
 *
 * 解析不出供应商时回落纯文字（fallbackLabel）——宁可少画一个标识，也不画半个空标识。
 */
export function ProviderPillResolution({
  boundProviderType,
  boundProviderLabel,
  boundModelLabel,
  boundCliLogin,
  fallbackLabel,
}: {
  boundProviderType: string;
  boundProviderLabel: string;
  boundModelLabel: string;
  /** 确知该后端没绑供应商 → 下一轮由 CLI 自身的登录账号决定模型。 */
  boundCliLogin: boolean;
  fallbackLabel?: string;
}): React.ReactNode {
  const { t } = useTranslation();

  if (boundProviderType) {
    return (
      <span
        data-testid="special-resolution"
        className="flex min-w-0 items-center gap-1"
      >
        <span aria-hidden="true">→</span>
        <LlmProviderLogo
          providerType={boundProviderType}
          providerName={boundProviderLabel}
          className="size-3.5"
        />
        <span className="min-w-0 truncate">
          {boundProviderLabel}
          {boundModelLabel ? (
            <>
              {" · "}
              <span className="font-mono">{boundModelLabel}</span>
            </>
          ) : null}
        </span>
      </span>
    );
  }

  if (boundCliLogin) {
    // 确知没绑供应商:箭头保留(它解析成的就是「CLI 自身的登录账号」),但没有供应商
    // 可认领,不画标识。
    return (
      <span
        data-testid="special-resolution"
        className="flex min-w-0 items-center gap-1"
      >
        <span aria-hidden="true">→</span>
        <span className="min-w-0 truncate">
          {t("modelTargetPicker.special.backendSublabel")}
        </span>
      </span>
    );
  }

  return fallbackLabel || undefined;
}
