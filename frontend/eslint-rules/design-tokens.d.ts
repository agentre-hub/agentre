/**
 * `design-tokens.js` 的类型面。
 *
 * 规则数据本身必须是 `.js`：eslint.config.js 直接 import 它，而 ESLint 的 flat
 * config 不经 TS 编译，换成 .ts 就要给 lint 这条链另装一个加载器。
 * 于是它在 TS 那侧是隐式 any——守卫测试一 import 就 TS7016。
 *
 * 声明写在这里而不是给测试加 `as` 断言：断言只是把类型糊过去，改了导出名也不会
 * 有人红；这份声明是契约，改名会当场报错。
 */

/** Tailwind 自带调色板的色名，形如 "red|orange|…"。 */
export const PALETTE: string;

/** 匹配「调色板字面色类」的正则源码。 */
export const LITERAL_COLOR_CLASS: string;

/** 匹配「写死的颜色值」（#hex / rgb() / hsl()）的正则源码。 */
export const RAW_COLOR_VALUE: string;

/** 阶梯上已有 token 的那几档像素值，形如 "10|11|12|13|14|15"。 */
export const TOKENED_FONT_SIZES: string;

/** 匹配「绕开阶梯的字面像素字号类」的正则源码。 */
export const ARBITRARY_FONT_SIZE: string;

/** 匹配「同一个类名串里软底配同族填充色文字」的正则源码。 */
export const FILL_TEXT_ON_OWN_SOFT_BG: string;

/** 匹配「手拼的焦点环」（任意变体下的 `ring-ring` / `ring-ring/NN`）的正则源码。 */
export const HAND_ROLLED_FOCUS_RING: string;

/** 匹配「把身份色当文字色写」（`text-agent-N`）的正则源码。 */
export const AGENT_COLOR_AS_TEXT: string;

/** 身份色当文字的那一组规则项；`restrictedSyntax` 已经包含它，单独导出是为了按文件豁免。 */
export const agentColorAsTextSyntax: { selector: string; message: string }[];

/** 匹配「用静止表面色做悬停反馈」（`hover:bg-muted` / `hover:bg-secondary`）的正则源码。 */
export const RESTING_SURFACE_AS_HOVER: string;

/** 匹配「用分隔线色画结构线」（`border-border` / `border-*-border`，`control-border` 除外）的正则源码。 */
export const DIVIDER_COLOR_EDGE: string;

/** 匹配「用 `divide-*` 在相邻子元素之间画线」的正则源码。 */
export const DIVIDE_UTILITY: string;

/** 匹配「字段或 outline 按钮的边」（`border-input` / `border-field-border`）的正则源码。 */
export const FIELD_EDGE: string;

/** 供 `no-restricted-syntax` 直接展开的规则项。 */
export const restrictedSyntax: { selector: string; message: string }[];
