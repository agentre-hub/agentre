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

// AvatarIdentity 是 chat-agents-store 里那份完整身份（useAgentIdentity 的产物）。
// 会话 meta 没有图标 / 上传图片这两项，所以有 agent 身份时以它为准；没有时回落 meta。
export type AvatarIdentity =
  | {
      name?: string | null;
      color?: string | null;
      avatarIcon?: string | null;
      avatarDataUrl?: string | null;
    }
  | null
  | undefined;

// avatarFromMeta 从 session meta（并可用 agent 身份覆盖）推导头像；
// meta 与身份都缺失时回落灰底问号。
export function avatarFromMeta(
  meta: AvatarMeta,
  identity?: AvatarIdentity,
): {
  letter: string;
  color: string;
  avatarIcon: string;
  avatarDataUrl: string;
} {
  const name = identity?.name || meta?.agentName;
  const colorToken = identity?.color || meta?.agentColor;
  return {
    letter: firstLetter(name),
    // meta 缺失时的兜底底色。同 types.ts 里的 `neutral`：共享包的身份色板没有
    // 中性那一档，补 --agent-neutral 之前只能写字面色。两处的值目前还不一样
    // （这里偏浅），补 token 时应当一并统一。
    // eslint-disable-next-line no-restricted-syntax
    color: tokenToCssColor(colorToken) ?? "#94a3b8",
    avatarIcon: identity?.avatarIcon ?? "",
    avatarDataUrl: identity?.avatarDataUrl ?? "",
  };
}
