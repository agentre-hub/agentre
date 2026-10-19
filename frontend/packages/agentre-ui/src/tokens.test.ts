import fs from "node:fs";
import path from "node:path";

import { describe, expect, it } from "vitest";

import { contrast, mix } from "./styles/__testing__/tokens";

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
/** 非文字图形（圆点 / 进度条 / 实心标记）门槛，见 WCAG 2.2 §1.4.11 与 docs/design.md §10。 */
const GRAPHIC_MIN = 3;
/**
 * 两块相邻的面不画线、只靠色阶分开时的下限（外框 / 内容页、填充 / 它脚下的面）。
 * 比 FEEDBACK_MIN 低：那条是「鼠标一碰就要看得出变化」，这条是「静止时认得出是两块」。
 */
const SURFACE_MIN = 1.08;

const css = fs
  .readFileSync(TOKENS_CSS, "utf8")
  .replace(/\/\*[\s\S]*?\*\//g, "");

/**
 * 取一个块的内容。块尾用花括号计数而不是非贪婪正则——`@theme` 里将来若出现
 * 嵌套块，正则会在第一个 `}` 截断，于是后半段声明凭空消失、测试却是绿的。
 * 选择器行首锚定：`@custom-variant dark (&:is(.dark *));` 那行里也有 `.dark`。
 * 名字要整个对上：`@utility focus-outline` 不许顺带匹配到 `@utility focus-outline-inset`，
 * 否则取到哪一块全看它俩在文件里谁排前面。
 */
function block(selector: string): string {
  const escaped = selector.replace(/[.*+?^${}()|[\]\\]/g, "\\$&");
  const head = new RegExp(`^${escaped}(?![\\w-])[^{]*\\{`, "m").exec(css);
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
  // 实心琥珀 Badge 上的深棕字。深浅两色都是亮琥珀，同一个值都可读。
  "--status-waiting-foreground",
  // 身份色块上的字形。两主题都是白字，深色靠压暗底色读得出（见下方守卫）。
  "--agent-foreground",
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
  "--chrome",
  "--sheet",
  "--raised",
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
 * 行号、aria-hidden 的伴随图标），现在满足 §1.4.11 的 3:1 但够不到正文的 4.5。
 * 凡是要读的东西都不该用它——真要往这张表里加它，说明用错 token 了。
 */
const TEXT_SURFACES: Array<[string, string[]]> = [
  [
    "--foreground",
    ["--background", "--card", "--popover", "--chrome", "--sheet", "--raised"],
  ],
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
      "--chrome",
      "--sheet",
      "--raised",
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
    [
      "--card",
      "--background",
      "--primary-soft",
      "--sidebar-selected-bg",
      "--raised",
    ],
  ],
  // raised：外框上的选中项（会话行、组织索引的行与组头），行尾的「无目标」落在它上面。
  ["--destructive-text", ["--destructive-soft", "--card", "--raised"]],
  // 「按时间」档的行是两行式，第二行是弱化文字；选中时它落在选中面上而不是 sidebar 上。
  // 不能沿用 --muted-foreground：暗色下 #909399 在任何「强过 hover」的选中面上都够不到
  // 4.5（可行窗口只剩对 sidebar 1.28~1.33 那一丝，落进去选中与 hover 就分不出了）。
  // 与 --status-*-text 同一个道理：换了落脚的面，弱化文字就得换值。
  ["--sidebar-selected-muted", ["--sidebar-selected-bg"]],
  // 同一个道理落在内容表面上：行被选中 / 悬停时底色换成 --accent，行里的说明、
  // 快捷键、右侧路径还是弱化文字。--muted-foreground 在 --accent 上亮色 4.38、
  // 暗色 3.48。
  ["--accent-muted-foreground", ["--accent"]],
  // 填充面上的弱化文字（见下方「填充两档」）。它由填充工具类换入整棵子树，子树里
  // 回到裸表面的地方（聚焦时底色回到内容页的字段）照样用它，所以裸表面也要过线。
  [
    "--fill-muted-foreground",
    ["--sheet", "--chrome", "--card", "--popover", "--background"],
  ],
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
    // 它不承载信息，只保证 3:1 的非文本对比度，进不了要 4.5 的正文表——真出现
    // 说明有人把「要读的东西」判成了装饰，或者反过来。
    expect(TEXT_SURFACES.map(([t]) => t)).not.toContain(
      "--decorative-foreground",
    );
  });
});

