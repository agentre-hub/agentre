import fs from "node:fs";
import path from "node:path";
import { ESLint } from "eslint";
import { beforeAll, describe, expect, it } from "vitest";

import {
  AGENT_COLOR_AS_TEXT,
  ARBITRARY_FONT_SIZE,
  FILL_TEXT_ON_OWN_SOFT_BG,
  HAND_ROLLED_FOCUS_RING,
  LITERAL_COLOR_CLASS,
  RAW_COLOR_VALUE,
  RESTING_SURFACE_AS_HOVER,
  DIVIDER_COLOR_EDGE,
  DIVIDE_UTILITY,
  FIELD_EDGE,
  agentColorAsTextSyntax,
  restrictedSyntax,
} from "../../eslint-rules/design-tokens.js";

/**
 * 设计 token 守卫的守卫（字号、颜色，以及「token 用对了角色没有」的四组）。
 *
 * 两件事要钉住：
 *   1. **正则真的拦得住 / 放得过**。规则写错的表现不是报错，是它默默不生效——
 *      lint 全绿，而字面像素继续攒（这一轮扫掉的 109 处就是这么攒起来的）。
 *   2. **eslint.config.js 消费的是同一份来源**。规则数据单独成模块的全部理由就是
 *      这个：否则守卫测试可能在测一份和实际生效的配置不一样的正则。
 *
 * 字号那组的判据是「这个尺寸有没有对应的 token」而不是「所有字面像素都禁」——
 * 8 / 9px 与展示字号目前没有档，放行是有意的（见 eslint-rules/design-tokens.js）。
 * 颜色那组没有这种「暂时没档」的放行：调色板色名一律禁，确实拿不到 token 的地方
 * （xterm 的 theme API）在 eslint.config.js 里按文件豁免并写明理由。
 */
const FRONTEND_ROOT = path.resolve(__dirname, "../..");
const matcher = new RegExp(ARBITRARY_FONT_SIZE);
const colorClass = new RegExp(LITERAL_COLOR_CLASS);
const rawColor = new RegExp(RAW_COLOR_VALUE);
const fillTextOnSoft = new RegExp(FILL_TEXT_ON_OWN_SOFT_BG);
const handRolledFocusRing = new RegExp(HAND_ROLLED_FOCUS_RING);
const agentColorAsText = new RegExp(AGENT_COLOR_AS_TEXT);
const restingSurfaceAsHover = new RegExp(RESTING_SURFACE_AS_HOVER);
const structuralLine = [DIVIDER_COLOR_EDGE, DIVIDE_UTILITY, FIELD_EDGE].map(
  (source) => new RegExp(source),
);
const drawsStructuralLine = (sample: string) =>
  structuralLine.some((pattern) => pattern.test(sample));

describe("字号阶梯的 lint 规则", () => {
  it.each([
    ['className="text-[10px]"', "10px 有 text-3xs"],
    ['className="text-[11px]"', "11px 有 text-2xs"],
    ['className="text-[12px]"', "12px 有 text-xs"],
    ['className="text-[13px]"', "13px 有 text-aux"],
    ['className="text-[14px]"', "14px 有 text-sm"],
    ['className="text-[15px]"', "15px 有 text-prose"],
    ['className="flex items-center text-[13px] font-medium"', "夹在长串里"],
    ['className="sm:text-[15px]"', "带断点变体"],
    ['className="group-hover:text-[11px]"', "带组合变体"],
  ])("拦住 %s（%s）", (sample) => {
    expect(matcher.test(sample)).toBe(true);
  });

  it.each([
    ['className="text-[8px]"', "徽标字形，没有对应档"],
    ['className="text-[9px]"', "设备指纹，没有对应档"],
    ['className="text-[22px]"', "展示字号，没有对应档"],
    ['className="text-2xs"', "已经是阶梯上的名字"],
    ['className="text-aux"', "已经是阶梯上的名字"],
    ['className="max-w-[520px]"', "不是字号"],
  ])("放过 %s（%s）", (sample) => {
    expect(matcher.test(sample)).toBe(false);
  });

  it("规则形态覆盖普通字符串与模板字符串两种写法", () => {
    // 只拦 Literal 的话，`` cn(`text-[13px] ${x}`) `` 会绕过去。
    const selectors = restrictedSyntax.map((rule) => rule.selector);

    expect(selectors.some((s) => s.startsWith("Literal["))).toBe(true);
    expect(selectors.some((s) => s.startsWith("TemplateElement["))).toBe(true);
  });

  it("eslint.config.js 用的就是这一份规则数据", () => {
    const config = fs.readFileSync(
      path.join(FRONTEND_ROOT, "eslint.config.js"),
      "utf8",
    );

    expect(config).toContain("./eslint-rules/design-tokens.js");
    expect(config).toContain("...restrictedSyntax");
  });
});

