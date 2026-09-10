// agent 调色板的 **token 词汇表**：16 个 agent 色 + 一个中性色。
//
// 为什么在包里：转录要按 token 上色（@提及 chip 的 --mention-color），而 token
// 名与 tokens.css 里的 CSS 变量是同一套东西——它已经随 design tokens 进了本包。
// 把词汇表留在宿主、CSS 变量放在包里，等于让两处各自演化：宿主加第 17 个 agent 色
// 而 tokens.css 没跟上时，chip 会拿到一个解析不出来的 var()，静默变成默认色。
//
// 头像**怎么画**不在这里：那是身份模型（宿主的 icon-registry / 自定义头像图片 /
// agentre-server 另一套），走 prop 注入（见 message-row.tsx 的 avatar）。
export type AgentColor =
  | "agent-1"
  | "agent-2"
  | "agent-3"
  | "agent-4"
  | "agent-5"
  | "agent-6"
  | "agent-7"
  | "agent-8"
  | "agent-9"
  | "agent-10"
  | "agent-11"
  | "agent-12"
  | "agent-13"
  | "agent-14"
  | "agent-15"
  | "agent-16"
  | "neutral";

// 调色板的展示顺序（宿主的颜色选择器按它排格子），同时充当合法 token 的全集。
export const agentColorOrder: AgentColor[] = [
  "agent-1",
  "agent-2",
  "agent-3",
  "agent-4",
  "agent-5",
  "agent-6",
  "agent-7",
  "agent-8",
  "agent-9",
  "agent-10",
  "agent-11",
  "agent-12",
  "agent-13",
  "agent-14",
  "agent-15",
  "agent-16",
];

const AGENT_COLOR_SET = new Set<string>(agentColorOrder);

// tokenToCssColor 把 agent / project 颜色 token 映射成 css 变量；非法 token → null。
// 先校验再拼串：直接 `var(--${token})` 会把任何脏字符串变成一个语法上合法、
// 解析结果为空的 var()，调用方分辨不出「没颜色」和「颜色坏了」。
export function tokenToCssColor(
  token: string | null | undefined,
): string | null {
  if (!token) return null;
  if (!AGENT_COLOR_SET.has(token)) return null;
  return `var(--${token})`;
}
