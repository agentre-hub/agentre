import { describe, expect, it } from "vitest";

import { AGENTRE_UI_NAMESPACE, agentreUiResources } from "../i18n";

import {
  ICON_VOCABULARY,
  hasIcon,
  iconForKey,
  iconList,
  iconMeta,
  iconsByCategory,
  searchIcons,
} from "./icon-registry";

/**
 * 判据取自搬迁前**桌面端 `components/agentre/icon-registry.ts` 的实测输出**
 * （同一台机器上把旧表 dump 下来），不是照着这里的新实现写的。顺序与 key 逐条
 * 对齐 agentre-server 的 `ORG_ICONS` —— 两边存的是同一个 `avatar_icon` 值。
 *
 * 唯一一处**故意不一致**：`bug` 的中文。旧桌面表把它写死成字面量 "Bug"（那一格
 * 从来没进过语言包），server 那份有 `缺陷`。合并取 i18n 的那一份。
 */
const BASELINE_KEY_ORDER = [
  "hammer",
  "code-xml",
  "terminal",
  "wrench",
  "cpu",
  "network",
  "palette",
  "brush",
  "paint-bucket",
  "layers",
  "ruler",
  "sparkles",
  "brain",
  "bot",
  "wand-sparkles",
  "beaker",
  "bug",
  "shield-check",
  "scale",
  "rocket",
  "gauge",
  "database",
  "chart-line",
  "package",
  "puzzle",
  "target",
  "flag",
  "compass",
  "megaphone",
  "eye",
];

const BASELINE_ZH_LABEL: [string, string][] = [
  ["hammer", "锤子"],
  ["code-xml", "代码"],
  ["terminal", "终端"],
  ["wrench", "扳手"],
  ["cpu", "处理器"],
  ["network", "网络"],
  ["palette", "调色板"],
  ["brush", "画笔"],
  ["paint-bucket", "油漆桶"],
  ["layers", "图层"],
  ["ruler", "尺子"],
  ["sparkles", "灵感"],
  ["brain", "大脑"],
  ["bot", "机器人"],
  ["wand-sparkles", "魔法棒"],
  ["beaker", "烧杯"],
  ["bug", "Bug"],
  ["shield-check", "安全"],
  ["scale", "天平"],
  ["rocket", "发射"],
  ["gauge", "仪表"],
  ["database", "数据库"],
  ["chart-line", "图表"],
  ["package", "包"],
  ["puzzle", "拼图"],
  ["target", "目标"],
  ["flag", "旗帜"],
  ["compass", "指南"],
  ["megaphone", "广播"],
  ["eye", "查看"],
];

const BASELINE_EN_LABEL: [string, string][] = [
  ["hammer", "Hammer"],
  ["code-xml", "Code"],
  ["terminal", "Terminal"],
  ["wrench", "Wrench"],
  ["cpu", "Processor"],
  ["network", "Network"],
  ["palette", "Palette"],
  ["brush", "Brush"],
  ["paint-bucket", "Paint Bucket"],
  ["layers", "Layers"],
  ["ruler", "Ruler"],
  ["sparkles", "Sparkles"],
  ["brain", "Brain"],
  ["bot", "Bot"],
  ["wand-sparkles", "Magic Wand"],
  ["beaker", "Beaker"],
  ["bug", "Bug"],
  ["shield-check", "Security"],
  ["scale", "Scale"],
  ["rocket", "Launch"],
  ["gauge", "Gauge"],
  ["database", "Database"],
  ["chart-line", "Chart"],
  ["package", "Package"],
  ["puzzle", "Puzzle"],
  ["target", "Target"],
  ["flag", "Flag"],
  ["compass", "Compass"],
  ["megaphone", "Broadcast"],
  ["eye", "View"],
];

const BASELINE_CATEGORY: [string, string][] = [
  ["hammer", "engineering"],
  ["code-xml", "engineering"],
  ["terminal", "engineering"],
  ["wrench", "engineering"],
  ["cpu", "engineering"],
  ["network", "engineering"],
  ["palette", "design"],
  ["brush", "design"],
  ["paint-bucket", "design"],
  ["layers", "design"],
  ["ruler", "design"],
  ["sparkles", "ai"],
  ["brain", "ai"],
  ["bot", "ai"],
  ["wand-sparkles", "ai"],
  ["beaker", "qa"],
  ["bug", "qa"],
  ["shield-check", "qa"],
  ["scale", "qa"],
  ["rocket", "ops"],
  ["gauge", "ops"],
  ["database", "ops"],
  ["chart-line", "ops"],
  ["package", "ops"],
  ["puzzle", "general"],
  ["target", "general"],
  ["flag", "general"],
  ["compass", "general"],
  ["megaphone", "general"],
  ["eye", "general"],
];

