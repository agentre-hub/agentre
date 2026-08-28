import React from "react";
import { act, render, screen } from "@testing-library/react";
import { MemoryRouter, Route, Routes, useLocation } from "react-router-dom";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { SIDEBAR_AXIS_KEY } from "@/lib/sidebar-axis-state";
import { useSidebarAxisStore } from "@/stores/sidebar-axis-store";

import type { DesktopPlatform } from "../../chrome";

import {
  ShortcutsProvider,
  useShortcutsContext,
} from "../../shortcuts/shortcuts-provider";
import type { AttentionEntry } from "../../shortcuts/types";

function LocationProbe() {
  const loc = useLocation();
  return <span data-testid="loc">{loc.pathname}</span>;
}

function ChatScopeBinder(props: {
  entries: AttentionEntry[];
  onSelect: (a: number, s: number) => void;
}) {
  const { setSessionScope } = useShortcutsContext();
  React.useEffect(() => {
    setSessionScope({ entries: props.entries, onSelect: props.onSelect });
    return () => setSessionScope(null);
  }, [props.entries, props.onSelect, setSessionScope]);
  return null;
}

function PaletteScopeBinder(props: {
  toggle: () => void;
  openWith?: (q: string) => void;
  enabled: boolean;
}) {
  const { setPaletteScope } = useShortcutsContext();
  React.useEffect(() => {
    if (!props.enabled) return;
    const openWith = props.openWith ?? (() => {});
    setPaletteScope({ toggle: props.toggle, openWith });
    return () => setPaletteScope(null);
  }, [props.toggle, props.openWith, props.enabled, setPaletteScope]);
  return null;
}

function renderHarness(opts: {
  platform?: DesktopPlatform;
  chatEntries?: AttentionEntry[];
  onSelect?: (a: number, s: number) => void;
  initialPath?: string;
  paletteToggle?: () => void;
  paletteOpenWith?: (q: string) => void;
  paletteScopeMounted?: boolean;
}) {
  const onSelect = opts.onSelect ?? vi.fn();
  const paletteToggle = opts.paletteToggle ?? vi.fn();
  const paletteOpenWith = opts.paletteOpenWith ?? vi.fn();
  return {
    onSelect,
    paletteToggle,
    paletteOpenWith,
    ...render(
      <MemoryRouter initialEntries={[opts.initialPath ?? "/chat"]}>
        <Routes>
          <Route
            path="*"
            element={
              <ShortcutsProvider platform={opts.platform ?? "darwin"}>
                <LocationProbe />
                {opts.chatEntries ? (
                  <ChatScopeBinder
                    entries={opts.chatEntries}
                    onSelect={onSelect}
                  />
                ) : null}
                {opts.paletteScopeMounted ? (
                  <PaletteScopeBinder
                    toggle={paletteToggle}
                    openWith={paletteOpenWith}
                    enabled={opts.paletteScopeMounted}
                  />
                ) : null}
                <input data-testid="input" />
              </ShortcutsProvider>
            }
          />
        </Routes>
      </MemoryRouter>,
    ),
  };
}

function press(key: string, init: KeyboardEventInit = {}) {
  act(() => {
    window.dispatchEvent(new KeyboardEvent("keydown", { key, ...init }));
  });
}
function release(key: string, init: KeyboardEventInit = {}) {
  act(() => {
    window.dispatchEvent(new KeyboardEvent("keyup", { key, ...init }));
  });
}

beforeEach(() => {
  localStorage.clear();
});

afterEach(() => {
  localStorage.clear();
});

describe("ShortcutsProvider chord dispatch — darwin", () => {
  it("dispatches ⌘E to chat", () => {
    renderHarness({ platform: "darwin", initialPath: "/projects" });
    press("e", { metaKey: true });
    expect(screen.getByTestId("loc").textContent).toBe("/chat");
    release("Meta");
  });

  it("Given the index is grouped by time, When ⌘D is pressed, Then it lands on the index and switches to the project axis", () => {
    // 合并之后「项目」不再是一个页面，而是索引的一个分组维度。⌘D 跟着改口径：
    // 落到索引并切到项目轴（并持久化，跟点 AxisPicker 一样）。
    useSidebarAxisStore.getState().setAxis("time");
    renderHarness({ platform: "darwin", initialPath: "/settings" });

    press("d", { metaKey: true });

    expect(screen.getByTestId("loc").textContent).toBe("/chat");
    expect(useSidebarAxisStore.getState().axis).toBe("project");
    expect(localStorage.getItem(SIDEBAR_AXIS_KEY)).toBe("project");
    release("Meta");
  });

  it("dispatches ⌘B to issues", () => {
    renderHarness({ platform: "darwin", initialPath: "/chat" });
    press("b", { metaKey: true });
    expect(screen.getByTestId("loc").textContent).toBe("/issues");
    release("Meta");
  });

  it("ignores chords belonging to the chat scope when no scope is mounted", () => {
    renderHarness({ platform: "darwin", initialPath: "/projects" });
    press("1", { metaKey: true });
    // No scope → not consumed, no navigation side-effect; remain on /projects.
    expect(screen.getByTestId("loc").textContent).toBe("/projects");
    release("Meta");
  });

  // NOTE: ⌘1..9 now dispatches to TabsScope (chat.tab.N) which takes priority
  // over the historical SessionScope (chat.session.N). When no TabsScope is mounted,
  // the chord is consumed (no-op) without falling back to SessionScope.
  it("⌘1 is consumed (no-op) when TabsScope is not mounted — chat.tab.1 takes priority over chat.session.1", () => {
    const onSelect = vi.fn();
    renderHarness({
      platform: "darwin",
      initialPath: "/chat",
      chatEntries: [
        { agentId: 7, sessionId: 42 },
        { agentId: 7, sessionId: 43 },
      ],
      onSelect,
    });
    press("1", { metaKey: true });
    // No TabsScope mounted → chord is consumed but session scope is NOT called.
    expect(onSelect).not.toHaveBeenCalled();
    release("Meta");
  });
});