/**
 * `--accent` 上的弱化文字，由**表面自己**换色。
 *
 * 弱化文字散在行的各个角落（说明、快捷键、路径、伴随图标），而「这一行现在是不是
 * 选中 / 悬停」只有行自己知道。让每个调用点按状态换类名，等于把同一个判断抄几十遍
 * ——全仓 `hover:bg-accent` 就有近 80 处。所以改在生产方：`bg-accent` 这个工具类
 * 在上底色的同时把 `--muted-foreground` 重新指向 `--accent-muted-foreground`，
 * 子树里所有 `text-muted-foreground` 跟着变，变体前缀（hover: / focus: /
 * data-[selected=true]:）照常生效。
 *
 * 带不透明度的 `bg-accent/60` 走不到这条——Tailwind 的静态工具类不吃修饰符——
 * 那些调用点要自己带上 `on-accent`。
 */
describe("accent 面上的弱化文字由表面换色", () => {
  it.each(["bg-accent", "on-accent"])(
    "@utility %s 把 --muted-foreground 指向 --accent-muted-foreground",
    (name) => {
      const utility = decls(block(`@utility ${name}`));
      expect(
        utility["--muted-foreground"],
        `tokens.css 里没有 @utility ${name}，或它没有重指 --muted-foreground`,
      ).toBe("var(--accent-muted-foreground)");
    },
  );

  // 悬停行是半透明的 accent 叠在它所在的表面上（全仓用到 /40 ~ /60）。叠出来的底
  // 比实心 accent 浅，但暗色下 --muted-foreground 仍然够不到：/60 叠 popover 3.92。
  it.each(
    THEMES.flatMap(([scope, label, get]) =>
      (["--popover", "--card"] as const).flatMap((surface) =>
        [0.4, 0.6].map((alpha) => [scope, label, surface, alpha, get] as const),
      ),
    ),
  )(
    "%s(%s)下 accent 以 %s 为底、不透明度 %s 时弱化文字仍达标",
    (_scope, _label, surface, alpha, get) => {
      const t = get();
      const fill = mix(t["--accent"], t[surface], alpha);
      expect(
        contrast(t["--accent-muted-foreground"], fill),
        `--accent-muted-foreground ${t["--accent-muted-foreground"]} 落在 ${fill} 上`,
      ).toBeGreaterThanOrEqual(TEXT_MIN);
    },
  );
});

/**
 * 键盘焦点的两种画法。
 *
 * 全仓只在这里定义一次，调用点写 `focus-visible:focus-outline` /
 * `focus-visible:focus-halo`，不再各自拼 ring（lint 拦着，见
 * frontend/eslint-rules/design-tokens.js）。
 *
 * 按钮类是 2px 实色环 + 2px 间隙：实色是为了让对比度不随底色漂移——此前的
 * `ring-ring/50` 合成后只有约 2.1:1，`/40` 约 1.8:1；间隙是为了让实心主按钮
 * （底色就是 --primary ≈ --ring）的环不和按钮本身粘成一块。
 */