/** 直接读语言包做解析，判据仍是包自己的那份文案。 */
function translator(language: "zh-CN" | "en") {
  return (key: string) => {
    const path = key.startsWith(`${AGENTRE_UI_NAMESPACE}:`)
      ? key.slice(AGENTRE_UI_NAMESPACE.length + 1)
      : key;
    const value = path
      .split(".")
      .reduce<unknown>(
        (node, part) => (node as Record<string, unknown>)?.[part],
        agentreUiResources[language],
      );
    return typeof value === "string" ? value : key;
  };
}

describe("图标词表", () => {
  it("Given 搬迁前的 key 顺序, When 读词表, Then 逐条一致（这串 key 是持久化值）", () => {
    expect(ICON_VOCABULARY.map((entry) => entry.key)).toEqual(
      BASELINE_KEY_ORDER,
    );
  });

  it("Given 每个 key, When 读分类, Then 与搬迁前一致", () => {
    expect(ICON_VOCABULARY.map((entry) => [entry.key, entry.category])).toEqual(
      BASELINE_CATEGORY,
    );
  });

  it('Given 中文, When 取标签, Then 与搬迁前一致，且 bug 从写死的 "Bug" 改为语言包里的中文', () => {
    expect(
      iconList(translator("zh-CN")).map((meta) => [meta.key, meta.label]),
    ).toEqual(
      BASELINE_ZH_LABEL.map(([key, label]) =>
        key === "bug" ? [key, "缺陷"] : [key, label],
      ),
    );
    // 旧表那一格是字面量，这条钉住「改的是它、且只改了它」。
    expect(BASELINE_ZH_LABEL.find(([key]) => key === "bug")?.[1]).toBe("Bug");
  });

  it("Given 英文, When 取标签, Then 与搬迁前逐条一致", () => {
    expect(
      iconList(translator("en")).map((meta) => [meta.key, meta.label]),
    ).toEqual(BASELINE_EN_LABEL);
  });

  it("Given 语言从英文切到中文, When 再读一次, Then 文案跟着变（表在模块求值时不取文案）", () => {
    const before = iconMeta("hammer", translator("en"))?.label;
    const after = iconMeta("hammer", translator("zh-CN"))?.label;

    expect(before).toBe(agentreUiResources.en.iconRegistry.icons.hammer);
    expect(after).toBe(agentreUiResources["zh-CN"].iconRegistry.icons.hammer);
  });

  it("Given 分类表, When 按分类分组, Then 顺序、标签与每组条数与搬迁前一致", () => {
    expect(
      iconsByCategory(translator("zh-CN")).map((group) => [
        group.category,
        group.label,
        group.items.length,
      ]),
    ).toEqual([
      ["engineering", "工程", 6],
      ["design", "设计", 5],
      ["ai", "AI", 4],
      ["qa", "测试", 4],
      ["ops", "运维", 5],
      ["general", "通用", 6],
    ]);
  });

  it("Given 搜索词, When 匹配 key / 标签 / 别名, Then 与搬迁前一致", () => {
    const t = translator("en");

    expect(searchIcons("hammer", t).map((meta) => meta.key)).toEqual([
      "hammer",
    ]);
    // 字面量别名
    expect(searchIcons("build", t).map((meta) => meta.key)).toEqual(["hammer"]);
    // 随语言变的别名
    expect(
      searchIcons(
        agentreUiResources["zh-CN"].iconRegistry.aliases.construction,
        translator("zh-CN"),
      ).map((meta) => meta.key),
    ).toContain("hammer");
    expect(searchIcons("", t)).toHaveLength(30);
  });

  it("Given 不认得的 key 或空值, When 取图标, Then 退回拼图而不是抛错", () => {
    expect(iconForKey("hammer")).toBe(
      ICON_VOCABULARY.find((entry) => entry.key === "hammer")?.icon,
    );
    expect(iconForKey("nope")).toBe(
      ICON_VOCABULARY.find((entry) => entry.key === "puzzle")?.icon,
    );
    expect(iconForKey(null)).toBe(
      ICON_VOCABULARY.find((entry) => entry.key === "puzzle")?.icon,
    );
    expect(hasIcon("hammer")).toBe(true);
    expect(hasIcon("nope")).toBe(false);
    expect(hasIcon("")).toBe(false);
    expect(iconMeta(null, translator("en"))).toBeUndefined();
    expect(iconMeta("nope", translator("en"))).toBeUndefined();
  });

  it("Given 语言包, When 逐条查词表引用的 key, Then 中英两份都解析得到", () => {
    const missing: string[] = [];
    for (const language of ["zh-CN", "en"] as const) {
      const t = translator(language);
      for (const entry of ICON_VOCABULARY) {
        const qualified = `${AGENTRE_UI_NAMESPACE}:${entry.labelKey}`;
        if (t(qualified) === qualified)
          missing.push(`${language} ${entry.labelKey}`);
        for (const alias of entry.aliases) {
          if (!("labelKey" in alias)) continue;
          const aliasKey = `${AGENTRE_UI_NAMESPACE}:${alias.labelKey}`;
          if (t(aliasKey) === aliasKey)
            missing.push(`${language} ${alias.labelKey}`);
        }
      }
    }

    expect(missing).toEqual([]);
  });
});
