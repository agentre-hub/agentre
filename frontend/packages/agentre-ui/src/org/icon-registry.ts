import {
  Beaker,
  Bot,
  Brain,
  Brush,
  Bug,
  CodeXml,
  Compass,
  Cpu,
  Database,
  Eye,
  Flag,
  Gauge,
  Hammer,
  Layers,
  LineChart,
  Megaphone,
  Network,
  Package,
  PaintBucket,
  Palette,
  Puzzle,
  Rocket,
  Ruler,
  Scale,
  ShieldCheck,
  Sparkles,
  Target,
  Terminal,
  WandSparkles,
  Wrench,
} from "lucide-react";
import type { LucideIcon } from "lucide-react";

import { AGENTRE_UI_NAMESPACE } from "../i18n";

/**
 * 图标词表 —— Agent 头像、部门图标、项目字形共用这一份。
 *
 * 这里的 `key` 是**持久化值**：`avatar_icon` / 部门 `icon` 存的就是这串 key，桌面端
 * 与浏览器宿主同步的是同一行数据。两边各抄一份清单的后果不是「样式不一致」，
 * 而是一边认得、另一边渲染成空头像，所以清单只留这一份。
 *
 * **渲染留在宿主**：桌面端的头像还有「上传图片」这一档（浏览器宿主的读写端点
 * 都不带头像正文，画一个上传按钮就是伪造后端），两边的选择器长得并不一样。
 * 包只出「有哪些 key、按什么顺序、叫什么名字、画哪枚 lucide 图标」。
 *
 * 文案一律是 **key 而不是取好的字符串**：这张表在模块求值时就建好，此刻取文案
 * 等于把语言钉死在「模块第一次被 import 的那一刻」。求值推迟到 `iconList(t)`
 * 这类读取函数，每次按当前语言取。
 */

export type IconCategory =
  | "engineering"
  | "design"
  | "ai"
  | "qa"
  | "ops"
  | "general";

/** 别名要么是不随语言变的字面量（`build` / `cli`），要么是包语言包里的一条 key。 */
export type IconTextSource = { text: string } | { labelKey: string };

export interface IconVocabularyEntry {
  key: string;
  labelKey: string;
  icon: LucideIcon;
  category: IconCategory;
  aliases: readonly IconTextSource[];
}

export interface IconMeta {
  key: string;
  label: string;
  icon: LucideIcon;
  category: IconCategory;
  aliases?: string[];
}

/** 极窄的翻译口子：宿主的 `TFunction` 与包内 `useUiTranslation()` 的 `t` 都满足它。 */
export type IconTranslate = (key: string) => string;

export const ICON_CATEGORY_ORDER: readonly IconCategory[] = [
  "engineering",
  "design",
  "ai",
  "qa",
  "ops",
  "general",
];

/**
 * 顺序即选择器里的呈现顺序，两个宿主一致。
 */
