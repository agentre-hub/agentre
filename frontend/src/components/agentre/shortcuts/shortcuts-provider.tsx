import React from "react";
import { useNavigate } from "react-router-dom";

import type { IndexAxis } from "@/lib/session-axis";
import { useSidebarAxisStore } from "@/stores/sidebar-axis-store";

import type { DesktopPlatform } from "../chrome";

import { findConflict, sameChord } from "./conflict";
import { chordFromEvent, isPrimaryModifier } from "./format";
import {
  CMD_NEW_CHAT_ID,
  NEW_CHAT_INITIAL_QUERY,
  PALETTE_OPEN_ID,
  SESSION_CHIP_IDS,
  TAB_CHIP_IDS,
  TAB_CLOSE_ID,
  getDef,
} from "./registry";
import {
  loadShortcutsState,
  saveShortcutsState,
  clearShortcutsState,
} from "./storage";
import type {
  AttentionEntry,
  KeyChord,
  PaletteScope,
  ShortcutDef,
} from "./types";

const NAV_PATHS: Record<string, string> = {
  "nav.chat": "/chat",
  // 「项目」合并进会话索引之后不再是一个页面，而是索引的一个分组维度：⌘D 落到
  // 索引，再由下面的 NAV_AXES 把轴切过去。
  "nav.projects": "/chat",
  "nav.issues": "/issues",
  "nav.org": "/org",
  "nav.hooks": "/hooks",
  "nav.settings": "/settings",
};

/** 落到索引后还要把分组维度切过去的导航动作。 */
const NAV_AXES: Record<string, IndexAxis> = {
  "nav.projects": "project",
};

// SessionScope —— 提供 ⌘1..9 历史侧边栏会话跳转的当前路由 scope。
// /chat 和 /projects 都注册到这个槽位，但路由互斥，所以同一时刻只有一个生效。
// Tab 化后 TabsScope 优先；SessionScope 仅作为回退保留，不再被主路径使用。
type SessionScope = {
  entries: AttentionEntry[];
  onSelect: (agentId: number, sessionId: number) => void;
};

// TabsScope —— ⌘1..9 切换 sortedTabs 中第 N 个 Tab，⌘W 关闭当前 Tab，
// Ctrl+Tab / Ctrl+Shift+Tab 循环切换到下一个 / 上一个 Tab（cycle）。
// 由 ChatTabsShortcuts 组件注册，挂在 ShortcutsProvider 里的 tabsScopeRef 上。
type TabsScope = {
  switchTo: (idx: number) => void;
  close: () => void;
  cycle: (delta: number) => void;
};

type ShortcutsContextValue = {
  platform: DesktopPlatform;
  bindings: Map<string, KeyChord>;
  sessionScopeRef: React.RefObject<SessionScope | null>;
  tabsScopeRef: React.RefObject<TabsScope | null>;
  paletteScopeRef: React.RefObject<PaletteScope | null>;
  setBinding: (actionId: string, chord: KeyChord) => void;
  resetAll: () => void;
  setSessionScope: (scope: SessionScope | null) => void;
  setTabsScope: (scope: TabsScope | null) => void;
  setPaletteScope: (scope: PaletteScope | null) => void;
  setPaused: (paused: boolean) => void;
  findChordConflict: (
    chord: KeyChord,
    excludeId: string,
  ) => ReturnType<typeof findConflict>;
};

const Ctx = React.createContext<ShortcutsContextValue | null>(null);

export function useShortcutsContext(): ShortcutsContextValue {
  const value = React.useContext(Ctx);
  if (!value) {
    throw new Error(
      "useShortcutsContext must be used inside <ShortcutsProvider>",
    );
  }
  return value;
}

export function useOptionalShortcutsContext(): ShortcutsContextValue | null {
  return React.useContext(Ctx);
}

type ProviderProps = {
  platform: DesktopPlatform;
  children: React.ReactNode;
};

function findIdForChord(
  bindings: Map<string, KeyChord>,
  chord: KeyChord,
): string | null {
  // Two-pass: prefer "tabs" scope (chat.tab.*) over "session" scope (chat.session.*)
  // when they share the same default chord (⌘1..9).
  let fallback: string | null = null;
  for (const [id, c] of bindings) {
    if (!sameChord(c, chord)) continue;
    const def = getDef(id);
    if (def?.scope === "tabs") return id;
    if (fallback === null) fallback = id;
  }
  return fallback;
}

