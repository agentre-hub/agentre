import type * as monacoApi from "monaco-editor/editor/editor.api";

import type { MonacoNS } from "./monaco";

type MonarchLanguage = monacoApi.languages.IMonarchLanguage;

// JSON / JSONC 的轻量 Monarch tokenizer。
//
// monaco-editor 0.56 起 JSON 移出了 basic-languages（basic-languages/
// monaco.contribution 只注册纯词法语言，不含 json；完整 JSON 语言服务
// vs/language/json 附带 worker + 校验器，体积大，与本项目「只读预览不引
// language service」的体积决策冲突）。因此 json 不在加载的语言集里，monaco
// 语言表把 .json/.jsonc 映射到 json 也只是映射到一个未注册的语言——预览按
// 纯文本渲染，没有高亮（spec 设计决策 7「扩展名→语言识别」在 json 上落空）。
//
// 这里注册一个与 basic-languages 同级的 JSON 语言：只做 Monarch 词法着色，
// 无 IntelliSense / worker。
//
// 规则面按「合法 JSON 全覆盖」构造：propertyName（键，string.key.json）在
// strings 之前判定，未引号字面量 true/false/null 有专门规则，所以合法 JSON 不
// 会落到 defaultToken('invalid') 露红；defaultToken 只对语法残缺的输入生效。
export function registerJsonLanguage(monaco: MonacoNS): void {
  monaco.languages.register({
    id: "json",
    extensions: [".json", ".jsonc"],
    aliases: ["JSON", "JSONC"],
  });
  monaco.languages.setLanguageConfiguration("json", {
    comments: {
      lineComment: "//",
      blockComment: ["/*", "*/"],
    },
    brackets: [
      ["{", "}"],
      ["[", "]"],
    ],
    autoClosingPairs: [
      { open: "{", close: "}" },
      { open: "[", close: "]" },
      { open: '"', close: '"' },
    ],
  });
  monaco.languages.setMonarchTokensProvider("json", jsonTokenProvider);
}

// 只读预览不需要对 JSON 正文做任何写操作，tokenizer 为纯函数式 Monarch 定义，
// 独立导出便于单测直接断言结构。显式标注 IMonarchLanguage 让规则数组按元组
// 上下文定型（否则 [regex, token] 会被推断成宽联合数组，过不了 tsc 的元组校验）。
export const jsonTokenProvider: MonarchLanguage = {
  defaultToken: "invalid",
  tokenPostfix: ".json",
  tokenizer: {
    root: [
      { include: "@whitespace" },
      { include: "@numbers" },
      // 键先于字符串判定："a": 命中 string.key.json；作为值的 "a" 落到
      // @strings 的 string。
      { include: "@propertyName" },
      { include: "@strings" },
      [/[{},:]/, "delimiter"],
      [/[[\]]/, "delimiter.bracket"],
      // 未引号的 JSON 字面量，合法 JSON 全覆盖的关键。
      [/\b(?:true|false|null)\b/, "keyword"],
    ],
    whitespace: [
      [/[ \t\r\n]+/, ""],
      [/\/\*/, "comment", "@comment"],
      [/\/\/.*$/, "comment"],
    ],
    comment: [
      [/[^/*]+/, "comment"],
      [/\*\//, "comment", "@pop"],
      [/[/*]/, "comment"],
    ],
    propertyName: [[/"(?:[^"\\]|\\.)*"(?=\s*:)/, "string.key.json"]],
    numbers: [
      [/-?0[xX][0-9a-fA-F]+/, "number.hex"],
      [/-?\d*\.\d+([eE][-+]?\d+)?/, "number.float"],
      [/-?\d+([eE][-+]?\d+)?/, "number"],
    ],
    strings: [[/"(?:[^"\\]|\\.)*"/, "string"]],
  },
};
