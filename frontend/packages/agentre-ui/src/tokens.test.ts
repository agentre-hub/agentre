import fs from "node:fs";
import path from "node:path";

import { describe, expect, it } from "vitest";

/**
 * design token 契约守卫。
 *
 * 和 `boundary.test.ts` 分工：那条守"包不许反向依赖宿主"，这条守
 * "token 本身的角色分得开、映射齐不齐"。两条都不测"好不好看"——那是设计稿的事。
 *
 * 为什么这条守卫住在包里：`styles/tokens.css` 是两端（桌面端 + agentre-server
 * 控制台）**唯一**的一份色值。此前 server 在自己的 globals.css 里复制了一份同名
 * token 并逐字节钉在它自己的守卫里——两边都绿，却谁也不知道对方存在，桌面端改了
 * 也流不过去。把不变量放在包里，才是"同源"真正成立的地方。
 *
 * 直接读文件而不是 import CSS：vitest 不编译 Tailwind，`@theme` 块不会变成任何
 * 可查询的东西，只有读源文件才测得到真实声明。
 */
const TOKENS_CSS = path.resolve(__dirname, "../styles/tokens.css");

/** 交互反馈（hover / 选中底色）没有法定门槛，用这条工程线代表"肉眼能觉察"。 */
const FEEDBACK_MIN = 1.15;
/** 正文门槛，见 docs/design.md §10。 */
const TEXT_MIN = 4.5;