describe("调色板字面色类的 lint 规则", () => {
  it.each([
    ['className="bg-slate-900"', "带色阶"],
    ['className="text-white"', "不带色阶"],
    ['className="dark:bg-black/70"', "变体 + 不透明度"],
    ['className="text-red-500/50"', "色阶 + 不透明度同时出现"],
    ['className="flex items-center bg-zinc-100 p-2"', "夹在长串里"],
    ['className="bg-neutral-600"', "身份色板缺中性档时的写法"],
  ])("拦住 %s（%s）", (sample) => {
    expect(colorClass.test(sample)).toBe(true);
  });

  it.each([
    ['className="bg-background"', "语义 token"],
    ['className="text-foreground"', "语义 token"],
    ['className="bg-scrim"', "语义 token"],
    ['className="text-agent-foreground"', "身份色上的前景"],
    ['className="bg-agent-15"', "身份色板里的一档"],
    ['className="text-status-waiting"', "语义 token"],
  ])("放过 %s（%s）", (sample) => {
    expect(colorClass.test(sample)).toBe(false);
  });
});

describe("写死颜色值的 lint 规则", () => {
  it.each([
    ['const c = "#0f172a";', "六位十六进制"],
    ['const c = "rgba(0, 0, 0, .5)";', "rgba()"],
    ['const c = "hsl(210 40% 98%)";', "hsl()"],
  ])("拦住 %s（%s）", (sample) => {
    expect(rawColor.test(sample)).toBe(true);
  });

  it.each([
    ['const c = "var(--background)";', "引 token"],
    [
      'const c = "color-mix(in oklab, var(--primary) 28%, transparent)";',
      "由 token 混出",
    ],
  ])("放过 %s（%s）", (sample) => {
    expect(rawColor.test(sample)).toBe(false);
  });
});

/**
 * 饱和填充色落在**同族的软底**上当文字。
 *
 * 填充色是为圆点 / 实底调的，压在自己的淡色底上读不出来：亮色下
 * `#109066` 在 `--status-running-bg` 上 3.83、`#dc2626` 在 `bg-destructive/10` 上 4.14。
 * 仓里早就有 `-text` 角色，但大多数胶囊没换过去（2026-10-13 审计：running 64 处对 4 处）。
 * 这组规则只管得住「软底和文字色写在同一个类名串里」的情形——那是唯一不用猜
 * 「这是文字还是图标」就能判的写法，也是对比度掉得最狠的地方。
 */
describe("软底上的同族文字必须用 -text 角色", () => {
  it.each([
    ["bg-status-running-bg text-status-running", "运行胶囊"],
    [
      "text-status-waiting bg-status-waiting-bg px-1.5",
      "顺序反过来、夹在长串里",
    ],
    ["rounded-full bg-destructive/10 text-destructive", "不透明度冲出来的红底"],
    [
      "bg-destructive-soft text-status-error",
      "错误胶囊：status-error 与 destructive 同族",
    ],
    ["bg-primary-soft text-primary", "品牌软底"],
    [
      "data-[variant=destructive]:text-destructive data-[variant=destructive]:hover:bg-destructive/10",
      "带变体前缀",
    ],
    ["bg-status-running/10 text-status-running/80", "两边都带不透明度"],
  ])("拦住 %s（%s）", (sample) => {
    expect(fillTextOnSoft.test(sample)).toBe(true);
  });

  it.each([
    ["bg-status-running-bg text-status-running-text", "已经是文字角色"],
    ["bg-destructive-soft text-destructive-text", "已经是文字角色"],
    ["bg-primary-soft text-primary-text", "已经是文字角色"],
    [
      "bg-destructive text-destructive-foreground hover:bg-destructive/90",
      "实底上的前景",
    ],
    ["bg-primary text-primary-foreground hover:bg-primary/90", "实底上的前景"],
    ["size-3.5 text-status-running", "没有软底：单独的图标用填充色是对的"],
    ["bg-status-running-bg text-foreground", "软底上的中性文字"],
    ["bg-status-waiting-bg text-status-running", "不同族，不归这条规则管"],
    ["size-1.5 rounded-full bg-destructive", "圆点"],
  ])("放过 %s（%s）", (sample) => {
    expect(fillTextOnSoft.test(sample)).toBe(false);
  });
});

