/**
 * 设计 token 守卫的规则数据。
 *
 * 单独抽成模块，是为了让 eslint.config.js 和守卫测试
 * （src/__tests__/eslint-design-tokens.test.ts）引用同一份来源——
 * 否则守卫测试可能在测一份和实际生效的配置不一样的正则。
 *
 * 两组规则：颜色与字号。两者的正则源都与 agentre-server 的同名模块**逐字一致** ——
 * 两端共用同一套 token 与同一套阶梯，规则分叉了就等于约定分叉了。
 *
 * 颜色这组是后补的：本仓此前一条 no-restricted-syntax 都没有，Dialog 遮罩里那句
 * bg-slate-900/25 能活到今天正是因为这个。
 *
 * 其后四组管的是「token 用对了角色没有」：软底上的同族文字、焦点环、身份色当文字、
 * 拿静止表面色做悬停。最后一组管结构线（无边框改版之后，分隔线色、divide-*、
 * 字段与 outline 按钮的边一律不许出现）。
 * 它们是本仓先加的，agentre-server 的同名模块里还没有——那边钉上带这些工具类的
 * 共享包之后要一并补上，否则同一套 token 两端各执一词。
 */

/** 任意变体前缀，如 sm: / dark: / hover: / group-hover: */
const VARIANT = "(?:[a-z-]+:)*";

/** Tailwind 自带调色板的色名。用了它们就是绕过了 token 层。 */
const PALETTE = [
  "red",
  "orange",
  "amber",
  "yellow",
  "lime",
  "green",
  "emerald",
  "teal",
  "cyan",
  "sky",
  "blue",
  "indigo",
  "violet",
  "purple",
  "fuchsia",
  "pink",
  "rose",
  "slate",
  "gray",
  "grey",
  "zinc",
  "neutral",
  "stone",
  "black",
  "white",
].join("|");

/** 会吃颜色的工具类前缀。 */
const COLOR_UTILITIES = [
  "text",
  "bg",
  "border",
  "ring",
  "from",
  "to",
  "via",
  "shadow",
  "fill",
  "stroke",
  "outline",
  "divide",
  "accent",
  "caret",
  "placeholder",
].join("|");

/**
 * 例：bg-slate-900/25、dark:bg-black/70、text-white、text-red-500/50
 *
 * 色阶和不透明度必须分成两个独立可选段：`text-red-500/50` 两者都有，
 * 合成一段（如 `(?:[-/][0-9]+)?`）只能吃掉其中一个，会漏掉这种最常见的写法。
 */
const SHADE = "(?:-[0-9]+)?";
// `/` 必须转义：no-restricted-syntax 的 selector 语法是 `Literal[value=/.../]`，
// 裸斜杠会被 esquery 当成正则结束符，报 "Unterminated group"。
const OPACITY = String.raw`(?:\/[0-9.]+)?`;
const LITERAL_COLOR_CLASS = `(?:^|[\\s"'\`])${VARIANT}(?:${COLOR_UTILITIES})-(?:${PALETTE})${SHADE}${OPACITY}(?:$|[\\s"'\`])`;

/** 例：#0f172a、rgba(0,0,0,.5)、hsl(210 40% 98%) */
const RAW_COLOR_VALUE = "(?:#[0-9a-fA-F]{3,8}\\b|\\b(?:rgba?|hsla?)\\s*\\()";

/**
 * 阶梯上**已经有 token** 的那几档像素值。
 *
 * 判据刻意是「这个尺寸有没有对应的 token」，不是「所有字面像素都禁」：
 *   10 → text-3xs   11 → text-2xs   12 → text-xs
 *   13 → text-aux   14 → text-sm    15 → text-prose
 * 8 / 9px 与 16px 以上的展示字号目前没有对应档（徽标字形、设备确认码这类
 * 一两处的特例），为它们各造一个 token 是把阶梯撑成词汇表，所以放行。
 * 哪天它们也成了档，往这个列表里加一个数字即可。
 */
const TOKENED_FONT_SIZES = "10|11|12|13|14|15";

