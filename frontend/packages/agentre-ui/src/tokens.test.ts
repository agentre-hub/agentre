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
  // 身份色板的中性档：它是「没有身份色」的墨色而不是一种色相，白字形压在上面，
  // 暗色下调浅反而会把字形吃掉。见下面「身份色板覆盖到中性档」。
  "--agent-neutral",
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
  "--rail",
];

/**
 * 反馈色 → 它**实际落在**的表面。
 *
 * 分成两条而不是「一个 accent 打天下」，是因为 `--rail`（外壳带：标题栏 / 图标栏 /
 * 状态栏）比其它表面暗得多，亮色下是 #e4e4e7。任何在白卡片上够用的 hover 落到它
 * 身上都会被吃掉：2026-08-19 把 --accent 从 #f4f4f5 调到 #e7e7ea 修好了 card /
 * background，却正好把它挪到了 rail 跟前——rail 上的 hover 从 1.154 掉到 1.028。
 * 当时这条守卫只列了 card 和 background，所以没拦住。
 *
 * 教训写在这里：**新增一个会被 hover 的表面时，必须同时把它加进这张表**，
 * 否则那一层的反馈会静默失效——不报错、不崩、只是点上去没有任何变化。
 */
const FEEDBACK_SURFACES: Array<[string, string[]]> = [
  ["--accent", ["--card", "--background", "--popover", "--secondary"]],
  ["--rail-accent", ["--rail"]],
  ["--sidebar-selected-bg", ["--sidebar"]],
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

  it.each(
    THEMES.flatMap(([scope, label, get]) =>
      FEEDBACK_SURFACES.map(
        ([token, surfaces]) => [scope, label, token, surfaces, get] as const,
      ),
    ),
  )(
    "%s(%s)下 %s 对它实际落到的每个表面都可觉察",
    (_scope, _label, token, surfaces, get) => {
      const t = get();
      expect(t[token], `tokens.css 里没有 ${token}`).toBeDefined();
      for (const surface of surfaces) {
        expect(
          contrast(t[token], t[surface]),
          `${token} ${t[token]} 落在 ${surface} ${t[surface]} 上`,
        ).toBeGreaterThanOrEqual(FEEDBACK_MIN);
      }
    },
  );
});

/**
 * 选中面必须**压过** hover 面。
 *
 * 这条不是「可觉察」（FEEDBACK_MIN 已经管了），是「谁更强」：`--primary-soft` 当选中面时
 * 对 --sidebar 只有 1.01，而 hover 的 --sidebar-active-bg 是 1.10——「我选中的」比
 * 「鼠标碰巧停着的」更弱，读起来是反的。当时补救的办法是在行左边加一根 3px 竖条；
 * 竖条撤掉之后，就只剩这条不变量兜着了。
 */
describe("选中面比 hover 面更强", () => {
  it.each(THEMES)(
    "%s（%s）下选中面对 --sidebar 的比值高于 hover 面",
    (_scope, _label, get) => {
      const t = get();
      const selected = contrast(t["--sidebar-selected-bg"], t["--sidebar"]);
      const hover = contrast(t["--sidebar-active-bg"], t["--sidebar"]);
      expect(
        selected,
        `选中面 ${t["--sidebar-selected-bg"]} 对 sidebar 是 ${selected.toFixed(2)}，` +
          `hover 面 ${t["--sidebar-active-bg"]} 是 ${hover.toFixed(2)}`,
      ).toBeGreaterThan(hover);
    },
  );
});

/**
 * 文字色 → 它**实际落在**的表面。
 *
 * 和 FEEDBACK_SURFACES 同一个道理，教训也同一个：取值必须由**最暗的那个落点**
 * 决定，不是由 card 决定。--muted-foreground 曾经是 #71717a，在 card 上 4.83
 * 看着很健康，但状态栏和窗口控制按钮的标签坐在 --rail #e4e4e7 上，那里只有 3.81。
 *
 * `--decorative-foreground` **刻意不在这张表里**：它按定义就不承载信息（分隔点、
 * 行号、aria-hidden 的伴随图标），2.5:1 是它的设计意图。凡是要读的东西都不该用它
 * ——真要往这张表里加它，说明用错 token 了。
 */