/**
 * 键盘焦点只用 tokens.css 里定义的工具类：按钮与字段都是 `focus-outline`
 * （`focus-halo` 仍在，无边的字段不再用它）。各处自己拼 `ring-[3px] ring-ring/40` 的结果是全仓并存三种环
 * （/40 37 处、/50 36 处、实色 6 处），半透明的那两种合成后只有 1.8–2.1:1。
 * `ring-ring` 这个组合只该出现在工具类的定义里，所以 ts / tsx 里一律不许写。
 */
describe("焦点环不许手拼", () => {
  it.each([
    ["focus-visible:ring-[3px] focus-visible:ring-ring/50", "半透明光晕"],
    [
      "focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring/40",
      "另一档不透明度",
    ],
    ["focus-visible:ring-2 focus-visible:ring-ring", "实色，但仍是自己拼的"],
    ["data-[state=open]:ring-ring/30", "任意变体前缀"],
    ["flex ring-2 ring-ring", "不带变体"],
  ])("拦住 %s（%s）", (sample) => {
    expect(handRolledFocusRing.test(sample)).toBe(true);
  });

  it.each([
    ["focus-visible:focus-outline", "按钮类的工具类"],
    ["focus-within:focus-halo", "工具类，字段不再用但仍可用"],
    ["ring-1 ring-primary/30 ring-inset", "装饰描边，不是焦点环"],
    ["aria-invalid:ring-destructive/20", "校验态的环"],
    ["ring-offset-background", "只是名字里带 ring"],
    ["border-l-2 border-l-ring", "拿 ring 色画边，不是环"],
    ["focus-visible:border-ring", "实色的边"],
  ])("放过 %s（%s）", (sample) => {
    expect(handRolledFocusRing.test(sample)).toBe(false);
  });
});

/**
 * 身份色只上图形。
 *
 * `--agent-1…16` 是为「色块 + 白字」调的：暗色下 16 个都压暗到托得住白字，
 * 当文字对 card 只有 3.5；亮色落在 secondary 上也有 12 个低于 4.5。
 * 图标只要 3:1，所以它可以带身份色——但类名只该来自 `agentIconColorClassNames`
 * 那一张表（components/agentre/types.ts，在 eslint.config.js 里按文件豁免），
 * 别处一律不写 `text-agent-N`，免得又被顺手挂到一段文字上。
 */
describe("身份色不许当文字色写", () => {
  it.each([
    ["text-sm font-semibold text-agent-1", "标题"],
    ["inline-flex text-xs text-agent-12 hover:underline", "链接"],
    ["hover:text-agent-3", "带变体"],
  ])("拦住 %s（%s）", (sample) => {
    expect(agentColorAsText.test(sample)).toBe(true);
  });

  it.each([
    ["bg-agent-7 text-agent-foreground", "色块 + 白字，身份色的本职"],
    ["border-agent-1/30 bg-agent-1/5", "描边与软底"],
    ["text-agent-foreground", "色块上的字形"],
  ])("放过 %s（%s）", (sample) => {
    expect(agentColorAsText.test(sample)).toBe(false);
  });
});

/**
 * 身份色类名表那一份按文件豁免，过的是**项目真实的** eslint.config.js。
 *
 * 豁免写错有两种不报错的样子：没生效（类名表自己被 lint 拦下，这个会红）与
 * 放得太宽（把整条 `no-restricted-syntax` 对那份文件关掉，其余几组跟着失守，
 * 这个不会红）。读配置文本看不出后一种，所以让 ESLint 真跑一遍。
 */
describe("身份色类名表的按文件豁免", () => {
  const AGENT_TEXT = agentColorAsTextSyntax[0].message;
  const HOVER = restrictedSyntax.find((rule) =>
    rule.selector.includes(RESTING_SURFACE_AS_HOVER),
  )!.message;
  const SAMPLE =
    'export const icon = "text-agent-3";\n' +
    'export const row = "px-2 hover:bg-muted";\n';

  let eslint: ESLint;

  // 预热单独摆在 beforeAll，理由同 eslint-native-controls.test.ts：第一次 lintText
  // 要解析整份配置，冷跑好几秒。
  beforeAll(async () => {
    eslint = new ESLint({ cwd: FRONTEND_ROOT, errorOnUnmatchedPattern: false });
    await eslint.lintText("export const warmup = 1;\n", {
      filePath: path.join(FRONTEND_ROOT, "src/warmup.ts"),
    });
  }, 60_000);

  async function lintAs(filePath: string) {
    const [result] = await eslint.lintText(SAMPLE, {
      filePath: path.join(FRONTEND_ROOT, filePath),
    });
    return result.messages
      .filter((m) => m.ruleId === "no-restricted-syntax")
      .map((m) => m.message);
  }

  it("Given text-agent-N in the class table file, When ESLint runs, Then only the identity-color rule is lifted", async () => {
    const messages = await lintAs("src/components/agentre/types.ts");

    expect(messages).not.toContain(AGENT_TEXT);
    expect(messages).toContain(HOVER);
  });

  it("Given the same text in any other file, When ESLint runs, Then the identity-color rule reports it", async () => {
    const messages = await lintAs("src/components/agentre/sample.ts");

    expect(messages).toContain(AGENT_TEXT);
    expect(messages).toContain(HOVER);
  });
});

