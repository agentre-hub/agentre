// 配额表的呈现层:Claude Code 5h / 7d 两个窗口的百分比、重置倒计时与 hover 面板。
//
// 数据来自 cc-usage-store,这里只负责渲染 —— 底栏那一行紧凑形状与 hover 展开的明细
// 面板共用同一套定级(usageLevel)与配色表。

import type { TFunction } from "i18next";
import { Timer } from "lucide-react";
import { useTranslation } from "react-i18next";

import {
  HoverCard,
  HoverCardContent,
  HoverCardTrigger,
  type UsageLevel,
  usageLevel,
} from "@agentre-hub/agentre-ui";
import { cn } from "@/lib/utils";

// formatResetIn 把"距离 ISO 时间点还有多久"渲染成紧凑的 XdYh / Xh / Xm 形式
// (e.g. "4d21h", "3h", "40m"),用于 QuotaMeter tooltip。
//   - 空串 / 无法解析的输入 → 空串(调用方自己决定是否显示括号)
//   - 已过期(diff<=0)→ "0m"
//   - <1h → "Nm"(向上取整,避免 30s 显示 0m)
//   - <24h → "Nh"(向下取整)
//   - >=24h → "XdYh"(Yh=0 时省略,写 "Xd")
// nowMs 可选(测试注入固定 now);省略走 Date.now()。
export function formatResetIn(value: unknown, nowMs?: number): string {
  if (value == null || value === "") return "";
  const target =
    value instanceof Date ? value.getTime() : Date.parse(String(value));
  if (Number.isNaN(target)) return "";
  const diffMs = target - (nowMs ?? Date.now());
  if (diffMs <= 0) return "0m";
  if (diffMs < 3_600_000) {
    return `${Math.max(1, Math.ceil(diffMs / 60_000))}m`;
  }
  const totalHours = Math.floor(diffMs / 3_600_000);
  const days = Math.floor(totalHours / 24);
  const hours = totalHours % 24;
  if (days <= 0) return `${hours}h`;
  if (hours === 0) return `${days}d`;
  return `${days}d${hours}h`;
}

// 配色表共用 quotaLevel 定级。文字色分表是因为"正常"态各处诉求不同(底栏配额要退到
// 背景里,面板与上下文里这个数字是主角);填充色三处一致,故只有一张表。
// 分表而不是拿 class 字符串去比较判断。
const QUOTA_METER_TONE: Record<UsageLevel, string> = {
  ok: "text-muted-foreground",
  warn: "text-status-waiting",
  danger: "text-status-error",
};
const QUOTA_PANEL_TONE: Record<UsageLevel, string> = {
  ok: "text-foreground",
  warn: "text-status-waiting",
  danger: "text-status-error",
};
const LEVEL_FILL_TONE: Record<UsageLevel, string> = {
  ok: "bg-primary",
  warn: "bg-status-waiting",
  danger: "bg-status-error",
};

const QUOTA_HOVER_OPEN_DELAY_MS = 200;
const QUOTA_HOVER_CLOSE_DELAY_MS = 100;

