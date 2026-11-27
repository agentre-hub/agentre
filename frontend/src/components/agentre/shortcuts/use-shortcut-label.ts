import { detectBrowserPlatform } from "@/lib/platform";

import { effectiveChord } from "./effective-bindings";
import { formatChord } from "./format";
import { useOptionalShortcutsContext } from "./shortcuts-provider";

/**
 * 某个快捷键当前的按键文案（悬停提示用），随用户改绑更新。不在 ShortcutsProvider
 * 里时按默认绑定和浏览器探测到的平台给出。
 */
export function useShortcutLabel(id: string): string {
  const ctx = useOptionalShortcutsContext();
  const chord = effectiveChord(id, ctx?.bindings ?? null);
  return chord
    ? formatChord(chord, ctx?.platform ?? detectBrowserPlatform())
    : "";
}