/**
 * 例：text-[13px]、sm:text-[15px]、group-hover:text-[11px]
 *
 * 前后各要一个边界，避免吃到 `mytext-[12px]` 这种不存在但正则会误判的串。
 * 不匹配收尾的 `]`：`[` 已经足够定位，少一个方括号就少一处 esquery 选择器
 * 解析上的不确定（那层语法里只有 `/` 一定要转义，但方括号能不写就不写）。
 */
const ARBITRARY_FONT_SIZE = `(?:^|[\\s"'\`])${VARIANT}text-\\[(?:${TOKENED_FONT_SIZES})px`;

/**
 * 软底 → 落在它上面的同族「填充色文字」。
 *
 * 状态色、危险色、品牌色各有两个值：饱和的那个画圆点和实底，`-text` 的那个写字。
 * 把饱和色压在自己的淡色底上当文字是对比度掉得最狠的写法（亮色 3.5–4.4），
 * 而且软底和文字色写在同一个类名串里，不用猜它是文字还是图标就能判。
 *
 * `status-error` 与 `destructive` 同值、共用 `destructive-soft` 这一个软底，所以归一族。
 */
const SOFT_FAMILIES = [
  ["bg-status-running(?:-bg|\\/[0-9]+)", "text-status-running"],
  ["bg-status-waiting(?:-bg|\\/[0-9]+)", "text-status-waiting"],
  [
    "bg-(?:destructive-soft|destructive\\/[0-9]+|status-error\\/[0-9]+)",
    "text-(?:destructive|status-error)",
  ],
  ["bg-primary(?:-soft|\\/[0-9]+)", "text-primary"],
];

/** 类名的起点：串首、空白，或变体前缀的冒号。 */
const CLASS_START = "(?:^|[\\s:])";
/** 填充色类名到此为止：后面不能再接 `-text` / `-foreground` 这类角色后缀，`/50` 可以。 */
const FILL_END = "(?![\\w-])";

/**
 * 例：`bg-status-running-bg text-status-running`、`bg-destructive/10 text-destructive`
 *
 * 每一族是一对前瞻：同一个串里既有该族的软底、又有该族的填充色文字。
 * 用前瞻而不是「A.*B|B.*A」，是因为两者在类名串里的先后没有约定。
 */
const FILL_TEXT_ON_OWN_SOFT_BG = `^(?:${SOFT_FAMILIES.map(
  ([soft, fill]) =>
    `(?=[\\s\\S]*${CLASS_START}${soft})(?=[\\s\\S]*${CLASS_START}${fill}${FILL_END})`,
).join("|")})`;

/**
 * 例：`focus-visible:ring-ring/50`、`data-[state=open]:ring-ring/30`、`ring-2 ring-ring`
 *
 * `ring-ring` 这个组合只该出现在 tokens.css 里那两个焦点工具类的定义中。
 * 前面的边界同时认变体前缀的冒号；后面不许再接字母或连字符，`/50` 可以。
 */
const HAND_ROLLED_FOCUS_RING = `${CLASS_START}ring-ring${FILL_END}`;

/**
 * 例：`text-agent-1`、`hover:text-agent-12`
 *
 * 身份色是为「色块 + 白字」调的，当文字读不出来。`text-agent-foreground`
 * （色块上的字形）不在此列——后面必须是数字。
 */
const AGENT_COLOR_AS_TEXT = `${CLASS_START}text-agent-[0-9]+${FILL_END}`;

/**
 * 例：`hover:bg-muted`、`hover:bg-secondary/60`、`group-hover:bg-muted`
 *
 * 只认 muted 与 secondary 两个：它们各自与别的表面同值（暗色 muted ≡ card、
 * secondary ≡ popover，亮色 secondary ≡ sidebar）。`hover:bg-card` 不在此列——
 * 标签条在 secondary 带上向 card 提亮是有意的，与激活标签同一个方向。
 */
const RESTING_SURFACE_AS_HOVER = `${CLASS_START}(?:group-)?hover(?:\\/[a-z-]+)?:bg-(?:muted|secondary)${FILL_END}`;