/**
 * hover 不许用静止表面色。
 *
 * `--muted` 与 `--secondary` 是页面上不表示交互状态的底色，而且各自与别的表面同值：
 * 暗色 muted ≡ card、secondary ≡ popover，亮色 secondary ≡ sidebar。拿它们当悬停
 * 反馈，落在同值的面上就是 1.00——不报错、不崩，只是鼠标移上去没有任何变化。
 * `--accent` 的定义就是「不等于任何静止表面的反馈色」（tokens.test.ts 守着）。
 */
describe("悬停反馈不许用静止表面色", () => {
  it.each([
    ["rounded-md px-2 hover:bg-muted", "实底"],
    ["hover:bg-muted/50", "带不透明度"],
    ["text-xs hover:bg-secondary/60 hover:text-foreground", "夹在长串里"],
    ["group-hover:bg-secondary", "group 变体"],
    ["[a&]:hover:bg-secondary/90", "前面还有别的变体"],
  ])("拦住 %s（%s）", (sample) => {
    expect(restingSurfaceAsHover.test(sample)).toBe(true);
  });

  it.each([
    ["hover:bg-accent", "反馈色"],
    ["hover:bg-accent/60 hover:on-accent", "半透明的反馈色"],
    ["bg-muted", "静止态用静止表面色，本职"],
    ["bg-secondary text-secondary-foreground", "静止态"],
    ["hover:bg-secondary-foreground", "名字里带 secondary 的另一个 token"],
    ["hover:bg-card", "标签条上向 card 提亮，有意为之"],
    ["hover:bg-rail-accent", "外壳带上的反馈色"],
  ])("放过 %s（%s）", (sample) => {
    expect(restingSurfaceAsHover.test(sample)).toBe(false);
  });
});

/**
 * 结构线：同一底色的两块区域之间用线分开。
 *
 * 无边框改版（docs/specs/2026-10-15-borderless-ui.md「结构线」「控件」）之后，
 * 区域靠表面色阶与留白分开，卡片、字段、outline 按钮靠填充；页面上会「出现」的线
 * 只剩焦点环、复选框 / 单选 / 开关的边、状态色条与拖放落点。
 * 这组规则只认**颜色**：分隔线色（border-border、border-sidebar-border 这类
 * border-*-border）、divide-* 与字段 / 按钮的边色。不按宽度判，是因为状态色条、
 * 拖放落点与控件边用的是同一批宽度类，区别只在颜色上；而分隔线色除了画线别无用途。
 */