describe("ShortcutsProvider palette dispatch", () => {
  it("⌘P calls paletteScope.toggle when scope is mounted", () => {
    const toggle = vi.fn();
    renderHarness({
      platform: "darwin",
      paletteToggle: toggle,
      paletteScopeMounted: true,
    });
    press("p", { metaKey: true });
    expect(toggle).toHaveBeenCalledTimes(1);
    release("Meta");
  });

  it("ignores Ctrl+P on darwin", () => {
    const toggle = vi.fn();
    renderHarness({
      platform: "darwin",
      paletteToggle: toggle,
      paletteScopeMounted: true,
    });
    press("p", { ctrlKey: true });
    expect(toggle).not.toHaveBeenCalled();
    release("Control");
  });

  it("⌘P is a no-op when no palette scope is mounted", () => {
    const toggle = vi.fn();
    renderHarness({
      platform: "darwin",
      paletteToggle: toggle,
      paletteScopeMounted: false,
    });
    press("p", { metaKey: true });
    expect(toggle).not.toHaveBeenCalled();
    release("Meta");
  });

  it("ignores chords while IME composition is active", () => {
    const toggle = vi.fn();
    renderHarness({
      platform: "darwin",
      initialPath: "/chat",
      paletteToggle: toggle,
      paletteScopeMounted: true,
    });
    // 模拟 IME 合成中按下 ⌘P:isComposing=true 时 handleKeyDown 应立刻 return
    act(() => {
      window.dispatchEvent(
        new KeyboardEvent("keydown", {
          key: "p",
          metaKey: true,
          isComposing: true,
        }),
      );
    });
    expect(toggle).not.toHaveBeenCalled();
  });
});

describe("ShortcutsProvider cmd.new-chat dispatch", () => {
  it("⌘N calls paletteScope.openWith with the seed query", () => {
    const openWith = vi.fn();
    renderHarness({
      platform: "darwin",
      paletteOpenWith: openWith,
      paletteScopeMounted: true,
    });
    press("n", { metaKey: true });
    expect(openWith).toHaveBeenCalledTimes(1);
    expect(openWith).toHaveBeenCalledWith("> ");
    release("Meta");
  });

  it("⌘N is a no-op when palette scope is not mounted", () => {
    const openWith = vi.fn();
    renderHarness({
      platform: "darwin",
      paletteOpenWith: openWith,
      paletteScopeMounted: false,
    });
    press("n", { metaKey: true });
    expect(openWith).not.toHaveBeenCalled();
    release("Meta");
  });

  it("Ctrl+N on linux dispatches the same way", () => {
    const openWith = vi.fn();
    renderHarness({
      platform: "linux",
      paletteOpenWith: openWith,
      paletteScopeMounted: true,
    });
    press("n", { ctrlKey: true });
    expect(openWith).toHaveBeenCalledWith("> ");
    release("Control");
  });
});

describe("ShortcutsProvider chord dispatch — linux uses Ctrl", () => {
  it("ignores Meta-modified chords on linux", () => {
    renderHarness({ platform: "linux", initialPath: "/chat" });

    press("e", { metaKey: true });
    expect(screen.getByTestId("loc").textContent).toBe("/chat");
    release("Meta");
  });

  it("dispatches Ctrl+D to the project axis on linux", () => {
    useSidebarAxisStore.getState().setAxis("agent");
    renderHarness({ platform: "linux", initialPath: "/settings" });

    press("d", { ctrlKey: true });

    expect(screen.getByTestId("loc").textContent).toBe("/chat");
    expect(useSidebarAxisStore.getState().axis).toBe("project");
    release("Control");
  });
});