/**
 * 结构线这一组只认**颜色**，不认宽度：状态色条（`border-l-2 border-l-status-running`）、
 * 拖放落点（`border-2 border-dashed border-ring`）、复选框（`border border-control-border`）
 * 用的宽度类与分隔线一模一样，区别只在颜色上。而分隔线色除了画线没有别的用途——
 * 宽度写在 cn() 的另一个字面量里时它照样是一条线，所以单独出现也拦。
 *
 * 单边写法也算：`border-b-border`、`border-l-sidebar-border`。
 */
const EDGE_SIDE = "(?:[tblrxyse]-)?";

/**
 * 例：`border-b border-border`、`border border-border/60`、`border-l border-sidebar-border`
 *
 * 分隔线色是 `border` 与 `border-*-border` 一族；`control-border`（复选框 / 单选 / 开关
 * 的边，控件本体）放行，`field-border` 归字段那一条报。`border-border-strong`
 * 后面还接着 `-strong`，是另一个 token（手写单选点的控件边），不在此列。
 */
const DIVIDER_COLOR_EDGE = `${CLASS_START}border-${EDGE_SIDE}(?:border|(?!control-|field-)[a-z]+-border)${FILL_END}`;

/**
 * 例：`divide-y divide-border`、`divide-x`、`[&>li]:divide-y`
 *
 * divide-* 只做一件事：在相邻子元素之间画线。前面的边界排除了
 * `task-pill-divide` 这类名字里恰好带 divide 的标识。
 */
const DIVIDE_UTILITY = `${CLASS_START}divide-[a-z0-9]`;

/**
 * 例：`border border-input`、`border-field-border`、`dark:border-input`
 *
 * 字段（输入框、文本域、下拉、搜索框）与 outline 按钮无边，靠相对前景色的填充：
 * `bg-field-fill` / `bg-button-fill`。后面不许再接字母或连字符：`--input-bg` 是
 * 另一个 token，`border-input-bg` 这种写法不归这条管。
 */
const FIELD_EDGE = `${CLASS_START}border-${EDGE_SIDE}(?:input|field-border)${FILL_END}`;

const TOKEN_HINT =
  "颜色必须走 design token：改用 bg-background / text-foreground / bg-sheet / bg-scrim" +
  " / text-agent-foreground 这类语义类名。token 定义在共享包" +
  " packages/agentre-ui/styles/tokens.css，工具类映射在同一文件的 @theme 块。" +
  " 需要新颜色时先加 token，不要就地写字面量——否则深色模式下它不会跟着变。";

const LADDER_HINT =
  "字号必须走阶梯：10→text-3xs、11→text-2xs、12→text-xs、13→text-aux、14→text-sm、15→text-prose。" +
  " 档位定义在共享包 packages/agentre-ui/styles/tokens.css 的 @theme 块，两端与包共用同一份。" +
  " 写字面像素会绕开阶梯，且行高只能靠继承——同一档在不同父容器下高矮不一。" +
  " 确实需要一档新尺寸时先加 token，不要就地写死。";

const TEXT_ROLE_HINT =
  "饱和填充色压在自己的软底上当文字读不出来（亮色 3.5–4.4）。软底上的同族文字改用 -text 角色：" +
  "text-status-running-text / text-status-waiting-text / text-destructive-text / text-primary-text。" +
  " 填充色留给圆点、实底和单独的图标。见 docs/design.md §3.5 与 §3.11。";

const FOCUS_HINT =
  "键盘焦点用共享包 packages/agentre-ui/styles/tokens.css 里定义的工具类：" +
  "按钮与字段都用 focus-visible:focus-outline（2px 实色环 + 2px 间隙；字段的包裹层用" +
  " focus-within:focus-outline，并把底色换回 bg-sheet）。focus-halo 仍是工具类，但无边的字段不再用它。" +
  " 半透明的 ring-ring/40、/50 合成后只有约 1.8–2.1:1。见 docs/design.md §10。";