const css = fs
  .readFileSync(TOKENS_CSS, "utf8")
  .replace(/\/\*[\s\S]*?\*\//g, "");

/**
 * 取一个块的内容。块尾用花括号计数而不是非贪婪正则——`@theme` 里将来若出现
 * 嵌套块，正则会在第一个 `}` 截断，于是后半段声明凭空消失、测试却是绿的。
 * 选择器行首锚定：`@custom-variant dark (&:is(.dark *));` 那行里也有 `.dark`。
 */
function block(selector: string): string {
  const escaped = selector.replace(/[.*+?^${}()|[\]\\]/g, "\\$&");
  const head = new RegExp(`^${escaped}[^{]*\\{`, "m").exec(css);
  if (!head) return "";
  const open = head.index + head[0].length - 1;
  let depth = 0;
  for (let i = open; i < css.length; i++) {
    if (css[i] === "{") depth++;
    else if (css[i] === "}" && --depth === 0) return css.slice(open + 1, i);
  }
  return "";
}

function decls(source: string): Record<string, string> {
  const out: Record<string, string> = {};
  for (const m of source.matchAll(/(--[\w-]+)\s*:\s*([^;]+);/g)) {
    out[m[1]] = m[2].replace(/\s+/g, " ").trim();
  }
  return out;
}

const root = decls(block(":root"));
const dark = decls(block(".dark"));
const theme = decls(block("@theme"));

/** 只有十六进制色值参与配色断言；--radius / 阴影 / 字体栈各有形态，另行处理。 */
const isHex = (v: string | undefined): v is string =>
  typeof v === "string" && /^#[0-9a-fA-F]{6}$/.test(v);

function contrast(a: string, b: string): number {
  const luminance = (hex: string) => {
    const ch = [1, 3, 5].map((i) => {
      const c = parseInt(hex.slice(i, i + 2), 16) / 255;
      return c <= 0.04045 ? c / 12.92 : ((c + 0.055) / 1.055) ** 2.4;
    });
    return 0.2126 * ch[0] + 0.7152 * ch[1] + 0.0722 * ch[2];
  };
  const [hi, lo] = [luminance(a), luminance(b)].sort((x, y) => y - x);
  return (hi + 0.05) / (lo + 0.05);
}

const THEMES = [
  [":root", "亮色", () => root] as const,
  [".dark", "暗色", () => dark] as const,
];

/**
 * 不随主题变化、只在 :root 声明一次的 token。
 * 加进这张表前先问一句：它真的两个主题同一个值吗？——大多数颜色不是。
 */
const THEME_INVARIANT = new Set([
  "--traffic-close",
  "--traffic-minimize",
  "--traffic-zoom",
  // 项目 / Agent 字形上那个首字：底色是 --agent-1…16 调色板，深浅两套压的都是白字。
  "--agent-foreground",
  // 实心琥珀 Badge 上的深棕字。深浅两色都是亮琥珀，同一个值都可读。
  "--status-waiting-foreground",
]);

/**
 * "静止表面"——页面上不表示交互状态的底色。
 * hover / 选中反馈色**不许**取其中任何一个值：取了就等于把反馈盖在同色的底上，
 * 反馈归零。亮色此前 --accent ≡ --secondary ≡ --muted ≡ --sidebar ≡ #f4f4f5，
 * 全仓 86 处 hover:bg-accent 因此在 secondary / sidebar 上是 1.00。
 */
const RESTING_SURFACES = [
  "--background",
  "--card",
  "--popover",
  "--secondary",
  "--muted",
  "--sidebar",
];

describe("交互反馈色与静止表面必须分开", () => {
  it.each(THEMES)("%s(%s)下 --accent 不等于任何静止表面", (_scope, _t, get) => {
    const t = get();
    for (const surface of RESTING_SURFACES) {
      expect(
        t["--accent"],
        `--accent 与 ${surface} 同值 → 落在该表面上的 hover 反馈为零`,
      ).not.toBe(t[surface]);
    }
  });

  it.each(THEMES)(
    "%s(%s)下 --accent 对 card 与 background 都可觉察",
    (_scope, _t, get) => {
      const t = get();
      for (const surface of ["--card", "--background"] as const) {
        expect(
          contrast(t["--accent"], t[surface]),
          `--accent ${t["--accent"]} 落在 ${surface} ${t[surface]} 上`,
        ).toBeGreaterThanOrEqual(FEEDBACK_MIN);
      }
    },
  );
});

describe("状态色的『文字』角色与『填充』角色分开", () => {
  // --status-running / --status-waiting 是饱和色，当点和底色是对的，当文字读不出来：
  // 亮色下它们在自己的胶囊底上只有 2.41 / 2.07。所以文字另立 --status-*-text。
  it.each(THEMES)(
    "%s(%s)下 RUNNING 文字在胶囊底和卡片上都达标",
    (_s, _t, get) => {
      const t = get();
      for (const surface of ["--status-running-bg", "--card"] as const) {
        expect(
          contrast(t["--status-running-text"], t[surface]),
          `--status-running-text ${t["--status-running-text"]} 落在 ${surface} ${t[surface]} 上`,
        ).toBeGreaterThanOrEqual(TEXT_MIN);
      }
    },
  );

  it.each(THEMES)(
    "%s(%s)下 WAITING 文字在胶囊底和卡片上都达标",
    (_s, _t, get) => {
      const t = get();
      for (const surface of ["--status-waiting-bg", "--card"] as const) {
        expect(
          contrast(t["--status-waiting-text"], t[surface]),
          `--status-waiting-text ${t["--status-waiting-text"]} 落在 ${surface} ${t[surface]} 上`,
        ).toBeGreaterThanOrEqual(TEXT_MIN);
      }
    },
  );
});

describe("token 声明完整性", () => {
  it("每个 :root 色值要么在 .dark 有覆写，要么显式声明为主题无关", () => {
    const missing = Object.entries(root)
      .filter(([name, value]) => isHex(value) && !THEME_INVARIANT.has(name))
      .map(([name]) => name)
      .filter((name) => !(name in dark));

    // 漏一个的表现是：深色模式下那一块悄悄用着浅色值，不报错、不崩，只是难看。
    expect(missing, `这些 token 在 .dark 里没有覆写`).toEqual([]);
  });

  it("每个色值 token 在 @theme inline 里有 --color-* 映射", () => {
    const missing = Object.entries(root)
      .filter(([, value]) => isHex(value))
      .map(([name]) => name)
      .filter((name) => !(`--color-${name.slice(2)}` in theme));

    // 少一条映射不报错，对应的 bg-/text- 工具类只是不生成任何规则，
    // 页面上表现为「那块没颜色」。
    expect(missing, `这些 token 没有 --color-* 别名`).toEqual([]);
  });

  it(".dark 不引入 :root 里不存在的色值 token", () => {
    const orphans = Object.entries(dark)
      .filter(([, value]) => isHex(value))
      .map(([name]) => name)
      .filter((name) => !(name in root));

    // 只在 .dark 声明 = 浅色模式下这个变量根本没有值，var() 回退到空。
    expect(orphans, `这些 token 只在 .dark 里存在`).toEqual([]);
  });
});