// QuotaMeter 展示 Claude Code 订阅的 5h / 7d 配额。数据由 chat-panel 通过 useCCUsage
// 拉取并传入(per-device, 不在这里订阅 store, 保证 Composer 可被纯 props 测试)。
//
// 渲染策略(与 cc_usage_svc.UsageState.reason 对齐):
//   - undefined / 空 reason / "no_credentials" → 整块不渲染(API key 用户、未首探)
//   - "ok" / "rate_limited"+stale / "network"+stale → 5h X% · 7d Y%(stale 不可见标记,只在面板脚注里提示)
//   - "auth_expired" / "device_offline" / "network"无stale → 灰态占位 "5h —%"
//
// 详情(重置倒计时 / Sonnet / Opus 拆分 / 异常态)在 HoverCard 面板里,不再用原生
// title —— 原生 title 不可键盘触达、不可着色、多行渲染跨平台不一致。
export function QuotaMeter({
  data,
  deviceLabel,
}: {
  data?: import("../../../../wailsjs/go/models").cc_usage_svc.UsageState;
  deviceLabel?: string;
}) {
  const { t } = useTranslation();
  if (!data || !data.reason) return null;
  if (data.reason === "no_credentials") return null;

  const showNumbers = data.data && (data.reason === "ok" || !!data.stale);
  const fiveH = data.data ? Math.round(data.data.fiveHourPercent) : null;
  const sevenD = data.data ? Math.round(data.data.weeklyPercent) : null;

  const offline =
    data.reason === "auth_expired" || data.reason === "device_offline";
  // 灰态占位没有可信数值,整块压成 subtle;有数值时两个窗口各自取色。
  const fiveTone = offline
    ? "text-muted-foreground"
    : QUOTA_METER_TONE[usageLevel(fiveH)];
  const sevenTone = offline
    ? "text-muted-foreground"
    : QUOTA_METER_TONE[usageLevel(sevenD)];

  return (
    <HoverCard
      openDelay={QUOTA_HOVER_OPEN_DELAY_MS}
      closeDelay={QUOTA_HOVER_CLOSE_DELAY_MS}
    >
      <HoverCardTrigger asChild>
        <button
          type="button"
          className={cn(
            // min-w-0 + 截断:计量器是底栏唯一的让位者(规格决策 11)。断点估窄时
            // 多出来的宽度只吃掉这里,绝不把发送按钮顶出可视区。
            "flex min-w-0 cursor-default items-center gap-1.5 overflow-hidden rounded-sm border border-transparent px-1 py-0.5 whitespace-nowrap",
            "font-mono text-meta tabular-nums transition-colors motion-reduce:transition-none",
            "hover:border-border hover:bg-accent",
            "focus-visible:border-border focus-visible:bg-accent focus-visible:outline-none",
            offline ? "text-muted-foreground" : "text-muted-foreground",
          )}
          aria-label={t("chat.quota.aria", {
            device: deviceLabel || "local",
            five: fiveH ?? "—",
            seven: sevenD ?? "—",
          })}
        >
          <Timer className="size-2.5 shrink-0" aria-hidden="true" />
          <span className={fiveTone}>
            {/* 窄档隐藏 5h/7d 前缀,只留两个百分比;语义由 aria-label 与面板保留。 */}
            <span
              data-quota-prefix="5h"
              className="@max-[800px]/composer:hidden"
            >
              5h{" "}
            </span>
            {showNumbers && fiveH !== null ? `${fiveH}%` : "—%"}
          </span>
          <span className="text-decorative-foreground">·</span>
          <span className={sevenTone}>
            <span
              data-quota-prefix="7d"
              className="@max-[800px]/composer:hidden"
            >
              7d{" "}
            </span>
            {showNumbers && sevenD !== null ? `${sevenD}%` : "—%"}
          </span>
        </button>
      </HoverCardTrigger>
      <HoverCardContent align="end" className="w-[268px] p-0">
        <QuotaPanel data={data} deviceLabel={deviceLabel} t={t} />
      </HoverCardContent>
    </HoverCard>
  );
}

// quotaFootnote 给面板脚注挑文案。正常态说明"百分比是已用比例",异常态
// (429 退避 / 网络错误 / OAuth 过期 / 设备离线)换成对应说明并着 waiting 色。
function quotaFootnote(
  reason: string,
  device: string,
  t: TFunction,
): { text: string; warn: boolean } {
  switch (reason) {
    case "rate_limited":
      return {
        text: t("chat.quota.title.rateLimited", { device }),
        warn: true,
      };
    case "network":
      return { text: t("chat.quota.title.network", { device }), warn: true };
    case "auth_expired":
      return {
        text: t("chat.quota.title.authExpired", { device }),
        warn: true,
      };
    case "device_offline":
      return {
        text: t("chat.quota.title.deviceOffline", { device }),
        warn: true,
      };
    default:
      return { text: t("chat.quota.panel.usedNote"), warn: false };
  }
}

