// session-avatar 把 session 的 agent 元信息（颜色 token + 名字）推导成头像所需的
// { letter, color }，供 tab 条与通知 toast 等处复用，避免各自再实现一遍。
// token → css 变量的映射本身归共享包（词汇表与 tokens.css 同源）。
import { tokenToCssColor } from "@agentre-hub/agentre-ui";

// firstLetter 取名字首字符作头像字母；空名回落 "?"。
export function firstLetter(name: string | null | undefined): string {
  if (!name) return "?";
  const trimmed = name.trim();
  if (!trimmed) return "?";
  return Array.from(trimmed)[0] ?? "?";
}

export type AvatarMeta =
  | { agentColor?: string | null; agentName?: string | null }
  | null
  | undefined;

// avatarFromMeta 从 session meta 推导头像；meta 缺失时回落灰底问号。
export function avatarFromMeta(meta: AvatarMeta): {
  letter: string;
  color: string;
} {
  return {
    letter: firstLetter(meta?.agentName),
    // meta 缺失时的兜底底色。同 types.ts 里的 `neutral`：共享包的身份色板没有
    // 中性那一档，补 --agent-neutral 之前只能写字面色。两处的值目前还不一样
    // （这里偏浅），补 token 时应当一并统一。
    // eslint-disable-next-line no-restricted-syntax
    color: tokenToCssColor(meta?.agentColor) ?? "#94a3b8",
  };
}
