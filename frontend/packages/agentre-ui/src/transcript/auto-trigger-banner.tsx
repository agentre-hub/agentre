import * as React from "react";
import { BellRing, Radio } from "lucide-react";

import { useUiTranslation } from "../i18n";

// AutoTriggerBanner 渲染 transcript 内嵌的来源分隔卡片 —— 标记一条**非用户发起**的
// assistant 轮。它解释「为什么凭空多出一条 assistant 消息」。
//
// 视觉对齐 CompactBoundaryDivider(左右细线 + 中间 chip),与设计稿
// (~/Desktop/agentry.pen「Autonomous Turn」)一致。
//
// trigger 决定说哪一句 —— 非用户发起的轮不止一种,而它们在转录结构上一模一样:
//   - 缺省 / 其它取值:CLI 在 run_in_background 任务完成后自主跑的一轮。
//   - "external":子进程被 agentre 之外的东西叫醒、自己起的一轮(sess-3797;今天已知
//     的成因是别的 Claude 会话经 UDS 发来的消息,但判据不含成因,文案因此不点名是谁)。
//
// 未知取值退回缺省那句而不是空着或印出取值本身:后端将来多一种 trigger 时,用户最坏
// 只是少看到一句解释,不会看见一个内部标识。
export function AutoTriggerBanner({
  trigger,
}: {
  trigger?: string;
} = {}): React.ReactElement {
  const { t } = useUiTranslation();
  const external = trigger === "external";
  const Icon = external ? Radio : BellRing;
  return (
    <div
      className="flex w-full max-w-measure items-center gap-3 py-1"
      role="separator"
      aria-label={t(
        external
          ? "chatPanel.autonomous.externalAria"
          : "chatPanel.autonomous.aria",
      )}
    >
      <div className="h-px flex-1 bg-border" />
      <div className="flex items-center gap-1.5 rounded-full border border-border bg-muted px-3 py-1 text-aux text-muted-foreground">
        <Icon className="size-3 text-primary" aria-hidden="true" />
        <span className="font-medium">
          {t(
            external
              ? "chatPanel.autonomous.externalBanner"
              : "chatPanel.autonomous.banner",
          )}
        </span>
      </div>
      <div className="h-px flex-1 bg-border" />
    </div>
  );
}
