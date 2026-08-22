import * as React from "react";

import { cn } from "../lib/utils";

/**
 * 「这条对话现在怎么了」的横幅外壳。
 *
 * 结构固定为**一行结论 + 一行后果 + 至多一个动作**（agentre-server 规格
 * 2026-08-21「连接态与失败态」决策 3）。「至多一个」是有意的下限：需要两个以上的
 * 选择说明这一屏该开一个对话框，而不是在横幅里摆一排按钮——所以 `action` 是单个
 * 节点而不是数组。
 *
 * 三档 tone 按**用户还能做什么**分，不按信号来自哪一层：
 *   - `alarm`   目标彻底够不着（机器不在 / App 没开 / 连接断了）→ 要去处理
 *   - `limited` 读得了、写不了 → 会自己恢复，等
 *   - `settled` 终态（设备被移除 / 已登出）→ 既成事实，中性色
 *
 * 三者共用一个 token 家族的话，用户就分不出「去处理」「等它恢复」「接受它」。
 * 尤其是终态**不用红**：既成事实不是警报，红色会让人以为再试试也许能好。
 *
 * `sticky` 是 prop 而不是写死：agentre-server 把它吸在转录滚动带顶部（往下读一屏，
 * 输入框为什么灰着的那句解释还在），桌面端那张挂在聊天头下面、不跟着滚。
 */
export type StatusBannerTone = "alarm" | "limited" | "settled";

const TONE_CLASS: Record<StatusBannerTone, string> = {
  alarm: "border-destructive/35 bg-destructive-soft text-destructive-text",
  limited:
    "border-status-waiting/40 bg-status-waiting-bg text-status-waiting-text",
  settled: "border-border-strong bg-secondary text-foreground",
};

export type StatusBannerProps = {
  tone: StatusBannerTone;
  /** 一行结论。 */
  title: React.ReactNode;
  /** 一行后果。 */
  body: React.ReactNode;
  /** 接在正文后面的附加事实（如「最后在线 X」）。取不到就别传，不编。 */
  meta?: React.ReactNode;
  /** 行首图标。画什么由调用方定——它跟着状态走，不跟着 tone 走。 */
  icon?: React.ReactNode;
  /**
   * 至多一个动作。「按下去会发生什么」始终由宿主给：两端路由不同，本包不认识
   * 任何一端的导航。
   */
  action?: React.ReactNode;
  sticky?: boolean;
  className?: string;
};

export function StatusBanner({
  tone,
  title,
  body,
  meta,
  icon,
  action,
  sticky = false,
  className,
}: StatusBannerProps) {
  return (
    // 外层只做容器查询的锚：同一个横幅会出现在 320px 左列、移动全宽、桌面详情区
    // 三种宽度里，跟视口没有固定对应关系，所以 @md 查的是它自己这一格。
    <div className="@container">
      <div
        role="alert"
        aria-live="assertive"
        data-tone={tone}
        className={cn(
          "grid grid-cols-[1rem_1fr] items-start gap-x-2.5 gap-y-1 rounded-lg border px-3 py-2.5",
          "@md:grid-cols-[1rem_1fr_auto]",
          sticky && "sticky top-0 z-10",
          TONE_CLASS[tone],
          className,
        )}
      >
        <span className="mt-0.5 flex size-4 shrink-0 items-center justify-center">
          {icon}
        </span>
        <p
          data-testid="status-banner-title"
          className="min-w-0 text-[12.5px] font-semibold leading-snug"
        >
          {title}
        </p>
        <p
          data-testid="status-banner-body"
          className="col-start-2 min-w-0 text-[11.5px] leading-relaxed text-muted-foreground"
        >
          {body}
          {meta}
        </p>
        {action && (
          <div
            data-testid="status-banner-action"
            // 窄容器下动作落到自己一行并铺满：桌面那种「右侧一个小按钮」在 372px
            // 下会把标题挤断。
            className="col-start-2 mt-1 flex w-full @md:col-start-3 @md:row-span-2 @md:mt-0 @md:w-auto @md:self-center"
          >
            {action}
          </div>
        )}
      </div>
    </div>
  );
}
