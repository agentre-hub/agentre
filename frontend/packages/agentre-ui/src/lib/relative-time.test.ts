import { describe, expect, it } from "vitest";

import { AGENTRE_UI_NAMESPACE, agentreUiResources } from "../i18n";

import {
  formatCompactRelativeTime,
  formatIntlRelativeTime,
  formatRelativeTime,
  relativeTimeBucket,
} from "./relative-time";

const NOW = 1_700_000_000_000;

/**
 * 三个宿主实现的**既有输出**就是这里的判据。
 *
 * 表里的值不是照着本文件旁边那份新实现写的，而是在搬迁前把桌面端原有的三份
 * 实现（`src/lib/relative-time.ts` 的紧凑档、`remote-devices/format.ts` 的
 * i18n 档、`hooks-page.tsx` 里私有的秒级档）跑在同一组 delta 上 dump 下来的。
 * 所以这张表在新实现写错时会红——它不是从新实现推出来的。
 */
const DELTAS = [
  0, 1, 999, 1_000, 59_000, 60_000, 90_000, 3_540_000, 3_600_000, 82_800_000,
  86_400_000, 8_640_000_000,
];

const COMPACT_BASELINE = [
  "",
  "now",
  "now",
  "now",
  "now",
  "1m",
  "1m",
  "59m",
  "1h",
  "23h",
  "1d",
  "100d",
];

const JUST_NOW_BASELINE_ZH = [
  "从未",
  "刚刚",
  "刚刚",
  "刚刚",
  "刚刚",
  "1 分钟前",
  "1 分钟前",
  "59 分钟前",
  "1 小时前",
  "23 小时前",
  "1 天前",
  "100 天前",
];

const SECONDS_BASELINE_ZH = [
  "从未",
  "0 秒前",
  "0 秒前",
  "1 秒前",
  "59 秒前",
  "1 分钟前",
  "1 分钟前",
  "59 分钟前",
  "1 小时前",
  "23 小时前",
  "1 天前",
  "100 天前",
];

const JUST_NOW_BASELINE_EN = [
  "never",
  "just now",
  "just now",
  "just now",
  "just now",
  "1m ago",
  "1m ago",
  "59m ago",
  "1h ago",
  "23h ago",
  "1d ago",
  "100d ago",
];

const SECONDS_BASELINE_EN = [
  "never",
  "0s ago",
  "0s ago",
  "1s ago",
  "59s ago",
  "1m ago",
  "1m ago",
  "59m ago",
  "1h ago",
  "23h ago",
  "1d ago",
  "100d ago",
];

/** 直接读语言包做插值，避开 i18next 实例，判据仍是包自己的那份文案。 */
function translator(language: "zh-CN" | "en") {
  return (key: string, options?: { count?: number }) => {
    const path = key.startsWith(`${AGENTRE_UI_NAMESPACE}:`)
      ? key.slice(AGENTRE_UI_NAMESPACE.length + 1)
      : key;
    const value = path
      .split(".")
      .reduce<unknown>(
        (node, part) => (node as Record<string, unknown>)?.[part],
        agentreUiResources[language],
      );
    if (typeof value !== "string") return key;
    return value.replace("{{count}}", String(options?.count ?? ""));
  };
}

function at(delta: number): number {
  return delta === 0 ? 0 : NOW - delta;
}

describe("relativeTimeBucket", () => {
  it("Given 时间戳为 0, When 取档位, Then 是「从未」而不是 0 秒前", () => {
    expect(relativeTimeBucket(0, NOW)).toEqual({ unit: "never" });
  });

  it("Given 未来的时间戳, When 取档位, Then 与三份原实现一样把差值夹到 0", () => {
    expect(relativeTimeBucket(NOW + 5_000, NOW)).toEqual({
      unit: "seconds",
      value: 0,
    });
  });

  it("Given 各档边界, When 取档位, Then 60s / 60m / 24h 三道坎与原实现一致", () => {
    expect(relativeTimeBucket(NOW - 59_999, NOW)).toEqual({
      unit: "seconds",
      value: 59,
    });
    expect(relativeTimeBucket(NOW - 60_000, NOW)).toEqual({
      unit: "minutes",
      value: 1,
    });
    expect(relativeTimeBucket(NOW - 3_599_999, NOW)).toEqual({
      unit: "minutes",
      value: 59,
    });
    expect(relativeTimeBucket(NOW - 3_600_000, NOW)).toEqual({
      unit: "hours",
      value: 1,
    });
    expect(relativeTimeBucket(NOW - 86_399_999, NOW)).toEqual({
      unit: "hours",
      value: 23,
    });
    expect(relativeTimeBucket(NOW - 86_400_000, NOW)).toEqual({
      unit: "days",
      value: 1,
    });
  });
});