const AGENT_COLOR_HINT =
  "身份色（--agent-1…16）是为「色块 + 白字」调的：暗色下当文字对 card 只有 3.5，" +
  "亮色落在 secondary 上也多数低于 4.5。文字用中性色（text-foreground / text-secondary-foreground）；" +
  "图标要带身份色时从 components/agentre/types.ts 的 agentIconColorClassNames 取类名，" +
  "不要就地写 text-agent-N。强调色用品牌色 text-primary-text。见 docs/design.md §3.6。";

const HOVER_HINT =
  "--muted 与 --secondary 是静止表面色，各自还与别的表面同值（暗色 muted ≡ card、" +
  "secondary ≡ popover，亮色 secondary ≡ sidebar），拿来当悬停反馈落在同值的面上就是 1.00。" +
  " 悬停一律用反馈色：hover:bg-accent；要轻一点用 hover:bg-accent/60 hover:on-accent；" +
  "窗口外壳带上用 hover:bg-rail-accent。见 docs/design.md §3.3 与 §9。";

const STRUCTURAL_LINE_HINT =
  "无边框改版之后，同一底色的两块区域之间不画线：外框与内容页靠表面色阶" +
  "（bg-chrome / bg-sheet / bg-raised，工作区用 content-sheet），面板头尾、列表行之间靠留白，" +
  "卡片、代码块、设置分组靠填充（bg-button-fill、bg-field-fill 一族）。" +
  " 允许出现的线只有焦点环、复选框 / 单选 / 开关的边（border-control-border）、状态色条与拖放落点。" +
  " 确属保留项而类名表认不出时，就地写 // eslint-disable-next-line no-restricted-syntax -- <理由>。" +
  " 见 docs/design.md §3.4 与 docs/specs/2026-10-15-borderless-ui.md「结构线」。";

const FIELD_EDGE_HINT =
  "字段（输入框、文本域、下拉、搜索框）与 outline 按钮都没有边：字段静止态 bg-field-fill、" +
  "悬停 hover:bg-field-fill-hover，聚焦时底色回到 bg-sheet 并只显示 focus-visible:focus-outline；" +
  "按钮 bg-button-fill + hover:bg-button-fill-hover。不要给 bg-*-fill 加 /透明度后缀——" +
  "那会丢掉填充面上的弱化文字换色。见 docs/design.md §3.4 与 §6。";

/**
 * 身份色当文字的那一组单独成表：身份色类名表所在的文件要豁免它，而只豁免它。
 */
const agentColorAsTextSyntax = [
  {
    selector: `Literal[value=/${AGENT_COLOR_AS_TEXT}/]`,
    message: `禁止把身份色当文字色写（如 text-agent-1）。${AGENT_COLOR_HINT}`,
  },
  {
    selector: `TemplateElement[value.raw=/${AGENT_COLOR_AS_TEXT}/]`,
    message: `禁止把身份色当文字色写（模板字符串里也不行）。${AGENT_COLOR_HINT}`,
  },
];

/**
 * no-restricted-syntax 的 selector 形式。
 * 走内建规则而不是自写插件：ESLint selector 已经能表达「字符串字面量匹配正则」。
 */
