import { Gauge } from "lucide-react";

import { useUiTranslation } from "../i18n";
import { formatTokens } from "../lib/format-tokens";
import { cn } from "../lib/utils";
import {
  HoverCard,
  HoverCardContent,
  HoverCardTrigger,
} from "../ui/hover-card";
import { usageLevel, type UsageLevel } from "./usage-level";

// 环的几何：14px 外径 + 2.5px 描边。端点用 butt 而不是 round —— round 在这个描边
// 宽度下两端各多吃约 4% 弧长，94% 会画成一个闭合的圆，恰好在最该读准的那一档失真。
const RING_SIZE = 14;
const RING_STROKE = 2.5;

const HOVER_OPEN_DELAY_MS = 200;
const HOVER_CLOSE_DELAY_MS = 100;

const LEVEL_STROKE_TONE: Record<UsageLevel, string> = {
  ok: "stroke-primary",
  warn: "stroke-status-waiting",
  danger: "stroke-status-error",
};

// 文字色分表：上下文的「正常」态用 primary-text（它是底栏里唯一常驻的定量信息，
// 该被看见），告警两档与配额一致。
const LEVEL_TEXT_TONE: Record<UsageLevel, string> = {
  ok: "text-primary-text",
  warn: "text-status-waiting",
  danger: "text-status-error",
};

export type ContextMeterProps = {
  /** 已用 token。小于 0 时按 0 处理，不画负弧。 */
  used: number;
  /**
   * 上下文窗口上限。
   *
   * `max <= 0`（窗口还没探到）时**由宿主决定不渲染这个组件**，这里不接管那个判断：
   * 「窗口未知」是宿主的数据状态，不是计量器的一种外观。
   */
  max: number;
  /** 宿主的测试抓手。包不预设任何一端的测试约定，要用就自己传。 */
  dataTestId?: string;
};

/**
 * 上下文用量计量器：底栏一枚 14px 的环 + 百分比，token 绝对值放进悬停浮窗。
 *
 * 环取代了早期那条 `h-1 w-24` 的线性条：那条要 96px，是底栏第二宽的元素，而且窄档
 * 整条隐藏 —— 最需要图形提示的时候反而没有图形。环只占 14px，可以全档常驻。
 *
 * 浮窗**只读**：不放「压缩 / 清空上下文」之类的动作入口。
 */
export function ContextMeter({ used, max, dataTestId }: ContextMeterProps) {
  const { t } = useUiTranslation();
  const safeUsed = Math.max(0, used);
  const ratio = max > 0 ? Math.min(1, safeUsed / max) : 0;
  const pct = Math.round(ratio * 100);
  // 传 ratio*100 而不是取整后的 pct：0.895 仍算 warning，不因四舍五入跳成 danger。
  const level = usageLevel(ratio * 100);

  return (
    <HoverCard
      openDelay={HOVER_OPEN_DELAY_MS}
      closeDelay={HOVER_CLOSE_DELAY_MS}
    >
      <HoverCardTrigger asChild>
        {/* 触发器必须是可聚焦的 button：token 绝对值已经降级成「悬停才拿得到」，
            span 会让键盘用户永远读不到它们。 */}
        <button
          type="button"
          data-testid={dataTestId}
          className={cn(
            "flex min-w-0 cursor-default items-center gap-1.5 overflow-hidden rounded-sm border border-transparent px-1 py-0.5 whitespace-nowrap",
            "font-mono text-meta tabular-nums transition-colors motion-reduce:transition-none",
            "hover:border-border hover:bg-accent",
            "focus-visible:border-border focus-visible:bg-accent focus-visible:outline-none",
          )}
          aria-label={t("contextMeter.aria", {
            max: formatTokens(max),
            percent: pct,
            used: formatTokens(safeUsed),
          })}
        >
          <ContextRing used={safeUsed} max={max} pct={pct} level={level} />
          <span
            className={cn("font-medium tabular-nums", LEVEL_TEXT_TONE[level])}
          >
            {pct}%
          </span>
        </button>
      </HoverCardTrigger>
      <HoverCardContent align="end" className="w-[228px] p-0">
        <ContextPanel used={safeUsed} max={max} pct={pct} level={level} />
      </HoverCardContent>
    </HoverCard>
  );
}

function ContextRing({
  used,
  max,
  pct,
  level,
}: {
  used: number;
  max: number;
  pct: number;
  level: UsageLevel;
}) {
  const radius = (RING_SIZE - RING_STROKE) / 2;
  const circumference = 2 * Math.PI * radius;
  const center = RING_SIZE / 2;
  return (
    <svg
      className="-rotate-90 shrink-0"
      width={RING_SIZE}
      height={RING_SIZE}
      viewBox={`0 0 ${RING_SIZE} ${RING_SIZE}`}
      role="progressbar"
      aria-valuemin={0}
      aria-valuemax={max}
      aria-valuenow={Math.min(used, max)}
    >
      <circle
        className="fill-none stroke-border"
        cx={center}
        cy={center}
        r={radius}
        strokeWidth={RING_STROKE}
      />
      <circle
        data-slot="context-ring-arc"
        className={cn(
          "fill-none transition-[stroke-dashoffset]",
          LEVEL_STROKE_TONE[level],
        )}
        cx={center}
        cy={center}
        r={radius}
        strokeWidth={RING_STROKE}
        strokeLinecap="butt"
        strokeDasharray={circumference}
        strokeDashoffset={circumference * (1 - Math.min(1, pct / 100))}
      />
    </svg>
  );
}

function ContextPanel({
  used,
  max,
  pct,
  level,
}: {
  used: number;
  max: number;
  pct: number;
  level: UsageLevel;
}) {
  const { t } = useUiTranslation();
  const remaining = Math.max(0, max - used);
  const tone = LEVEL_TEXT_TONE[level];
  return (
    <div>
      <div className="flex items-center gap-1.5 border-b border-border px-3 py-2">
        <Gauge
          className="size-3.5 shrink-0 text-foreground"
          aria-hidden="true"
        />
        <span className="text-xs font-semibold text-foreground">
          {t("contextMeter.panel.title")}
        </span>
      </div>
      <div className="flex flex-col gap-2 px-3 py-2.5">
        <div className="flex items-baseline gap-1 font-mono tabular-nums">
          <span className="text-sm font-semibold text-foreground">
            {formatTokens(used)}
          </span>
          <span className="text-2xs text-muted-foreground">
            / {formatTokens(max)}
          </span>
          <span className={cn("ml-auto text-2xs font-medium", tone)}>
            {pct}%
          </span>
        </div>
        <div className="flex items-baseline gap-2 border-t border-border pt-2 text-2xs">
          <span className="font-medium text-foreground">
            {t("contextMeter.panel.remaining")}
          </span>
          <span className={cn("ml-auto font-mono tabular-nums", tone)}>
            {formatTokens(remaining)}
          </span>
        </div>
      </div>
      <div
        className={cn(
          "border-t border-border px-3 py-1.5 text-2xs",
          level === "ok"
            ? "bg-muted text-muted-foreground"
            : "bg-status-waiting-bg text-status-waiting-text",
        )}
      >
        {level === "ok"
          ? t("contextMeter.panel.note")
          : t("contextMeter.panel.nearLimit")}
      </div>
    </div>
  );
}