describe("键盘焦点环", () => {
  it("focus-outline 是 2px 实色环，与控件之间留 2px 间隙", () => {
    const rule = block("@utility focus-outline");
    expect(rule).toMatch(/outline:\s*2px solid var\(--ring\)/);
    expect(rule).toMatch(/outline-offset:\s*2px/);
  });

  it("focus-outline-inset 是同一条环向内画：贴着裁剪容器的元素用它", () => {
    // 向外的环要伸出去 4px（2px 间隙 + 2px 环）。标签页填满一条 overflow-hidden 的带子、
    // 会话行贴着列表的裁剪边，伸出去的部分直接被切掉——真机上标签页的环只剩右边一条线。
    // 「同一条环」是拿两块比出来的，不是把字面量再抄一遍：只改了其中一处的粗细或颜色，
    // 这里就红。
    const outline = (rule: string) => /outline:\s*([^;]+);/.exec(rule)?.[1];
    const inset = block("@utility focus-outline-inset");
    expect(outline(inset)).toBeDefined();
    expect(outline(inset)).toBe(outline(block("@utility focus-outline")));
    expect(inset).toMatch(/outline-offset:\s*-2px/);
  });

  it.each(THEMES)(
    "%s(%s)下向内的环对它压着的每种行底色都有 3:1",
    (_s, _t, get) => {
      const t = get();
      // 向内的环压在元素自己的填充上：会话行的静止 / 悬停 / 选中，标签页的激活 / 未激活。
      for (const fill of [
        "--sidebar",
        "--sidebar-active-bg",
        "--sidebar-selected-bg",
        "--background",
        "--secondary",
        "--card",
        "--muted",
        "--accent",
        "--chrome",
        "--sheet",
        "--raised",
      ]) {
        expect(
          contrast(t["--ring"], t[fill]),
          `--ring ${t["--ring"]} 压在 ${fill} ${t[fill]} 上`,
        ).toBeGreaterThanOrEqual(GRAPHIC_MIN);
      }
    },
  );

  it("focus-halo 是实色的边加一圈光晕（输入类控件现有的样子）", () => {
    const rule = block("@utility focus-halo");
    expect(rule).toMatch(/border-ring/);
    expect(rule).toMatch(/ring-ring\/50/);
  });

  it.each(THEMES)(
    "%s(%s)下实色环对它会落到的每个表面都有 3:1",
    (_s, _t, get) => {
      const t = get();
      for (const surface of [
        "--background",
        "--card",
        "--popover",
        "--secondary",
        "--sidebar",
        "--rail",
        "--chrome",
        "--sheet",
        "--raised",
      ]) {
        expect(
          contrast(t["--ring"], t[surface]),
          `--ring ${t["--ring"]} 落在 ${surface} ${t[surface]} 上`,
        ).toBeGreaterThanOrEqual(GRAPHIC_MIN);
      }
    },
  );
});

/**
 * 状态**填充色**上的前景。
 *
 * 和「文字 vs 填充」那条是一对：那条管「状态当文字写在纸上」，这条管「写在状态
 * 实底上的字」。实底亮度的任何变化（比如降饱和）都会直接改这里的对比度——没有
 * 这条守卫，把 --status-running 调暗一点、前景字就会悄悄掉到 2.54 也没人拦。
 */
describe("状态填充色上的前景读得出", () => {
  it.each(THEMES)(
    "%s(%s)下 --status-running-foreground 在 --status-running 实底上达标",
    (_scope, _label, get) => {
      const t = get();
      // 不随主题变的 token（waiting-foreground）只在 :root 声明，按级联取有效值。
      const effective = (name: string) => t[name] ?? root[name];
      const fg = effective("--status-running-foreground");
      expect(
        contrast(fg, t["--status-running"]),
        `${fg} 落在 ${t["--status-running"]} 上`,
      ).toBeGreaterThanOrEqual(TEXT_MIN);
    },
  );

  it.each(THEMES)(
    "%s(%s)下 --status-waiting-foreground 在 --status-waiting 实底上达标",
    (_scope, _label, get) => {
      const t = get();
      const effective = (name: string) => t[name] ?? root[name];
      const fg = effective("--status-waiting-foreground");
      expect(
        contrast(fg, t["--status-waiting"]),
        `${fg} 落在 ${t["--status-waiting"]} 上`,
      ).toBeGreaterThanOrEqual(TEXT_MIN);
    },
  );
});