export function ShortcutsProvider({
  platform,
  children,
}: ProviderProps): React.ReactElement {
  const initial = React.useMemo(() => loadShortcutsState(), []);
  const [bindings, setBindings] = React.useState<Map<string, KeyChord>>(
    initial.bindings,
  );

  const platformRef = React.useRef(platform);
  const bindingsRef = React.useRef(bindings);
  const sessionScopeRef = React.useRef<SessionScope | null>(null);
  const tabsScopeRef = React.useRef<TabsScope | null>(null);
  const paletteScopeRef = React.useRef<PaletteScope | null>(null);
  const pausedRef = React.useRef(false);

  React.useEffect(() => {
    platformRef.current = platform;
  }, [platform]);
  React.useEffect(() => {
    bindingsRef.current = bindings;
  }, [bindings]);

  const navigate = useNavigate();
  const navigateRef = React.useRef(navigate);
  React.useEffect(() => {
    navigateRef.current = navigate;
  }, [navigate]);

  const dispatchChord = React.useCallback(
    (chord: KeyChord): { matched: boolean; consumed: boolean } => {
      const id = findIdForChord(bindingsRef.current, chord);
      if (!id) return { matched: false, consumed: false };
      const def = getDef(id);
      if (!def) return { matched: false, consumed: false };

      // TabsScope: ⌘1..9 → switch tab by sorted index; ⌘W → close active tab.
      // TabsScope takes priority over SessionScope for the same chord.
      if (def.scope === "tabs") {
        const scope = tabsScopeRef.current;
        if (!scope) return { matched: true, consumed: true }; // consume but no-op
        if (id === TAB_CLOSE_ID) {
          scope.close();
          return { matched: true, consumed: true };
        }
        const tabIdx = TAB_CHIP_IDS.indexOf(id);
        if (tabIdx >= 0) {
          scope.switchTo(tabIdx);
        }
        return { matched: true, consumed: true };
      }

      if (def.scope === "session") {
        const scope = sessionScopeRef.current;
        if (!scope) return { matched: false, consumed: false };
        const idx = SESSION_CHIP_IDS.indexOf(id);
        const entry = idx >= 0 ? scope.entries[idx] : undefined;
        if (!entry) return { matched: true, consumed: true };
        scope.onSelect(entry.agentId, entry.sessionId);
        return { matched: true, consumed: true };
      }

      if (id === PALETTE_OPEN_ID) {
        const scope = paletteScopeRef.current;
        if (!scope) return { matched: false, consumed: false };
        scope.toggle();
        return { matched: true, consumed: true };
      }

      if (id === CMD_NEW_CHAT_ID) {
        const scope = paletteScopeRef.current;
        if (!scope) return { matched: false, consumed: false };
        scope.openWith(NEW_CHAT_INITIAL_QUERY);
        return { matched: true, consumed: true };
      }

      const axis = NAV_AXES[id];
      if (axis) useSidebarAxisStore.getState().setAxis(axis);
      const path = NAV_PATHS[id];
      if (path) navigateRef.current(path);
      return { matched: true, consumed: true };
    },
    [],
  );

  React.useEffect(() => {
    function handleKeyDown(event: KeyboardEvent) {
      if (pausedRef.current) return;
      // IME 合成期间（中文 / 日文输入）不参与任何 chord 派发：浏览器在选词过程中
      // 也会触发 keydown，event.isComposing=true 表明 keystroke 属于输入法，
      // 不能当作 ⌘P / ⌘1-9 / nav.* 来用。
      if (event.isComposing) return;

      // Ctrl+Tab / Ctrl+Shift+Tab —— 浏览器式循环切换到下一个 / 上一个 Tab。
      // 它不是可重绑的 KeyChord：Ctrl 在 macOS 不是主修饰键，"Tab" 也不在
      // ACCEPTABLE_KEY 集里，所以在这里作为固定 chord 直接派发给 tabsScope.cycle。
      // 要求 ctrlKey && !metaKey，从而放行 macOS 的 ⌘Tab（系统 App 切换）。
      if (
        event.key === "Tab" &&
        event.ctrlKey &&
        !event.altKey &&
        !event.metaKey
      ) {
        tabsScopeRef.current?.cycle(event.shiftKey ? -1 : 1);
        event.preventDefault();
        return;
      }

      const p = platformRef.current;
      if (!isPrimaryModifier(event, p)) return;

      const chord = chordFromEvent(event, p);
      if (!chord) return;

      const { matched, consumed } = dispatchChord(chord);
      if (matched && consumed) event.preventDefault();
    }

    window.addEventListener("keydown", handleKeyDown);
    return () => {
      window.removeEventListener("keydown", handleKeyDown);
    };
  }, [dispatchChord]);

  const setBinding = React.useCallback((actionId: string, chord: KeyChord) => {
    setBindings((prev) => {
      const next = new Map(prev);
      next.set(actionId, chord);
      saveShortcutsState({ bindings: next });
      return next;
    });
  }, []);

  const resetAll = React.useCallback(() => {
    clearShortcutsState();
    const fresh = loadShortcutsState();
    setBindings(fresh.bindings);
  }, []);

  const setSessionScope = React.useCallback((scope: SessionScope | null) => {
    sessionScopeRef.current = scope;
  }, []);

  const setTabsScope = React.useCallback((scope: TabsScope | null) => {
    tabsScopeRef.current = scope;
  }, []);

  const setPaletteScope = React.useCallback((scope: PaletteScope | null) => {
    paletteScopeRef.current = scope;
  }, []);

  const setPaused = React.useCallback((paused: boolean) => {
    pausedRef.current = paused;
  }, []);

  const findChordConflict = React.useCallback(
    (chord: KeyChord, excludeId: string) =>
      findConflict(bindingsRef.current, chord, excludeId),
    [],
  );

  const value = React.useMemo<ShortcutsContextValue>(
    () => ({
      platform,
      bindings,
      sessionScopeRef,
      tabsScopeRef,
      paletteScopeRef,
      setBinding,
      resetAll,
      setSessionScope,
      setTabsScope,
      setPaletteScope,
      setPaused,
      findChordConflict,
    }),
    [
      platform,
      bindings,
      setBinding,
      resetAll,
      setSessionScope,
      setTabsScope,
      setPaletteScope,
      setPaused,
      findChordConflict,
    ],
  );

  return <Ctx.Provider value={value}>{children}</Ctx.Provider>;
}

export type { ShortcutDef };
