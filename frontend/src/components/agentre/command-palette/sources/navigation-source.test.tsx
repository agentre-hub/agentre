import { render, screen } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";

import { useSettingsDialogStore } from "@/stores/settings-dialog-store";

import type { OnSelectCtx } from "../types";
import { navigationSource, type NavigationItem } from "./navigation-source";

function mkCtx(): OnSelectCtx {
  return {
    navigate: vi.fn(),
    close: vi.fn(),
    openSession: vi.fn(),
    openNewSession: vi.fn(),
    openNotChattableDialog: vi.fn(),
  };
}

function itemFor(labelKey: string): NavigationItem {
  const { items } = navigationSource.useItems();
  const item = items.find((i) => i.labelKey === labelKey);
  if (!item) throw new Error(`no navigation item ${labelKey}`);
  return item;
}

describe("navigationSource", () => {
  beforeEach(() => {
    useSettingsDialogStore.setState({ open: false, page: "appearance" });
  });

  it("Given the Settings item, When it is selected, Then the palette closes and the settings dialog opens without navigating", () => {
    const ctx = mkCtx();

    navigationSource.onSelect(itemFor("nav.settings"), ctx);

    expect(ctx.close).toHaveBeenCalledTimes(1);
    expect(ctx.navigate).not.toHaveBeenCalled();
    expect(useSettingsDialogStore.getState().open).toBe(true);
  });

  it("Given a page item, When it is selected, Then it navigates to that page and leaves settings closed", () => {
    const ctx = mkCtx();

    navigationSource.onSelect(itemFor("nav.issues"), ctx);

    expect(ctx.close).toHaveBeenCalledTimes(1);
    expect(ctx.navigate).toHaveBeenCalledWith("/issues");
    expect(useSettingsDialogStore.getState().open).toBe(false);
  });

  it("does not show a /settings route under the Settings row", () => {
    render(
      <>
        {navigationSource.renderItem(itemFor("nav.settings"), {
          active: false,
        })}
      </>,
    );

    expect(screen.getByText("Settings")).toBeInTheDocument();
    expect(screen.queryByText("/settings")).not.toBeInTheDocument();
  });
});
