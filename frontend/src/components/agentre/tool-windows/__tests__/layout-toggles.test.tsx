import { act, render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter } from "react-router-dom";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { TooltipProvider } from "@agentre-hub/agentre-ui";
import {
  DEFAULT_TOOL_WINDOW_SLOTS,
  useToolWindowLayout,
} from "@/stores/tool-window-layout-store";

import { AppTopBar } from "../../chrome";
import {
  ShortcutsProvider,
  useShortcutsContext,
} from "../../shortcuts/shortcuts-provider";
import { LayoutToggles } from "../layout-toggles";

const initial = useToolWindowLayout.getState();

function renderToggles() {
  return render(
    <MemoryRouter>
      <ShortcutsProvider platform="darwin">
        <TooltipProvider>
          <LayoutToggles />
        </TooltipProvider>
      </ShortcutsProvider>
    </MemoryRouter>,
  );
}

const sessionBtn = () => screen.getByTestId("layout-toggle-sessionList");
const bottomBtn = () => screen.getByTestId("layout-toggle-bottom");
const rightBtn = () => screen.getByTestId("layout-toggle-right");

beforeEach(() => {
  useToolWindowLayout.setState({ ...initial, sessionListCollapsed: false });
});
afterEach(() => {
  useToolWindowLayout.setState(initial, true);
});

