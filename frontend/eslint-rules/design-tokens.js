/**
 * 设计 token 守卫的规则数据。
 *
 * 单独抽成模块，是为了让 eslint.config.js 和守卫测试
 * （src/__tests__/eslint-design-tokens.test.ts）引用同一份来源——
 * 否则守卫测试可能在测一份和实际生效的配置不一样的正则。
 * 形态照搬 agentre-server 的同名模块，两仓的这条规则应当保持一致。
 *
 * 本轮只落**字号**一条。颜色字面量守卫本仓还没有（这正是 Dialog 遮罩里那句
 * bg-slate-900/25 能活到今天的原因），补它会当场翻出一批与本轮无关的违规，
 * 属于另一轮。
 */

/** 任意变体前缀，如 sm: / dark: / hover: / group-hover: */
const VARIANT = "(?:[a-z-]+:)*";

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

const LADDER_HINT =
  "字号必须走阶梯：10→text-3xs、11→text-2xs、12→text-xs、13→text-aux、14→text-sm、15→text-prose。" +
  " 档位定义在共享包 packages/agentre-ui/styles/tokens.css 的 @theme 块，两端与包共用同一份。" +
  " 写字面像素会绕开阶梯，且行高只能靠继承——同一档在不同父容器下高矮不一。" +
  " 确实需要一档新尺寸时先加 token，不要就地写死。";

/**
 * no-restricted-syntax 的 selector 形式。
 * 走内建规则而不是自写插件：ESLint selector 已经能表达「字符串字面量匹配正则」。
 */
const restrictedSyntax = [
  {
    selector: `Literal[value=/${ARBITRARY_FONT_SIZE}/]`,
    message: `禁止绕开字号阶梯的字面像素类（如 text-[13px]）。${LADDER_HINT}`,
  },
  {
    selector: `TemplateElement[value.raw=/${ARBITRARY_FONT_SIZE}/]`,
    message: `禁止绕开字号阶梯的字面像素类（模板字符串里也不行）。${LADDER_HINT}`,
  },
];

export { ARBITRARY_FONT_SIZE, TOKENED_FONT_SIZES, restrictedSyntax };