describe("formatCompactRelativeTime", () => {
  it("Given 搬迁前 lib/relative-time.ts 的输出表, When 逐个 delta 重跑, Then 逐项一致", () => {
    expect(
      DELTAS.map((delta) => formatCompactRelativeTime(at(delta), NOW)),
    ).toEqual(COMPACT_BASELINE);
  });

  it("Given 负数或 0 时间戳, When 格式化, Then 与原实现一样给空串", () => {
    expect(formatCompactRelativeTime(-1, NOW)).toBe("");
    expect(formatCompactRelativeTime(0, NOW)).toBe("");
  });
});

describe("formatRelativeTime", () => {
  it("Given 搬迁前 remote-devices 那份的输出表, When 不开秒档重跑, Then 中英逐项一致", () => {
    expect(
      DELTAS.map((delta) =>
        formatRelativeTime(at(delta), NOW, translator("zh-CN")),
      ),
    ).toEqual(JUST_NOW_BASELINE_ZH);
    expect(
      DELTAS.map((delta) =>
        formatRelativeTime(at(delta), NOW, translator("en")),
      ),
    ).toEqual(JUST_NOW_BASELINE_EN);
  });

  it("Given 搬迁前 hooks-page 那份的输出表, When 开秒档重跑, Then 中英逐项一致", () => {
    expect(
      DELTAS.map((delta) =>
        formatRelativeTime(at(delta), NOW, translator("zh-CN"), {
          seconds: true,
        }),
      ),
    ).toEqual(SECONDS_BASELINE_ZH);
    expect(
      DELTAS.map((delta) =>
        formatRelativeTime(at(delta), NOW, translator("en"), { seconds: true }),
      ),
    ).toEqual(SECONDS_BASELINE_EN);
  });

  it("Given 宿主的 t 绑在自己的默认 namespace 上, When 格式化, Then key 带包的 namespace 前缀", () => {
    const seen: string[] = [];
    formatRelativeTime(NOW - 60_000, NOW, (key: string) => {
      seen.push(key);
      return key;
    });

    expect(seen).toEqual([`${AGENTRE_UI_NAMESPACE}:relativeTime.minutesAgo`]);
  });
});

describe("formatIntlRelativeTime", () => {
  it("Given locale 与 delta, When 格式化, Then 与 Intl.RelativeTimeFormat 的口径一致", () => {
    const oracle = new Intl.RelativeTimeFormat("zh-CN", { numeric: "auto" });

    expect(formatIntlRelativeTime(NOW - 2 * 60_000, "zh-CN", NOW)).toBe(
      oracle.format(-2, "minute"),
    );
    expect(formatIntlRelativeTime(NOW - 3 * 3_600_000, "zh-CN", NOW)).toBe(
      oracle.format(-3, "hour"),
    );
    expect(formatIntlRelativeTime(NOW + 2 * 86_400_000, "zh-CN", NOW)).toBe(
      oracle.format(2, "day"),
    );
  });

  it("Given 不足一分钟的差值, When 格式化, Then 回落到 0 分钟那一档", () => {
    const oracle = new Intl.RelativeTimeFormat("en", { numeric: "auto" });

    expect(formatIntlRelativeTime(NOW - 1_000, "en", NOW)).toBe(
      oracle.format(0, "minute"),
    );
  });

  it("Given 同一个 locale 被反复格式化, When 计数构造次数, Then 每个 locale 只构造一次", () => {
    const RealFormat = Intl.RelativeTimeFormat;
    let constructed = 0;
    const counting = function (
      locale?: Intl.LocalesArgument,
      options?: Intl.RelativeTimeFormatOptions,
    ) {
      constructed += 1;
      return new RealFormat(locale as string, options);
    } as unknown as typeof Intl.RelativeTimeFormat;

    Object.defineProperty(Intl, "RelativeTimeFormat", {
      configurable: true,
      writable: true,
      value: counting,
    });
    try {
      formatIntlRelativeTime(NOW - 60_000, "de", NOW);
      formatIntlRelativeTime(NOW - 120_000, "de", NOW);
      formatIntlRelativeTime(NOW - 60_000, "fr", NOW);
    } finally {
      Object.defineProperty(Intl, "RelativeTimeFormat", {
        configurable: true,
        writable: true,
        value: RealFormat,
      });
    }

    expect(constructed).toBe(2);
  });
});