// QuotaRow 是面板里的一行窗口:名称 + 重置倒计时 + 百分比 + 进度条。
function QuotaRow({
  label,
  percent,
  resetsAt,
  t,
}: {
  label: string;
  percent: number;
  resetsAt?: unknown;
  t: TFunction;
}) {
  const pct = Math.round(percent);
  const level = usageLevel(pct);
  const remaining = formatResetIn(resetsAt);
  return (
    <div className="flex flex-col gap-1">
      <div className="flex items-baseline gap-1.5 text-2xs">
        <span className="font-medium text-foreground">{label}</span>
        {remaining ? (
          <span className="font-mono text-muted-foreground">
            {t("chat.quota.resetRemaining", { time: remaining }).trim()}
          </span>
        ) : null}
        <span
          className={cn(
            "ml-auto font-mono tabular-nums",
            QUOTA_PANEL_TONE[level],
          )}
        >
          {pct}%
        </span>
      </div>
      <span className="h-1 overflow-hidden rounded-sm bg-border">
        <span
          className={cn("block h-1 rounded-sm", LEVEL_FILL_TONE[level])}
          style={{ width: `${Math.min(100, Math.max(0, pct))}%` }}
        />
      </span>
    </div>
  );
}

// QuotaPanel 是 HoverCard 的内容:标题 + 设备名 + 两个主窗口 + 可选的
// Sonnet / Opus 7 天分组 + 脚注。
function QuotaPanel({
  data,
  deviceLabel,
  t,
}: {
  data: import("../../../../wailsjs/go/models").cc_usage_svc.UsageState;
  deviceLabel?: string;
  t: TFunction;
}) {
  const device = deviceLabel || "local";
  const d = data.data;
  const foot = quotaFootnote(data.reason, device, t);
  const sonnet = d?.sonnetWeeklyPercent;
  const opus = d?.opusWeeklyPercent;
  return (
    <div>
      <div className="flex items-center gap-1.5 border-b border-border px-3 py-2">
        <Timer
          className="size-3.5 shrink-0 text-foreground"
          aria-hidden="true"
        />
        <span className="text-xs font-semibold text-foreground">
          {t("chat.quota.panel.title")}
        </span>
        <span className="ml-auto truncate font-mono text-2xs text-muted-foreground">
          {device}
        </span>
      </div>
      {d ? (
        <div className="flex flex-col gap-2.5 px-3 py-2.5">
          <QuotaRow
            label={t("chat.quota.panel.fiveHour")}
            percent={d.fiveHourPercent}
            resetsAt={d.fiveHourResetsAt}
            t={t}
          />
          <QuotaRow
            label={t("chat.quota.panel.weekly")}
            percent={d.weeklyPercent}
            resetsAt={d.weeklyResetsAt}
            t={t}
          />
          {sonnet != null || opus != null ? (
            <div className="flex flex-col gap-2 border-l-2 border-border pl-2.5">
              {sonnet != null ? (
                <QuotaRow
                  label={t("chat.quota.panel.sonnetWeekly")}
                  percent={sonnet}
                  resetsAt={d.sonnetWeeklyResetsAt}
                  t={t}
                />
              ) : null}
              {opus != null ? (
                <QuotaRow
                  label={t("chat.quota.panel.opusWeekly")}
                  percent={opus}
                  resetsAt={d.opusWeeklyResetsAt}
                  t={t}
                />
              ) : null}
            </div>
          ) : null}
        </div>
      ) : null}
      <div
        className={cn(
          "border-t border-border px-3 py-1.5 text-2xs",
          foot.warn
            ? "bg-status-waiting-bg text-status-waiting"
            : "bg-muted text-muted-foreground",
        )}
      >
        {foot.text}
      </div>
    </div>
  );
}
