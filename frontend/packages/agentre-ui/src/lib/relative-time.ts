import { AGENTRE_UI_NAMESPACE } from "../i18n";

/**
 * 「多久以前」的单一实现。
 *
 * 搬进来之前这套阶梯有四份：桌面端 `src/lib/relative-time.ts`（紧凑、无 i18n）、
 * `remote-devices/format.ts`（i18n，不足一分钟说「刚刚」）、`hooks-page.tsx` 里
 * 私有的那份（i18n，不足一分钟说「N 秒前」），以及 agentre-server 的
 * `lib/sessionView.ts`（`Intl.RelativeTimeFormat` + 每 locale 一个格式化器缓存）。
 * 四份的**档位阶梯**是同一套（60 秒 / 60 分 / 24 小时），差的只是三种输出形态，
 * 所以这里把阶梯收成 `relativeTimeBucket`，三种形态各是它上面的一层薄壳。
 *
 * 「不足一分钟」两种说法都保留：`justNow` 与 `secondsAgo` 是两句不同的话，不是
 * 同一句的两种写法——把 hooks 那份改成「刚刚」会让「刚跑完 3 秒」和「跑完 59 秒」
 * 变成同一行字。用 `options.seconds` 选。
 */

const MINUTE_MS = 60_000;
const HOUR_MS = 3_600_000;
const DAY_MS = 86_400_000;

export type RelativeTimeBucket =
  | { unit: "never" }
  | { unit: "seconds"; value: number }
  | { unit: "minutes"; value: number }
  | { unit: "hours"; value: number }
  | { unit: "days"; value: number };

/**
 * 阶梯本身。时间戳为 0（「从未发生过」的哨兵）给 `never`；未来的时间戳按 0 处理，
 * 与三份原实现的 `Math.max(0, now - then)` 一致。
 */
export function relativeTimeBucket(
  fromMs: number,
  nowMs: number,
): RelativeTimeBucket {
  if (!fromMs) return { unit: "never" };

  const delta = Math.max(0, nowMs - fromMs);
  if (delta < MINUTE_MS) {
    return { unit: "seconds", value: Math.floor(delta / 1_000) };
  }
  if (delta < HOUR_MS) {
    return { unit: "minutes", value: Math.floor(delta / MINUTE_MS) };
  }
  if (delta < DAY_MS) {
    return { unit: "hours", value: Math.floor(delta / HOUR_MS) };
  }
  return { unit: "days", value: Math.floor(delta / DAY_MS) };
}

/** 极窄的翻译口子：宿主的 `TFunction` 与包内 `useUiTranslation()` 的 `t` 都满足它。 */
export type RelativeTimeTranslate = (
  key: string,
  options?: { count: number },
) => string;

const KEY_BY_UNIT: Record<RelativeTimeBucket["unit"], string> = {
  never: "relativeTime.never",
  seconds: "relativeTime.secondsAgo",
  minutes: "relativeTime.minutesAgo",
  hours: "relativeTime.hoursAgo",
  days: "relativeTime.daysAgo",
};

/**
 * key 一律带包的 namespace 前缀：宿主传进来的 `t` 绑在它自己的默认 namespace
 * （桌面端是 `common`）上，不带前缀会解析不到这几条文案而原样吐出 key。
 */
function uiKey(key: string): string {
  return `${AGENTRE_UI_NAMESPACE}:${key}`;
}

export type FormatRelativeTimeOptions = {
  /** true = 不足一分钟说「N 秒前」（hooks 页那份的口径）；默认说「刚刚」。 */
  seconds?: boolean;
};

/** i18n 形态。文案在包的 `relativeTime.*` 下，两个宿主都从这一份取。 */
export function formatRelativeTime(
  fromMs: number,
  nowMs: number,
  t: RelativeTimeTranslate,
  options?: FormatRelativeTimeOptions,
): string {
  const bucket = relativeTimeBucket(fromMs, nowMs);

  if (bucket.unit === "never") return t(uiKey(KEY_BY_UNIT.never));
  if (bucket.unit === "seconds" && !options?.seconds) {
    return t(uiKey("relativeTime.justNow"));
  }

  return t(uiKey(KEY_BY_UNIT[bucket.unit]), { count: bucket.value });
}

/**
 * 紧凑形态（`now` / `3m` / `2h` / `4d`），不进 i18n：它挂在会话行、标签页 tooltip
 * 这类一眼扫过的位置，与旁边的时间戳同属动态内容。
 */
export function formatCompactRelativeTime(
  fromMs: number,
  nowMs: number = Date.now(),
): string {
  if (!fromMs || fromMs <= 0) return "";

  const bucket = relativeTimeBucket(fromMs, nowMs);
  switch (bucket.unit) {
    case "never":
      return "";
    case "seconds":
      return "now";
    case "minutes":
      return `${bucket.value}m`;
    case "hours":
      return `${bucket.value}h`;
    default:
      return `${bucket.value}d`;
  }
}

const INTL_UNITS: [Intl.RelativeTimeFormatUnit, number][] = [
  ["year", 365 * DAY_MS],
  ["month", 30 * DAY_MS],
  ["day", DAY_MS],
  ["hour", HOUR_MS],
  ["minute", MINUTE_MS],
];

/**
 * 每个 locale 的格式化器只构造一次。
 *
 * `Intl.*Format` 的**构造**是 Intl API 里最贵的一步，远贵于 `.format()`。会话索引
 * 每一行都调一次，而索引会因为搜索框每敲一个字符、每 30 秒的兜底轮询、每条
 * mirror_changed 信号而整体重渲染——200 行就是每次重渲染 200 次构造。
 *
 * 键就是 locale 本身：选项（`numeric: "auto"`）是写死的，没有第二种组合。
 */
const intlFormatters = new Map<string, Intl.RelativeTimeFormat>();

function intlFormatter(locale: string): Intl.RelativeTimeFormat {
  const cached = intlFormatters.get(locale);
  if (cached) return cached;

  const formatter = new Intl.RelativeTimeFormat(locale, { numeric: "auto" });
  intlFormatters.set(locale, formatter);
  return formatter;
}

/**
 * `Intl` 形态：年/月/日/时/分五档，且能表达「将来」。用它而不是 `t(...)` 的理由与
 * 紧凑形态一样——渲染的是动态时刻，不是静态 UI 文案；locale 由调用方传入。
 */
export function formatIntlRelativeTime(
  ms: number,
  locale: string,
  now: number = Date.now(),
): string {
  const diff = ms - now;
  const abs = Math.abs(diff);
  const formatter = intlFormatter(locale);

  for (const [unit, span] of INTL_UNITS) {
    if (abs >= span) return formatter.format(Math.round(diff / span), unit);
  }
  return formatter.format(0, "minute");
}
