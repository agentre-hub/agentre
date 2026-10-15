import { render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter } from "react-router-dom";
import { describe, expect, it, vi } from "vitest";

import { Dialog, DialogContent, DialogTitle } from "@agentre-hub/agentre-ui";

import { KeyboardShortcutsPanel } from "../../shortcuts";
import { ShortcutsProvider } from "../../shortcuts/shortcuts-provider";

function renderPanel() {
  return render(
    <MemoryRouter>
      <ShortcutsProvider platform="darwin">
        <KeyboardShortcutsPanel />
      </ShortcutsProvider>
    </MemoryRouter>,
  );
}

describe("KeyboardShortcutsPanel", () => {
  it("shows current chat tab shortcuts instead of historical sidebar session copy", () => {
    renderPanel();

    const chatSection = screen
      .getByRole("heading", { name: "Chat Page" })
      .closest("section");

    expect(chatSection).not.toBeNull();
    const scope = within(chatSection!);

    expect(scope.getByText("Switch to Tab N")).toBeInTheDocument();
    expect(
      scope.getByText(
        "⌘1 - ⌘9 · Uses TabStrip order (pinned + normal + preview)",
      ),
    ).toBeInTheDocument();
    expect(scope.getByText("Close Current Tab")).toBeInTheDocument();
    expect(
      scope.getByText("Close the active tab (pinned tabs cannot be closed)"),
    ).toBeInTheDocument();
    expect(scope.queryByText(/attention 会话/)).not.toBeInTheDocument();
    expect(scope.queryByText(/sidebar 自上而下/)).not.toBeInTheDocument();
  });

  it("lists the Ctrl+Tab cycle shortcut as a fixed row", () => {
    renderPanel();

    const chatSection = screen
      .getByRole("heading", { name: "Chat Page" })
      .closest("section");
    const scope = within(chatSection!);

    expect(scope.getByText("Cycle Through Tabs")).toBeInTheDocument();
    // darwin platform → ⌃ (control) glyph for both directions.
    expect(
      scope.getByText("⌃Tab / ⌃⇧Tab · Next / previous tab (wraps around)"),
    ).toBeInTheDocument();
  });

  it("Given the panel sits in a dialog, When Esc is pressed while recording, Then only the recording is cancelled and the dialog stays open", async () => {
    // 设置是对话框：Radix 在 document 捕获阶段听 Esc，比录制的监听早一步。
    // 录制中这一下 Esc 归录制，不能顺带把整个设置关掉。
    const user = userEvent.setup();
    const onOpenChange = vi.fn();
    render(
      <MemoryRouter>
        <ShortcutsProvider platform="darwin">
          <Dialog open onOpenChange={onOpenChange}>
            <DialogContent aria-describedby={undefined}>
              <DialogTitle>Settings</DialogTitle>
              <KeyboardShortcutsPanel />
            </DialogContent>
          </Dialog>
        </ShortcutsProvider>
      </MemoryRouter>,
    );

    await user.click(screen.getAllByRole("button", { name: "Record" })[0]!);
    expect(screen.getByText("Press a new shortcut...")).toBeInTheDocument();

    await user.keyboard("{Escape}");

    expect(onOpenChange).not.toHaveBeenCalled();
    expect(
      screen.queryByText("Press a new shortcut..."),
    ).not.toBeInTheDocument();
    expect(
      screen.getByRole("dialog", { name: "Settings" }),
    ).toBeInTheDocument();
  });

  it("Given nothing is recording, When Esc is pressed, Then the surrounding dialog closes as usual", async () => {
    const user = userEvent.setup();
    const onOpenChange = vi.fn();
    render(
      <MemoryRouter>
        <ShortcutsProvider platform="darwin">
          <Dialog open onOpenChange={onOpenChange}>
            <DialogContent aria-describedby={undefined}>
              <DialogTitle>Settings</DialogTitle>
              <KeyboardShortcutsPanel />
            </DialogContent>
          </Dialog>
        </ShortcutsProvider>
      </MemoryRouter>,
    );

    await user.keyboard("{Escape}");

    expect(onOpenChange).toHaveBeenCalledWith(false);
  });
});