describe("状态色的『文字』角色与『填充』角色分开", () => {
  // --status-running / --status-waiting 是饱和色，当点和底色是对的，当文字读不出来：
  // 亮色下它们在自己的胶囊底上只有 2.51 / 2.23。所以文字另立 --status-*-text。
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

/** 状态点会落到的表面：卡片、灰槽、弹层，以及外框（会话列表）、内容页、外框上的选中行。 */
const STATUS_DOT_SURFACES = [
  "--card",
  "--secondary",
  "--popover",
  "--chrome",
  "--sheet",
  "--raised",
] as const;

/**
 * 状态**填充色**当「非文字图形」用（圆点、进度条、实心标记）时对静止表面的对比度。
 *
 * 既有守卫只覆盖了另外两种角色：`--status-*-text` 是「状态当文字」、
 * `--status-*-foreground` 是「实底上的字」。填充色本身画成的点/图形压在
 * `--card` / `--secondary` 上这一角色一直没有守卫，所以浅色下 `--status-running` /
 * `--status-waiting` 掉到 3:1 以下也无人拦（2026-09-17 审计）。
 * 门槛取 WCAG 2.2 §1.4.11 的非文本对比度 3:1。
 *
 * 必须按 effective() 取级联后的有效值：旧缺陷的形态正是「深色没覆写、沿用到
 * :root」，只比对当前主题块里声明的 hex 会正好漏掉它。
 */
describe("状态填充色当非文字图形用时对表面达标", () => {
  it.each(
    THEMES.flatMap(([scope, label, get]) =>
      ["--status-running", "--status-waiting"].map(
        (token) => [scope, label, token, get] as const,
      ),
    ),
  )("%s(%s)下 %s 对静止表面都 ≥3.0", (_scope, _label, token, get) => {
    const t = get();
    // 深色没有覆写时会沿用到 :root——按级联后的有效值算，而不是只看 .dark 块。
    const effective = (name: string) => t[name] ?? root[name];
    const fill = effective(token);
    for (const surface of STATUS_DOT_SURFACES) {
      expect(
        contrast(fill, effective(surface)),
        `${token} ${fill} 落在 ${surface} ${effective(surface)} 上`,
      ).toBeGreaterThanOrEqual(GRAPHIC_MIN);
    }
  });
});

/**
 * 空闲点是四个状态里最容易被调没的一个：它没有软底、没有文字，全部信息就是这一枚
 * 圆点。亮色专门为「坐在 bg-secondary 槽里」调到了 3.04，暗色却一直是 #6a6d74——
 * 在同一个槽里与弹层里都只有 2.77，上面那条守卫只查 running / waiting，没拦住。
 * 无边框布局后会话列表坐在外框 --chrome 上，亮色 #8c8c93 在那里又掉到 2.83，
 * 于是改成 #84848b（外框 3.15）——这张表加进外框，就是为了下次调外框时拦住它。
 */
describe("空闲状态点对它会落到的表面达标", () => {
  it.each(THEMES)(
    "%s(%s)下 --status-idle 对状态点会落到的每个表面都 ≥3.0",
    (_scope, _label, get) => {
      const t = get();
      const effective = (name: string) => t[name] ?? root[name];
      for (const surface of STATUS_DOT_SURFACES) {
        expect(
          contrast(effective("--status-idle"), effective(surface)),
          `--status-idle ${effective("--status-idle")} 落在 ${surface} ${effective(surface)} 上`,
        ).toBeGreaterThanOrEqual(GRAPHIC_MIN);
      }
    },
  );

  it.each(THEMES)(
    "%s(%s)下它仍然比弱化文字暗 —— 空闲是最安静的一档",
    (_scope, _label, get) => {
      const t = get();
      for (const name of STATUS_DOT_SURFACES) {
        const surface = t[name];
        expect(
          contrast(t["--status-idle"], surface),
          `落在 ${name} 上时空闲点不该比弱化文字更显眼`,
        ).toBeLessThan(contrast(t["--muted-foreground"], surface));
      }
    },
  );
});

/**
 * 表面阶梯：外框 `--chrome`（标题栏、图标栏、各页左侧列表、Tab 条、右侧栏、状态栏）、
 * 内容页 `--sheet`（浮在外框上的工作区）、抬起 `--raised`（外框上的选中项）。
 *
 * 它们之间不画线，只靠色阶分开——所以「分得开」本身就是这里唯一的轮廓，掉到
 * SURFACE_MIN 以下，内容页就化进外框里了。
 */
const SURFACE_TOKENS = ["--chrome", "--sheet", "--raised"] as const;

/** 亮度次序：对纯黑的对比度随亮度单调递增。 */
const lightness = (hex: string) => contrast(hex, "#000000");

describe("外框、内容页、抬起三层表面", () => {
  it.each(SURFACE_TOKENS)(
    "%s 两个主题都有色值，并有 --color-* 映射",
    (name) => {
      expect(isHex(root[name]), `:root 里没有 ${name} 的色值`).toBe(true);
      expect(isHex(dark[name]), `.dark 里没有 ${name} 的色值`).toBe(true);
      expect(theme[`--color-${name.slice(2)}`]).toBe(`var(${name})`);
    },
  );

  it.each(THEMES)(
    "%s(%s)下内容页浮在外框上：比外框亮且分得开",
    (_scope, _label, get) => {
      const t = get();
      expect(lightness(t["--sheet"])).toBeGreaterThan(lightness(t["--chrome"]));
      expect(
        contrast(t["--chrome"], t["--sheet"]),
        `--chrome ${t["--chrome"]} 与 --sheet ${t["--sheet"]}`,
      ).toBeGreaterThanOrEqual(SURFACE_MIN);
    },
  );

  it.each(THEMES)(
    "%s(%s)下外框上的选中项抬起来：不暗于内容页，与外框分得开",
    (_scope, _label, get) => {
      const t = get();
      expect(lightness(t["--raised"])).toBeGreaterThanOrEqual(
        lightness(t["--sheet"]),
      );
      expect(
        contrast(t["--raised"], t["--chrome"]),
        `--raised ${t["--raised"]} 落在 --chrome ${t["--chrome"]} 上`,
      ).toBeGreaterThanOrEqual(SURFACE_MIN);
    },
  );
});

/**
 * 内容页这张「纸」的样子只定义一次：与外框之间留 8px 缝（上沿接 Tab 条、左沿接
 * 外框上的列表，所以只留右与下）、12px 圆角、圆角要裁得住子元素的底色；浅色带一圈
 * 极淡的投影把纸托起来，深色靠色阶已经够了、再加投影只会发脏。
 */
describe("content-sheet 工具类", () => {
  /** 取工具类里一条普通属性的值（decls 只收 `--*` 自定义属性）。 */
  const prop = (name: string) =>
    new RegExp(`(?:^|[;{\\s])${name}:\\s*([^;]+);`).exec(
      block("@utility content-sheet"),
    )?.[1];

  it("内容页底色、12px 圆角、右与下各 8px 缝、裁掉越界的子元素", () => {
    expect(prop("background-color")).toBe("var(--sheet)");
    expect(prop("border-radius")).toBe("0.75rem");
    expect(prop("margin")).toBe("0 0.5rem 0.5rem 0");
    expect(prop("overflow")).toBe("hidden");
  });

  it("投影走 --sheet-shadow：浅色有、深色没有", () => {
    expect(prop("box-shadow")).toBe("var(--sheet-shadow)");
    expect(root["--sheet-shadow"], ":root 里没有 --sheet-shadow").toBeDefined();
    expect(root["--sheet-shadow"]).not.toBe("none");
    expect(dark["--sheet-shadow"]).toBe("none");
  });
});

/**
 * 活动 Tab 与内容页连成一体：底部两侧各一道 8px 反向圆角，用内容页底色从 Tab 侧沿
 * 流进内容页上沿。非活动 Tab 的悬停底是内缩的胶囊，换上填充上的弱化文字色，和
 * bg-button-fill 一样。
 */
describe("Tab 与内容页的接合工具类", () => {
  it("tab-flare：两侧各一块 8px 的方块，贴底、伸出 Tab 外，用内容页底色画反向圆角", () => {
    const b = block("@utility tab-flare");
    expect(b).toMatch(/width:\s*0\.5rem;/);
    expect(b).toMatch(/height:\s*0\.5rem;/);
    expect(b).toMatch(/bottom:\s*0;/);
    expect(b).toMatch(/left:\s*-0\.5rem;/);
    expect(b).toMatch(/right:\s*-0\.5rem;/);
    expect(b).toMatch(/pointer-events:\s*none;/);
    expect(b).toMatch(
      /radial-gradient\(\s*circle at 0 0,[^;]*var\(--sheet\) 0\.5rem\s*\)/,
    );
    expect(b).toMatch(
      /radial-gradient\(\s*circle at 100% 0,[^;]*var\(--sheet\) 0\.5rem\s*\)/,
    );
  });

  it("tab-hover-pill：悬停时内缩的按钮填充，并把弱化文字换成填充上的那一档", () => {
    const b = block("@utility tab-hover-pill");
    expect(b).toMatch(/inset:\s*0\.25rem;/);
    expect(b).toMatch(/z-index:\s*-1;/);
    expect(b).toMatch(/--muted-foreground:\s*var\(--fill-muted-foreground\);/);
    expect(b).toMatch(/background-color:\s*var\(--color-button-fill\);/);
  });
});

/**
 * 字段与按钮不画边，靠「前景色按比例叠在脚下的表面上」的填充认出来：字段 5%（悬停
 * 7%）、按钮 8%（悬停 12%）。相对取色而不是定一个色值，是为了它落在内容页、填充
 * 分组、外框上时都比脚下深一级——一个定值做不到在三种底上都分得开。
 *
 * 代价是填充把底压暗，落在上面的弱化文字（说明、占位符）就不够 4.5 了：单靠下调
 * 比例走不通（浅色外框上的字段要降到 4%，那时与外框只剩 1.076）。所以和
 * `bg-accent` 同一个做法：填充工具类把子树里的 --muted-foreground 换成
 * --fill-muted-foreground。
 */
const FILL_ALPHAS = {
  "--color-field-fill": 0.05,
  "--color-field-fill-hover": 0.07,
  "--color-button-fill": 0.08,
  "--color-button-fill-hover": 0.12,
} as const;
const FILL_PAIRS = [
  ["--color-field-fill", "--color-field-fill-hover"],
  ["--color-button-fill", "--color-button-fill-hover"],
] as const;

/** `color-mix(in oklab, var(--foreground) N%, transparent)` → N/100；不是这个形状就是 undefined。 */
function fillAlpha(name: string): number | undefined {
  const m =
    /^color-mix\(\s*in oklab,\s*var\(--foreground\)\s+([\d.]+)%,\s*transparent\s*\)$/.exec(
      theme[name] ?? "",
    );
  return m ? Number(m[1]) / 100 : undefined;
}

/** 填充会落到的表面：内容页、外框、旧的卡片 / 弹层 / 页面底，以及填充分组（内容页 + 字段填充）。 */
function fillSurfaces(t: Record<string, string>): Array<[string, string]> {
  const out: Array<[string, string]> = [
    "--sheet",
    "--chrome",
    "--card",
    "--popover",
    "--background",
  ].map((name) => [name, t[name]]);
  const group = fillAlpha("--color-field-fill");
  if (group !== undefined) {
    out.push(["填充分组", mix(t["--foreground"], t["--sheet"], group)]);
  }
  return out;
}

describe("填充两档：字段与按钮", () => {
  it.each(Object.entries(FILL_ALPHAS))(
    "%s 是前景色 %s 叠在透明上（相对脚下取色）",
    (name, alpha) => {
      expect(theme[name], `@theme 里没有 ${name}`).toBeDefined();
      expect(fillAlpha(name), `${name}: ${theme[name]}`).toBe(alpha);
    },
  );

  it.each(
    Object.keys(FILL_ALPHAS).map((n) => `bg-${n.slice("--color-".length)}`),
  )(
    "@utility %s 把 --muted-foreground 指向 --fill-muted-foreground",
    (name) => {
      expect(
        decls(block(`@utility ${name}`))["--muted-foreground"],
        `tokens.css 里没有 @utility ${name}，或它没有重指 --muted-foreground`,
      ).toBe("var(--fill-muted-foreground)");
    },
  );

  it.each(
    THEMES.flatMap(([scope, label, get]) =>
      FILL_PAIRS.map(
        ([rest, hover]) => [scope, label, rest, hover, get] as const,
      ),
    ),
  )(
    "%s(%s)下 %s 与悬停 %s 对脚下每种表面都分得开，弱化文字在上面达标",
    (_scope, _label, rest, hover, get) => {
      const t = get();
      const [a, b] = [fillAlpha(rest), fillAlpha(hover)];
      expect(a, `${rest} 不是相对填充`).toBeDefined();
      expect(b, `${hover} 不是相对填充`).toBeDefined();
      expect(
        t["--fill-muted-foreground"],
        "tokens.css 里没有 --fill-muted-foreground",
      ).toBeDefined();
      for (const [name, surface] of fillSurfaces(t)) {
        const restFill = mix(t["--foreground"], surface, a!);
        const hoverFill = mix(t["--foreground"], surface, b!);
        expect(
          contrast(restFill, surface),
          `${rest} 叠在 ${name} ${surface} 上得到 ${restFill}`,
        ).toBeGreaterThanOrEqual(SURFACE_MIN);
        expect(
          contrast(hoverFill, surface),
          `${hover} 落在 ${name} 上应当比静止态更深`,
        ).toBeGreaterThan(contrast(restFill, surface));
        for (const fill of [restFill, hoverFill]) {
          expect(
            contrast(t["--fill-muted-foreground"], fill),
            `--fill-muted-foreground ${t["--fill-muted-foreground"]} 落在 ${name} 上的填充 ${fill}`,
          ).toBeGreaterThanOrEqual(TEXT_MIN);
        }
      }
    },
  );
});

/**
 * 边的阶梯改成「分隔线 / 填充两档 + 控件边」。
 *
 * 字段与 outline 按钮不再画边，从边的阶梯挪到了填充阶梯（字段 5% 一档、按钮 8%
 * 一档）；剩下的边只有两种：分隔线 `--border`（只留给明确保留的少数用途），和
 * 轮廓即控件的 `--control-border`（未选中的复选框 / 单选——丢了边控件就没了，
 * 所以它守 WCAG 2.2 §1.4.11 的 3:1，并且比任何填充都实）。
 */
describe("分隔线 / 填充两档 + 控件边", () => {
  /** 表单控件会落在的表面：内容页、表单卡片、页面底、灰槽、弹层。 */
  const CONTROL_SURFACES = [
    "--sheet",
    "--card",
    "--background",
    "--secondary",
    "--popover",
  ] as const;

  it.each(THEMES)(
    "%s(%s)下字段填充 < 按钮填充 < 控件边，分隔线也比控件边轻",
    (_scope, _label, get) => {
      const t = get();
      const field = fillAlpha("--color-field-fill");
      const button = fillAlpha("--color-button-fill");
      expect(field).toBeDefined();
      expect(button).toBeDefined();
      for (const name of CONTROL_SURFACES) {
        const surface = t[name];
        const steps: Array<[string, number]> = [
          [
            "字段填充",
            contrast(mix(t["--foreground"], surface, field!), surface),
          ],
          [
            "按钮填充",
            contrast(mix(t["--foreground"], surface, button!), surface),
          ],
          ["--control-border", contrast(t["--control-border"], surface)],
        ];
        for (let i = 1; i < steps.length; i++) {
          expect(
            steps[i][1],
            `${steps[i][0]} 落在 ${name} 上应当比 ${steps[i - 1][0]} 实`,
          ).toBeGreaterThan(steps[i - 1][1]);
        }
        expect(
          contrast(t["--border"], surface),
          `--border 落在 ${name} 上应当比 --control-border 轻`,
        ).toBeLessThan(contrast(t["--control-border"], surface));
      }
    },
  );

  it.each(THEMES)(
    "%s(%s)下 --control-border 对控件会落到的每个表面都 ≥3.0",
    (_scope, _label, get) => {
      const t = get();
      for (const name of CONTROL_SURFACES) {
        expect(
          contrast(t["--control-border"], t[name]),
          `--control-border ${t["--control-border"]} 落在 ${name} ${t[name]} 上`,
        ).toBeGreaterThanOrEqual(GRAPHIC_MIN);
      }
    },
  );
});

/**
 * 设置对话框压在当前页面上的那层遮罩：规格 2026-10-15「设置对话框 · 外观」定为
 * 「浅色约 32%、深色约 55% 的黑」。它比 --overlay-scrim（表单弹窗用）轻：设置几乎
 * 铺满窗口，底下那一页只需要退后，不需要被抹掉。
 */
describe("设置对话框的遮罩", () => {
  it.each([
    [":root", "亮色", () => root, "rgba(0, 0, 0, 0.32)"] as const,
    [".dark", "暗色", () => dark, "rgba(0, 0, 0, 0.55)"] as const,
  ])("%s(%s)下 --settings-scrim 是 %s", (_s, _l, get, expected) => {
    expect(get()["--settings-scrim"]).toBe(expected);
  });

  it("有 --color-settings-scrim 映射（bg-settings-scrim）", () => {
    expect(theme["--color-settings-scrim"]).toBe("var(--settings-scrim)");
  });
});

/**
 * 弹层与对话框只靠表面色 + 阴影立起来，阴影不再自带一圈 1px 的环——那圈环就是一条
 * 结构线，换了个写法而已。
 */
describe("浮层阴影不带描边环", () => {
  /** 按顶层逗号切开阴影的各层（rgba(...) 里的逗号不算）。 */
  const layers = (value: string) => {
    const out: string[] = [];
    let depth = 0;
    let start = 0;
    for (let i = 0; i < value.length; i++) {
      if (value[i] === "(") depth++;
      else if (value[i] === ")") depth--;
      else if (value[i] === "," && depth === 0) {
        out.push(value.slice(start, i).trim());
        start = i + 1;
      }
    }
    out.push(value.slice(start).trim());
    return out;
  };

  it.each(THEMES)(
    "%s(%s)下 --overlay-shadow 没有零偏移零模糊的环",
    (_s, _l, get) => {
      const shadow = get()["--overlay-shadow"];
      expect(shadow, "tokens.css 里没有 --overlay-shadow").toBeDefined();
      const all = layers(shadow);
      expect(all.length).toBeGreaterThan(0);
      for (const layer of all) {
        expect(
          layer,
          `--overlay-shadow 里有一层是描边环：${layer}`,
        ).not.toMatch(/^0(px)? 0(px)? 0(px)? [\d.]+px\b/);
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
  it("--agent-neutral 有声明与 --color-* 映射，且亮色的白色字形压得住", () => {
    expect(
      root["--agent-neutral"],
      "tokens.css 里没有 --agent-neutral",
    ).toBeDefined();
    expect(theme["--color-agent-neutral"]).toBe("var(--agent-neutral)");
    // 亮色前景是白的（深色另有墨字覆写，见 .dark），所以中性档在亮色里必须
    // 深到读得出白字。#525252 压白 7.81。两套主题的完整配对由下面「身份色上
    // 的前景在两个主题都读得出」那条守卫统一把关。
    expect(
      contrast(root["--agent-foreground"], root["--agent-neutral"]),
      `--agent-foreground ${root["--agent-foreground"]} 落在 --agent-neutral ${root["--agent-neutral"]} 上`,
    ).toBeGreaterThanOrEqual(TEXT_MIN);
  });
});

/**
 * 身份色上的前景。
 *
 * `--agent-foreground` 两主题都是白字，读不读得出全看底色：深色身份色曾是提亮过的
 * 300-400 档，白字最差只有 2.54:1（agent-1）；浅色也有 600 档压白只到 2.94
 * （agent-14）。所以守的是「17 档 × 两主题」每一格的有效值，中性档
 * `--agent-neutral` 一起算——它换了值就可能掉到白字压不住。
 */
const AGENT_COLOR_TOKENS = [
  ...Array.from({ length: 16 }, (_, i) => `--agent-${i + 1}`),
  "--agent-neutral",
];

describe("身份色上的字形两个主题都是白字", () => {
  it("深色不把 --agent-foreground 翻成墨字：暗色靠压暗底色读得出，不靠换字色", () => {
    // 深色曾经把底色提亮到 300-400 档再压墨字，头像在深色界面里一片发亮、字显得
    // 又细又小。现在两主题都压白字，深色的 17 档底色各自压暗到白字达标。
    const effective = (name: string) => dark[name] ?? root[name];
    expect(root["--agent-foreground"]).toBe("#ffffff");
    expect(effective("--agent-foreground")).toBe("#ffffff");
  });
});

describe("身份色上的前景在两个主题都读得出", () => {
  it.each(THEMES)(
    "%s(%s)下 --agent-foreground 压得住全部 17 档身份色",
    (_scope, _label, get) => {
      const t = get();
      // 深色若不覆写，CSS 会沿用到 :root 的值——那正是旧的 bug，所以按级联后的
      // 有效值算，而不是只看 .dark 块。
      const effective = (name: string) => t[name] ?? root[name];
      const fg = effective("--agent-foreground");
      for (const token of AGENT_COLOR_TOKENS) {
        const fill = effective(token);
        expect(
          contrast(fg, fill),
          `${fg} 落在 ${token} ${fill} 上`,
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

// ─── 布局不变量:只有一条,住在这里是因为声明就在 tokens.css ────────────────
describe("@mention chip 不许撑破它所在的列", () => {
  // chip 刻意 white-space:nowrap —— 名字从中间折断不可读。代价是它必须自己封顶:
  // 没有 max-width 时,一个长项目名把 chip 撑到列宽之外(实测 320px 列溢出 259px),
  // 转录里画到右侧栏上、输入框里顶穿输入框。封顶后超出部分收成省略号。
  const chip = block(".agentre-mention");

  it("有 max-width 封顶", () => {
    expect(chip).toMatch(/max-width:\s*100%/);
  });

  it("超出的部分收成省略号", () => {
    expect(chip).toMatch(/overflow:\s*hidden/);
    expect(chip).toMatch(/text-overflow:\s*ellipsis/);
  });

  it("不是 flex 容器 —— 匿名 flex item 出不了省略号", () => {
    expect(chip).not.toMatch(/display:\s*inline-flex/);
  });
});
