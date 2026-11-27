import { getDef } from "./registry";
import type { KeyChord } from "./types";

let published: ReadonlyMap<string, KeyChord> | null = null;

/**
 * ShortcutsProvider 把当前生效的绑定发布到这里，给不在 React 树里读按键的地方用
 * （全局派发、终端放行判断）。返回撤销函数：Provider 卸载后回到默认绑定。
 */
export function publishBindings(
  bindings: ReadonlyMap<string, KeyChord>,
): () => void {
  published = bindings;
  return () => {
    if (published === bindings) published = null;
  };
}

/**
 * 某个动作此刻生效的和弦：可改绑的取用户绑定（没有就是默认）；固定的永远是默认，
 * 存储里为它们留下的记录不生效。
 */
export function effectiveChord(
  id: string,
  bindings: ReadonlyMap<string, KeyChord> | null = published,
): KeyChord | undefined {
  const def = getDef(id);
  if (!def) return undefined;
  if (!def.rebindable) return def.defaultBinding;
  return bindings?.get(id) ?? def.defaultBinding;
}