describe("结构线的 lint 规则", () => {
  it.each([
    [
      "flex items-center gap-1 border-b border-border px-3 py-2",
      "面板头下的分隔线",
    ],
    ["border-t border-border bg-secondary/40 px-3 py-2", "页脚上的分隔线"],
    ["border-t-2 border-border", "带宽度的单边"],
    ["border-l border-sidebar-border", "border-*-border 一族"],
    ["rounded-md border border-border bg-card px-2.5 py-2", "卡片外框"],
    ["flex items-center border-b border-border/60", "带不透明度"],
    ["border-border bg-card hover:bg-accent", "宽度写在 cn() 的另一个字面量里"],
    ["dark:border-border", "带变体"],
    ["border-b-border", "单边的颜色写法"],
    ["flex flex-col divide-y divide-border", "列表行之间的分隔线"],
    ["divide-x", "不带颜色的 divide"],
    ["[&>li]:divide-y", "任意变体前缀的 divide"],
    ["h-9 rounded-md border border-field-border bg-transparent", "字段的边"],
    ["border border-input bg-background shadow-xs", "outline 按钮的边"],
    ["dark:border-input", "带变体的字段边"],
  ])("拦住 %s（%s）", (sample) => {
    expect(drawsStructuralLine(sample)).toBe(true);
  });

  it.each([
    [
      "size-4 rounded-sm border border-control-border",
      "复选框 / 单选的边是控件本体",
    ],
    ["border border-control-border bg-transparent shadow-xs", "开关类控件的边"],
    ["border-l-2 border-l-status-running", "子代理运行组的状态色条"],
    [
      "flex flex-col gap-px border-l-2 border-status-waiting/40 pl-1.5",
      "「需要关注」的琥珀色左条",
    ],
    ["border-2 border-dashed border-ring bg-background/85", "拖放落点提示"],
    ["focus-visible:focus-outline", "按钮类焦点环"],
    ["focus-within:focus-outline", "字段包裹层的焦点环"],
    ["focus-within:focus-halo", "focus-halo 工具类"],
    ["-mx-1 my-1 h-px bg-border", "菜单分隔是一条填充，不是边"],
    [
      "mt-1 size-2 rounded-full border border-border-strong",
      "手写单选点的控件边",
    ],
    ["data-[state=checked]:border-primary", "选中态的控件边"],
    ["bg-field-fill hover:bg-field-fill-hover", "字段的填充"],
    ["bg-button-fill hover:bg-button-fill-hover", "按钮的填充"],
    ["task-pill-divide", "名字里带 divide 的标识"],
    ["border-separate border-spacing-x-3", "表格布局，不画线"],
  ])("放过 %s（%s）", (sample) => {
    expect(drawsStructuralLine(sample)).toBe(false);
  });
});

/**
 * 规则要在宿主与共享包两边都生效，过的是**项目真实的** eslint.config.js：
 * 共享包 ui/** 有自己的一块配置（只摘原生控件那组），漏展开 restrictedSyntax
 * 的话那一层就没人看着；测试文件照其它几组的惯例豁免。
 */
describe("结构线规则的生效范围", () => {
  const SAMPLE = 'export const head = "flex border-b border-border px-3";\n';
  const STRUCTURAL = new Set(
    restrictedSyntax
      .filter((rule) =>
        [DIVIDER_COLOR_EDGE, DIVIDE_UTILITY, FIELD_EDGE].some((source) =>
          rule.selector.includes(source),
        ),
      )
      .map((rule) => rule.message),
  );

  let eslint: ESLint;

  beforeAll(async () => {
    eslint = new ESLint({ cwd: FRONTEND_ROOT, errorOnUnmatchedPattern: false });
    await eslint.lintText("export const warmup = 1;\n", {
      filePath: path.join(FRONTEND_ROOT, "src/warmup.ts"),
    });
  }, 60_000);

  async function structuralMessages(filePath: string) {
    const [result] = await eslint.lintText(SAMPLE, {
      filePath: path.join(FRONTEND_ROOT, filePath),
    });
    return result.messages.filter(
      (m) => m.ruleId === "no-restricted-syntax" && STRUCTURAL.has(m.message),
    );
  }

  it.each([
    "src/components/agentre/sample.tsx",
    "packages/agentre-ui/src/ui/sample.tsx",
    "packages/agentre-ui/src/transcript/sample.tsx",
  ])(
    "Given a divider line in %s, When ESLint runs, Then it is reported",
    async (file) => {
      expect(await structuralMessages(file)).toHaveLength(1);
    },
  );

  it.each([
    "src/components/agentre/__tests__/sample.test.tsx",
    "packages/agentre-ui/src/ui/sample.test.tsx",
  ])(
    "Given the same line in test file %s, When ESLint runs, Then it is exempt",
    async (file) => {
      expect(await structuralMessages(file)).toHaveLength(0);
    },
  );
});

describe("每一组规则都有两种形态", () => {
  it("每组各自都覆盖 Literal 与 TemplateElement", () => {
    // 只拦 Literal 的话，模板字符串里的写法会绕过去。
    const shapes = (source: string) =>
      restrictedSyntax
        .filter((rule) => rule.selector.includes(source))
        .map((rule) => rule.selector.split("[")[0])
        .sort();

    for (const source of [
      AGENT_COLOR_AS_TEXT,
      ARBITRARY_FONT_SIZE,
      FILL_TEXT_ON_OWN_SOFT_BG,
      HAND_ROLLED_FOCUS_RING,
      LITERAL_COLOR_CLASS,
      RAW_COLOR_VALUE,
      RESTING_SURFACE_AS_HOVER,
      DIVIDER_COLOR_EDGE,
      DIVIDE_UTILITY,
      FIELD_EDGE,
    ]) {
      expect(shapes(source)).toEqual(["Literal", "TemplateElement"]);
    }
  });
});