const restrictedSyntax = [
  {
    selector: `Literal[value=/${LITERAL_COLOR_CLASS}/]`,
    message: `禁止 Tailwind 调色板字面色类（如 bg-slate-900、text-white）。${TOKEN_HINT}`,
  },
  {
    selector: `TemplateElement[value.raw=/${LITERAL_COLOR_CLASS}/]`,
    message: `禁止 Tailwind 调色板字面色类（模板字符串里也不行）。${TOKEN_HINT}`,
  },
  {
    selector: `Literal[value=/${RAW_COLOR_VALUE}/]`,
    message: `禁止在 ts/tsx 里写死颜色值（#hex / rgb() / hsl()）。${TOKEN_HINT}`,
  },
  {
    selector: `TemplateElement[value.raw=/${RAW_COLOR_VALUE}/]`,
    message: `禁止在模板字符串里写死颜色值（#hex / rgb() / hsl()）。${TOKEN_HINT}`,
  },
  {
    selector: `Literal[value=/${ARBITRARY_FONT_SIZE}/]`,
    message: `禁止绕开字号阶梯的字面像素类（如 text-[13px]）。${LADDER_HINT}`,
  },
  {
    selector: `TemplateElement[value.raw=/${ARBITRARY_FONT_SIZE}/]`,
    message: `禁止绕开字号阶梯的字面像素类（模板字符串里也不行）。${LADDER_HINT}`,
  },
  {
    selector: `Literal[value=/${FILL_TEXT_ON_OWN_SOFT_BG}/]`,
    message: `禁止在软底上用同族的填充色当文字（如 bg-status-running-bg text-status-running）。${TEXT_ROLE_HINT}`,
  },
  {
    selector: `TemplateElement[value.raw=/${FILL_TEXT_ON_OWN_SOFT_BG}/]`,
    message: `禁止在软底上用同族的填充色当文字（模板字符串里也不行）。${TEXT_ROLE_HINT}`,
  },
  {
    selector: `Literal[value=/${HAND_ROLLED_FOCUS_RING}/]`,
    message: `禁止手拼焦点环（如 focus-visible:ring-[3px] focus-visible:ring-ring/50）。${FOCUS_HINT}`,
  },
  {
    selector: `TemplateElement[value.raw=/${HAND_ROLLED_FOCUS_RING}/]`,
    message: `禁止手拼焦点环（模板字符串里也不行）。${FOCUS_HINT}`,
  },
  ...agentColorAsTextSyntax,
  {
    selector: `Literal[value=/${RESTING_SURFACE_AS_HOVER}/]`,
    message: `禁止用静止表面色做悬停反馈（如 hover:bg-muted、hover:bg-secondary/60）。${HOVER_HINT}`,
  },
  {
    selector: `TemplateElement[value.raw=/${RESTING_SURFACE_AS_HOVER}/]`,
    message: `禁止用静止表面色做悬停反馈（模板字符串里也不行）。${HOVER_HINT}`,
  },
  {
    selector: `Literal[value=/${DIVIDER_COLOR_EDGE}/]`,
    message: `禁止用分隔线色画结构线（如 border-b border-border、border border-border）。${STRUCTURAL_LINE_HINT}`,
  },
  {
    selector: `TemplateElement[value.raw=/${DIVIDER_COLOR_EDGE}/]`,
    message: `禁止用分隔线色画结构线（模板字符串里也不行）。${STRUCTURAL_LINE_HINT}`,
  },
  {
    selector: `Literal[value=/${DIVIDE_UTILITY}/]`,
    message: `禁止用 divide-* 在列表行之间画线。${STRUCTURAL_LINE_HINT}`,
  },
  {
    selector: `TemplateElement[value.raw=/${DIVIDE_UTILITY}/]`,
    message: `禁止用 divide-* 在列表行之间画线（模板字符串里也不行）。${STRUCTURAL_LINE_HINT}`,
  },
  {
    selector: `Literal[value=/${FIELD_EDGE}/]`,
    message: `禁止给字段或 outline 按钮画边（如 border-input、border-field-border）。${FIELD_EDGE_HINT}`,
  },
  {
    selector: `TemplateElement[value.raw=/${FIELD_EDGE}/]`,
    message: `禁止给字段或 outline 按钮画边（模板字符串里也不行）。${FIELD_EDGE_HINT}`,
  },
];

export {
  AGENT_COLOR_AS_TEXT,
  ARBITRARY_FONT_SIZE,
  DIVIDE_UTILITY,
  DIVIDER_COLOR_EDGE,
  FIELD_EDGE,
  FILL_TEXT_ON_OWN_SOFT_BG,
  HAND_ROLLED_FOCUS_RING,
  LITERAL_COLOR_CLASS,
  PALETTE,
  RAW_COLOR_VALUE,
  RESTING_SURFACE_AS_HOVER,
  TOKENED_FONT_SIZES,
  agentColorAsTextSyntax,
  restrictedSyntax,
};
