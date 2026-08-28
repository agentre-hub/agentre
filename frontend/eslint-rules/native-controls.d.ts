/**
 * `native-controls.js` 的类型面（理由与 design-tokens.d.ts 同一条：规则数据必须是
 * `.js`，flat config 不经 TS 编译，于是它在 TS 那侧是隐式 any）。
 */

/** `<input type="…">` 里有对应原语、因此被禁的那几种。 */
export const REPLACEABLE_INPUT_TYPES: string[];

/** 附在每条消息后面的那句「该换成什么」。 */
export const HINT: string;

/** 供 `no-restricted-syntax` 直接展开的规则项。 */
export const nativeControlSyntax: { selector: string; message: string }[];