export const ICON_VOCABULARY: readonly IconVocabularyEntry[] = [
  {
    key: "hammer",
    labelKey: "iconRegistry.icons.hammer",
    icon: Hammer,
    category: "engineering",
    aliases: [
      { text: "build" },
      { labelKey: "iconRegistry.aliases.construction" },
    ],
  },
  {
    key: "code-xml",
    labelKey: "iconRegistry.icons.codeXml",
    icon: CodeXml,
    category: "engineering",
    aliases: [{ text: "code" }, { labelKey: "iconRegistry.aliases.coding" }],
  },
  {
    key: "terminal",
    labelKey: "iconRegistry.icons.terminal",
    icon: Terminal,
    category: "engineering",
    aliases: [{ text: "cli" }, { text: "shell" }],
  },
  {
    key: "wrench",
    labelKey: "iconRegistry.icons.wrench",
    icon: Wrench,
    category: "engineering",
    aliases: [{ text: "tools" }, { labelKey: "iconRegistry.aliases.tools" }],
  },
  {
    key: "cpu",
    labelKey: "iconRegistry.icons.cpu",
    icon: Cpu,
    category: "engineering",
    aliases: [{ text: "chip" }, { labelKey: "iconRegistry.aliases.hardware" }],
  },
  {
    key: "network",
    labelKey: "iconRegistry.icons.network",
    icon: Network,
    category: "engineering",
    aliases: [{ text: "net" }, { labelKey: "iconRegistry.aliases.topology" }],
  },
  {
    key: "palette",
    labelKey: "iconRegistry.icons.palette",
    icon: Palette,
    category: "design",
    aliases: [{ text: "color" }, { labelKey: "iconRegistry.aliases.color" }],
  },
  {
    key: "brush",
    labelKey: "iconRegistry.icons.brush",
    icon: Brush,
    category: "design",
    aliases: [{ text: "paint" }, { labelKey: "iconRegistry.aliases.painting" }],
  },
  {
    key: "paint-bucket",
    labelKey: "iconRegistry.icons.paintBucket",
    icon: PaintBucket,
    category: "design",
    aliases: [{ text: "fill" }, { labelKey: "iconRegistry.aliases.fill" }],
  },
  {
    key: "layers",
    labelKey: "iconRegistry.icons.layers",
    icon: Layers,
    category: "design",
    aliases: [
      { text: "stack" },
      { labelKey: "iconRegistry.aliases.hierarchy" },
    ],
  },
  {
    key: "ruler",
    labelKey: "iconRegistry.icons.ruler",
    icon: Ruler,
    category: "design",
    aliases: [
      { text: "measure" },
      { labelKey: "iconRegistry.aliases.measure" },
    ],
  },
  {
    key: "sparkles",
    labelKey: "iconRegistry.icons.sparkles",
    icon: Sparkles,
    category: "ai",
    aliases: [
      { text: "ai" },
      { labelKey: "iconRegistry.aliases.intelligence" },
    ],
  },
  {
    key: "brain",
    labelKey: "iconRegistry.icons.brain",
    icon: Brain,
    category: "ai",
    aliases: [{ text: "think" }, { labelKey: "iconRegistry.aliases.thinking" }],
  },
  {
    key: "bot",
    labelKey: "iconRegistry.icons.bot",
    icon: Bot,
    category: "ai",
    aliases: [{ text: "robot" }],
  },
  {
    key: "wand-sparkles",
    labelKey: "iconRegistry.icons.wandSparkles",
    icon: WandSparkles,
    category: "ai",
    aliases: [{ text: "magic" }, { labelKey: "iconRegistry.aliases.magic" }],
  },
  {
    key: "beaker",
    labelKey: "iconRegistry.icons.beaker",
    icon: Beaker,
    category: "qa",
    aliases: [{ text: "lab" }, { labelKey: "iconRegistry.aliases.experiment" }],
  },
  {
    key: "bug",
    labelKey: "iconRegistry.icons.bug",
    icon: Bug,
    category: "qa",
    aliases: [
      { text: "debug" },
      { labelKey: "iconRegistry.aliases.debugging" },
    ],
  },
  {
    key: "shield-check",
    labelKey: "iconRegistry.icons.shieldCheck",
    icon: ShieldCheck,
    category: "qa",
    aliases: [
      { text: "security" },
      { labelKey: "iconRegistry.aliases.protection" },
    ],
  },
  {
    key: "scale",
    labelKey: "iconRegistry.icons.scale",
    icon: Scale,
    category: "qa",
    aliases: [
      { text: "balance" },
      { labelKey: "iconRegistry.aliases.tradeoff" },
    ],
  },
  {
    key: "rocket",
    labelKey: "iconRegistry.icons.rocket",
    icon: Rocket,
    category: "ops",
    aliases: [{ text: "launch" }, { labelKey: "iconRegistry.aliases.release" }],
  },
  {
    key: "gauge",
    labelKey: "iconRegistry.icons.gauge",
    icon: Gauge,
    category: "ops",
    aliases: [
      { text: "metric" },
      { labelKey: "iconRegistry.aliases.monitoring" },
    ],
  },
  {
    key: "database",
    labelKey: "iconRegistry.icons.database",
    icon: Database,
    category: "ops",
    aliases: [{ text: "db" }, { labelKey: "iconRegistry.aliases.storage" }],
  },
  {
    key: "chart-line",
    labelKey: "iconRegistry.icons.chartLine",
    icon: LineChart,
    category: "ops",
    aliases: [{ text: "chart" }, { labelKey: "iconRegistry.aliases.trend" }],
  },
  {
    key: "package",
    labelKey: "iconRegistry.icons.package",
    icon: Package,
    category: "ops",
    aliases: [
      { text: "bundle" },
      { labelKey: "iconRegistry.aliases.artifact" },
    ],
  },
  {
    key: "puzzle",
    labelKey: "iconRegistry.icons.puzzle",
    icon: Puzzle,
    category: "general",
    aliases: [{ text: "module" }, { labelKey: "iconRegistry.aliases.module" }],
  },
  {
    key: "target",
    labelKey: "iconRegistry.icons.target",
    icon: Target,
    category: "general",
    aliases: [{ text: "goal" }, { labelKey: "iconRegistry.aliases.focus" }],
  },
  {
    key: "flag",
    labelKey: "iconRegistry.icons.flag",
    icon: Flag,
    category: "general",
    aliases: [{ text: "mark" }, { labelKey: "iconRegistry.aliases.milestone" }],
  },
  {
    key: "compass",
    labelKey: "iconRegistry.icons.compass",
    icon: Compass,
    category: "general",
    aliases: [
      { text: "direction" },
      { labelKey: "iconRegistry.aliases.direction" },
    ],
  },
  {
    key: "megaphone",
    labelKey: "iconRegistry.icons.megaphone",
    icon: Megaphone,
    category: "general",
    aliases: [
      { text: "announce" },
      { labelKey: "iconRegistry.aliases.notification" },
    ],
  },
  {
    key: "eye",
    labelKey: "iconRegistry.icons.eye",
    icon: Eye,
    category: "general",
    aliases: [{ text: "watch" }, { labelKey: "iconRegistry.aliases.observe" }],
  },
];