describe("LayoutToggles", () => {
  it("renders three buttons in order session list, bottom, right", () => {
    renderToggles();
    const all = screen.getAllByTestId(/^layout-toggle-/);
    expect(all.map((b) => b.getAttribute("data-testid"))).toEqual([
      "layout-toggle-sessionList",
      "layout-toggle-bottom",
      "layout-toggle-right",
    ]);
  });

  it("session list button reflects pressed state and equals the toggleSessionList action", async () => {
    const user = userEvent.setup();
    renderToggles();
    expect(sessionBtn()).toHaveAttribute("aria-pressed", "true");
    expect(sessionBtn()).toHaveAccessibleName("Collapse session list");
    expect(sessionBtn().querySelector("[data-filled]")).not.toBeNull();

    await user.click(sessionBtn());
    expect(useToolWindowLayout.getState().sessionListCollapsed).toBe(true);
    expect(sessionBtn()).toHaveAttribute("aria-pressed", "false");
    expect(sessionBtn()).toHaveAccessibleName("Expand session list");
    expect(sessionBtn().querySelector("[data-filled]")).toBeNull();
  });

  it("shows live shortcut in the tooltip", async () => {
    const user = userEvent.setup();
    renderToggles();
    await user.hover(sessionBtn());
    const tip = await screen.findByRole("tooltip");
    expect(tip).toHaveTextContent("Collapse session list");
    expect(tip).toHaveTextContent("⌘0");
  });

  it("bottom and right buttons follow region state and call toggleRegion", async () => {
    const user = userEvent.setup();
    const toggleRegion = vi.spyOn(
      useToolWindowLayout.getState(),
      "toggleRegion",
    );
    useToolWindowLayout.setState({ toggleRegion });
    renderToggles();
    const initialBottom = bottomBtn().getAttribute("aria-pressed");
    expect(bottomBtn()).toBeEnabled();
    expect(rightBtn()).toBeEnabled();

    await user.click(bottomBtn());
    expect(toggleRegion).toHaveBeenLastCalledWith("bottom");
    await user.click(rightBtn());
    expect(toggleRegion).toHaveBeenLastCalledWith("right");
    expect(initialBottom).toMatch(/true|false/);
  });

  it("when a region is shown it is pressed with a filled icon and 'Hide' wording", () => {
    renderToggles();
    const r = useToolWindowLayout.getState();
    expect(r.slotOf).toBeDefined();
    const pressed = rightBtn().getAttribute("aria-pressed") === "true";
    expect(rightBtn()).toHaveAccessibleName(
      pressed ? "Hide right tool windows" : "Show right tool windows",
    );
    expect(rightBtn().querySelector("[data-filled]") !== null).toBe(pressed);
  });

  it("when a region has no windows it is disabled and the tooltip says so", async () => {
    const user = userEvent.setup();
    const slotOf = { ...useToolWindowLayout.getState().slotOf };
    for (const id of Object.keys(slotOf) as (keyof typeof slotOf)[]) {
      if (slotOf[id].startsWith("right")) slotOf[id] = "bottomLeft";
    }
    act(() => useToolWindowLayout.setState({ slotOf }));
    renderToggles();
    expect(rightBtn()).toBeDisabled();
    expect(rightBtn()).toHaveAttribute("aria-pressed", "false");
    await user.hover(rightBtn().parentElement as HTMLElement);
    expect(await screen.findByRole("tooltip")).toHaveTextContent(
      "The right area has no tool windows",
    );
    expect(DEFAULT_TOOL_WINDOW_SLOTS).toBeDefined();
  });

  it("is keyboard reachable with Tab in order", async () => {
    const user = userEvent.setup();
    renderToggles();
    await user.tab();
    expect(sessionBtn()).toHaveFocus();
    await user.tab();
    expect(bottomBtn()).toHaveFocus();
  });

  it("in the title bar sits after the palette and before Windows controls, and dblclick does not maximise", async () => {
    const user = userEvent.setup();
    const max = vi.fn();
    Object.defineProperty(window, "runtime", {
      configurable: true,
      value: { WindowToggleMaximise: max },
    });
    render(
      <MemoryRouter>
        <ShortcutsProvider platform="windows">
          <TooltipProvider>
            <AppTopBar platform="windows" layoutToggles={<LayoutToggles />} />
          </TooltipProvider>
        </ShortcutsProvider>
      </MemoryRouter>,
    );
    const header = screen.getByRole("banner");
    const html = header.innerHTML;
    expect(html.indexOf("layout-toggle-right")).toBeGreaterThan(-1);
    expect(html.indexOf("layout-toggle-right")).toBeLessThan(
      html.indexOf("Close window"),
    );
    expect(
      within(header)
        .getByTestId("layout-toggle-sessionList")
        .closest(".wails-no-drag"),
    ).not.toBeNull();
    await user.dblClick(bottomBtn());
    expect(max).not.toHaveBeenCalled();
    Reflect.deleteProperty(window, "runtime");
  });

  describe.each([
    {
      region: "bottom" as const,
      slots: ["bottomLeft", "bottomRight"] as const,
      hide: "Hide bottom tool windows",
      show: "Show bottom tool windows",
      shortcut: "⌘J",
      rebound: { mod: "primary" as const, key: "K" },
      reboundLabel: "⌘K",
      id: "toolWindow.region.bottom",
    },
    {
      region: "right" as const,
      slots: ["rightTop", "rightBottom"] as const,
      hide: "Hide right tool windows",
      show: "Show right tool windows",
      shortcut: "⌥⌘B",
      rebound: { mod: "primary-alt" as const, key: "R" },
      reboundLabel: "⌥⌘R",
      id: "toolWindow.region.right",
    },
  ])("$region toggle", (c) => {
    const btn = () => screen.getByTestId(`layout-toggle-${c.region}`);

    function hideRegionInStore() {
      const shown = { ...useToolWindowLayout.getState().shown };
      for (const slot of c.slots) shown[slot] = null;
      act(() => useToolWindowLayout.setState({ shown }));
    }
    const storeShown = () =>
      c.slots.some(
        (slot) => useToolWindowLayout.getState().shown[slot] !== null,
      );

    it("goes hidden -> shown -> hidden in the store and in aria-pressed, filled icon, name and tooltip", async () => {
      const user = userEvent.setup();
      hideRegionInStore();
      renderToggles();

      expect(btn()).toHaveAttribute("aria-pressed", "false");
      expect(btn()).toHaveAccessibleName(c.show);
      expect(btn().querySelector("[data-filled]")).toBeNull();
      await user.hover(btn());
      let tip = await screen.findByRole("tooltip");
      expect(tip).toHaveTextContent(c.show);
      expect(tip).toHaveTextContent(c.shortcut);
      await user.unhover(btn());

      await user.click(btn());
      expect(storeShown()).toBe(true);
      expect(btn()).toHaveAttribute("aria-pressed", "true");
      expect(btn()).toHaveAccessibleName(c.hide);
      expect(btn().querySelector("[data-filled]")).not.toBeNull();
      // 点击已把指针停在按钮上并收起提示；先移开再悬停才会重新打开。
      await user.unhover(btn());
      await user.hover(btn());
      tip = (await screen.findAllByRole("tooltip")).at(-1) as HTMLElement;
      expect(tip).toHaveTextContent(c.hide);
      expect(tip).toHaveTextContent(c.shortcut);
      await user.unhover(btn());

      await user.click(btn());
      expect(storeShown()).toBe(false);
      expect(btn()).toHaveAttribute("aria-pressed", "false");
      expect(btn()).toHaveAccessibleName(c.show);
      expect(btn().querySelector("[data-filled]")).toBeNull();
    });

    it("shows the rebound shortcut in the tooltip after the binding changes", async () => {
      const user = userEvent.setup();
      function Rebind() {
        const ctx = useShortcutsContext();
        return (
          <button type="button" onClick={() => ctx.setBinding(c.id, c.rebound)}>
            rebind
          </button>
        );
      }
      render(
        <MemoryRouter>
          <ShortcutsProvider platform="darwin">
            <TooltipProvider>
              <Rebind />
              <LayoutToggles />
            </TooltipProvider>
          </ShortcutsProvider>
        </MemoryRouter>,
      );
      await user.click(screen.getByText("rebind"));
      await user.hover(btn());
      const tip = await screen.findByRole("tooltip");
      expect(tip).toHaveTextContent(c.reboundLabel);
      expect(tip).not.toHaveTextContent(c.shortcut);
    });
  });
});