const TEXT_SURFACES: Array<[string, string[]]> = [
  ["--foreground", ["--background", "--card", "--popover"]],
  [
    "--muted-foreground",
    [
      "--card",
      "--background",
      "--popover",
      "--secondary",
      "--muted",
      "--sidebar",
      "--rail",
    ],
  ],
  ["--secondary-foreground", ["--secondary"]],
  ["--card-foreground", ["--card"]],
  ["--popover-foreground", ["--popover"]],
  ["--sidebar-foreground", ["--sidebar"]],
  ["--code-foreground", ["--code-surface"]],
  ["--code-muted-foreground", ["--code-surface"]],
  [
    "--primary-text",
    ["--card", "--background", "--primary-soft", "--sidebar-selected-bg"],
  ],
  ["--destructive-text", ["--destructive-soft", "--card"]],
  // 「按时间」档的行是两行式，第二行是弱化文字；选中时它落在选中面上而不是 sidebar 上。
  // 不能沿用 --muted-foreground：暗色下 #909399 在任何「强过 hover」的选中面上都够不到
  // 4.5（可行窗口只剩对 sidebar 1.28~1.33 那一丝，落进去选中与 hover 就分不出了）。
  // 与 --status-*-text 同一个道理：换了落脚的面，弱化文字就得换值。
  ["--sidebar-selected-muted", ["--sidebar-selected-bg"]],
];

describe("正文色对它落到的每个表面都达标", () => {
  it.each(
    THEMES.flatMap(([scope, label, get]) =>
      TEXT_SURFACES.map(
        ([token, surfaces]) => [scope, label, token, surfaces, get] as const,
      ),
    ),
  )("%s(%s)下 %s", (_scope, _label, token, surfaces, get) => {
    const t = get();
    expect(t[token], `tokens.css 里没有 ${token}`).toBeDefined();
    for (const surface of surfaces) {
      expect(
        contrast(t[token], t[surface]),
        `${token} ${t[token]} 落在 ${surface} ${t[surface]} 上`,
      ).toBeGreaterThanOrEqual(TEXT_MIN);
    }
  });

  it("装饰色没有混进正文表", () => {
    // 它对每个表面都在 2.5 附近，进了这张表必然全红——真出现说明有人把
    // 「要读的东西」判成了装饰，或者反过来。
    expect(TEXT_SURFACES.map(([t]) => t)).not.toContain(
      "--decorative-foreground",
    );
  });
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

/**
 * 身份色板的第 17 档。`AgentColor` 从一开始就有 `neutral`（「没有身份色」这一档），
 * 但 tokens.css 只到 --agent-16，于是宿主只能在 agentColorClassNames 里写字面色
 * （桌面端曾经是 bg-neutral-600 + 一条 eslint 豁免）——字面色不进 token 层，两个
 * 宿主各写各的，改一处也流不到另一处。
 */
describe("身份色板覆盖到中性档", () => {
  it("--agent-neutral 有声明与 --color-* 映射，且白色字形压得住", () => {
    expect(
      root["--agent-neutral"],
      "tokens.css 里没有 --agent-neutral",
    ).toBeDefined();
    expect(theme["--color-agent-neutral"]).toBe("var(--agent-neutral)");
    // 字形是白的（--agent-foreground 主题无关），所以中性档也必须是深到读得出白字
    // 的那一端——这正是它不跟随主题变浅的原因。
    expect(
      contrast(root["--agent-foreground"], root["--agent-neutral"]),
      `--agent-foreground ${root["--agent-foreground"]} 落在 --agent-neutral ${root["--agent-neutral"]} 上`,
    ).toBeGreaterThanOrEqual(TEXT_MIN);
  });
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
