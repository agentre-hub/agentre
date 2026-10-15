import { fireEvent, render, screen } from "@testing-library/react";
import { MemoryRouter, useLocation } from "react-router-dom";
import { beforeEach, describe, expect, it, vi } from "vitest";

import "@/i18n";

import { useSettingsDialogStore } from "@/stores/settings-dialog-store";

import { NotChattableDialog } from "./not-chattable-dialog";
import { blockReasonToCta, ORG_SELECTED_STORAGE_KEY } from "./mapping";

function LocationProbe() {
  const location = useLocation();
  return <output data-testid="location">{location.pathname}</output>;
}

beforeEach(() => {
  useSettingsDialogStore.setState({ open: false, page: "appearance" });
});

function renderDialog(
  blockReason: string,
  onOpenChange: (open: boolean) => void = () => undefined,
) {
  return render(
    <MemoryRouter initialEntries={["/chat"]}>
      <LocationProbe />
      <NotChattableDialog
        agent={{ blockReason, id: 42, name: "CEO" }}
        open
        onOpenChange={onOpenChange}
      />
    </MemoryRouter>,
  );
}

describe("blockReasonToCta", () => {
  it("maps each setup branch to its primary target and copy key", () => {
    expect(blockReasonToCta("no-backend")).toMatchObject({
      copyKey: "chatPage.notChattable.reasons.noBackend",
      primaryTarget: "org-agent:<id>",
    });
    expect(blockReasonToCta("provider-inactive")).toMatchObject({
      primaryTarget: "settings:llm-providers",
    });
    expect(blockReasonToCta("remote-provider-missing")).toMatchObject({
      primaryTarget: "settings:remote-devices",
    });
    expect(blockReasonToCta("gateway-not-running")).toMatchObject({
      primaryTarget: "settings:agent-backend",
    });
    expect(blockReasonToCta("remote-openclaw-unavailable")).toMatchObject({
      primaryTarget: "settings:remote-devices",
    });
    expect(blockReasonToCta("unknown-backend")).toMatchObject({
      primaryTarget: "settings:agent-backend",
    });
  });

  it("falls back to the unknown-backend guidance for an unknown reason", () => {
    expect(blockReasonToCta("future-reason").copyKey).toBe(
      "chatPage.notChattable.reasons.unknownBackend",
    );
  });
});

describe("NotChattableDialog primary action", () => {
  it("stores the selected Agent and navigates to the org chart for no-backend", () => {
    renderDialog("no-backend");

    fireEvent.click(
      screen.getByRole("button", { name: "Configure Agent backend" }),
    );

    expect(localStorage.getItem(ORG_SELECTED_STORAGE_KEY)).toBe(
      JSON.stringify({ kind: "agent", id: 42 }),
    );
    expect(screen.getByTestId("location")).toHaveTextContent("/org");
  });

  it("opens the settings dialog on the llm-providers page for provider setup", () => {
    renderDialog("provider-inactive");

    fireEvent.click(
      screen.getByRole("button", { name: "Configure LLM provider" }),
    );

    expect(useSettingsDialogStore.getState()).toMatchObject({
      open: true,
      page: "llm-providers",
    });
    expect(screen.getByTestId("location")).toHaveTextContent("/chat");
  });

  it("closes itself before the settings dialog opens, so the two never stack", () => {
    let settingsOpenWhenClosed: boolean | null = null;
    const onOpenChange = vi.fn((open: boolean) => {
      if (!open) {
        settingsOpenWhenClosed = useSettingsDialogStore.getState().open;
      }
    });
    renderDialog("gateway-not-running", onOpenChange);

    fireEvent.click(screen.getByRole("button", { name: /agent backend/i }));

    expect(onOpenChange).toHaveBeenCalledWith(false);
    expect(settingsOpenWhenClosed).toBe(false);
    expect(useSettingsDialogStore.getState()).toMatchObject({
      open: true,
      page: "agent-backend",
    });
  });
});