const ENTRY_BY_KEY = new Map(
  ICON_VOCABULARY.map((entry) => [entry.key, entry]),
);

/** key 带包的 namespace 前缀：宿主传进来的 `t` 绑在它自己的默认 namespace 上。 */
function resolve(source: IconTextSource, t: IconTranslate): string {
  return "text" in source
    ? source.text
    : t(`${AGENTRE_UI_NAMESPACE}:${source.labelKey}`);
}

function localize(entry: IconVocabularyEntry, t: IconTranslate): IconMeta {
  return {
    key: entry.key,
    label: t(`${AGENTRE_UI_NAMESPACE}:${entry.labelKey}`),
    icon: entry.icon,
    category: entry.category,
    aliases: entry.aliases.map((alias) => resolve(alias, t)),
  };
}

/** 按当前语言取整张图标表。语言切换后再调一次即得新文案。 */
export function iconList(t: IconTranslate): IconMeta[] {
  return ICON_VOCABULARY.map((entry) => localize(entry, t));
}

/** 按当前语言取分类表。 */
export function iconCategories(
  t: IconTranslate,
): { key: IconCategory; label: string }[] {
  return ICON_CATEGORY_ORDER.map((key) => ({
    key,
    // AI 不翻译：中英文写法相同，翻它等于给自己留一条会漂的文案。
    label:
      key === "ai"
        ? "AI"
        : t(`${AGENTRE_UI_NAMESPACE}:iconRegistry.categories.${key}`),
  }));
}

/** 按 key 取单个图标的当前语言元数据。 */
export function iconMeta(
  key: string | null | undefined,
  t: IconTranslate,
): IconMeta | undefined {
  if (!key) return undefined;
  const entry = ENTRY_BY_KEY.get(key);
  return entry ? localize(entry, t) : undefined;
}

/** 表里不认得的 key 一律退回拼图 —— 宿主存过、这张表后来改过名时不至于空一块。 */
export function iconForKey(key: string | null | undefined): LucideIcon {
  if (!key) return Puzzle;
  return ENTRY_BY_KEY.get(key)?.icon ?? Puzzle;
}

export function hasIcon(key: string | null | undefined): boolean {
  if (!key) return false;
  return ENTRY_BY_KEY.has(key);
}

export function searchIcons(query: string, t: IconTranslate): IconMeta[] {
  const needle = query.trim().toLowerCase();
  const all = iconList(t);
  if (!needle) return all;

  return all.filter((meta) => {
    if (meta.key.toLowerCase().includes(needle)) return true;
    if (meta.label.toLowerCase().includes(needle)) return true;
    return Boolean(
      meta.aliases?.some((alias) => alias.toLowerCase().includes(needle)),
    );
  });
}

export function iconsByCategory(t: IconTranslate): {
  category: IconCategory;
  label: string;
  items: IconMeta[];
}[] {
  const all = iconList(t);
  return iconCategories(t).map((category) => ({
    category: category.key,
    label: category.label,
    items: all.filter((meta) => meta.category === category.key),
  }));
}
