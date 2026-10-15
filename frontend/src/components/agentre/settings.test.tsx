import { act, render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import {
  MemoryRouter,
  useNavigate,
  type NavigateFunction,
} from "react-router-dom";
import { beforeEach, describe, expect, it, vi } from "vitest";

import { DialogShell, DialogShellHeader } from "@agentre-hub/agentre-ui";
import {
  openSettings,
  useSettingsDialogStore,
} from "@/stores/settings-dialog-store";

// 子页里再开一层「正在保存」的弹窗：用来钉住忙碌规则落在最上面那一层。
const nested = vi.hoisted(() => ({ busyOpen: false, onOpenChange: vi.fn() }));

vi.mock("@/hooks/use-chat-agents", () => ({
  useChatAgents: () => ({ agents: [] }),
}));

vi.mock("./sync", () => ({
  SyncPanel: () => null,
  useSyncStatus: () => ({ loading: false, status: null }),
}));

// Agent 后端页的 H1 现在由面板通过 renderHeader 交回宿主渲染（页级操作要落在标题
// 行），所以这个替身必须调一次 renderHeader，页头才还在。传 null 表示「没有按钮可
// 交」——替身不复刻真面板的 actions，别把它读成真实契约。
vi.mock("./agent-backends", () => ({
  AgentBackendsPanel: ({
    renderHeader,
  }: {
    renderHeader?: (actions: React.ReactNode) => React.ReactNode;
  }) => <>{renderHeader?.(null)}</>,
}));

vi.mock("./remote-devices/remote-devices-panel", () => ({
  RemoteDevicesPanel: ({
    onOpenAgentBackends,
  }: {
    onOpenAgentBackends: () => void;
  }) => (
    <>
      <button type="button" onClick={onOpenAgentBackends}>
        Configure Agent Backends
      </button>
      <DialogShell
        open={nested.busyOpen}
        busy
        onOpenChange={nested.onOpenChange}
      >
        <DialogShellHeader title="Signing in" />
      </DialogShell>
    </>
  ),
}));

import { SettingsDialog } from "./settings/page";

function renderSettingsDialog() {
  return render(
    <MemoryRouter>
      <SettingsDialog
        effectiveTheme="light"
        onThemePreferenceChange={() => {}}
        themePreference="system"
      />
    </MemoryRouter>,
  );
}

beforeEach(() => {
  useSettingsDialogStore.setState({ open: false, page: "appearance" });
  nested.busyOpen = false;
  nested.onOpenChange.mockReset();
});

describe("SettingsDialog remote-device onboarding navigation", () => {
  it("opens Agent Backends from the Remote Devices completion action", async () => {
    const user = userEvent.setup();
    openSettings("remote-devices");
    renderSettingsDialog();

    await user.click(
      screen.getByRole("button", { name: "Configure Agent Backends" }),
    );

    // 真正证明「跳过去了」的是左侧导航的选中态：它由 SettingsNav 自己渲染，
    // 完全不经过被替身接管的面板。
    expect(screen.getByTestId("settings-nav-agent-backend")).toHaveAttribute(
      "aria-current",
      "page",
    );
    expect(
      screen.getByRole("heading", { name: "Agent Backends" }),
    ).toBeInTheDocument();
  });
});

describe("SettingsDialog backdrop", () => {
  it("Given settings is open, When rendered, Then the page underneath is dimmed by the settings scrim, not the form-dialog scrim", () => {
    openSettings("appearance");
    renderSettingsDialog();

    const overlay = document.querySelector<HTMLElement>(
      '[data-slot="settings-dialog-overlay"]',
    );
    if (!overlay) throw new Error("expected the settings scrim");
    expect(overlay).toHaveClass("bg-settings-scrim");
    expect(overlay).not.toHaveClass("bg-scrim");
  });
});

describe("SettingsDialog busy sub-page", () => {
  it("Given a sub-page dialog is busy saving, When Esc is pressed or its scrim is clicked, Then neither it nor settings closes", async () => {
    const user = userEvent.setup();
    nested.busyOpen = true;
    openSettings("remote-devices");
    renderSettingsDialog();

    expect(
      screen.getByRole("dialog", { name: "Signing in" }),
    ).toBeInTheDocument();

    await user.keyboard("{Escape}");
    const overlay = document.querySelector<HTMLElement>(
      '[data-slot="dialog-shell-overlay"]',
    );
    if (!overlay) throw new Error("expected the nested dialog scrim");
    await user.click(overlay);

    expect(nested.onOpenChange).not.toHaveBeenCalled();
    expect(useSettingsDialogStore.getState().open).toBe(true);
    expect(
      screen.getByRole("dialog", { name: "Signing in" }),
    ).toBeInTheDocument();
  });

  it("Given nothing above settings is busy, When Esc is pressed, Then settings closes", async () => {
    const user = userEvent.setup();
    openSettings("appearance");
    renderSettingsDialog();

    await user.keyboard("{Escape}");

    expect(useSettingsDialogStore.getState().open).toBe(false);
  });
});

// 设置浮在当前页面上；任何一次导航（子页里「打开这条远端会话」跳 /chat、⌘B 去
// Issues、命令面板里选一页……）都是用户要离开去看别处，设置还盖着就等于什么都没
// 发生。同一路径也算：从对话页打开设置、再在里面打开一条会话，导航仍是 /chat。
describe("SettingsDialog navigation", () => {
  let navigate: NavigateFunction | null = null;
  function NavigateProbe() {
    navigate = useNavigate();
    return null;
  }

  function renderWithProbe() {
    return render(
      <MemoryRouter initialEntries={["/chat"]}>
        <NavigateProbe />
        <SettingsDialog
          effectiveTheme="light"
          onThemePreferenceChange={() => {}}
          themePreference="system"
        />
      </MemoryRouter>,
    );
  }

  it("Given settings is open over the chat page, When the app navigates to the same path, Then settings closes", () => {
    openSettings("remote-devices");
    renderWithProbe();
    expect(useSettingsDialogStore.getState().open).toBe(true);

    act(() => navigate!("/chat"));

    expect(useSettingsDialogStore.getState().open).toBe(false);
  });

  it("Given settings is open, When the app navigates to another page, Then settings closes and remembers its sub-page", () => {
    openSettings("files");
    renderWithProbe();

    act(() => navigate!("/issues"));

    expect(useSettingsDialogStore.getState()).toMatchObject({
      open: false,
      page: "files",
    });
  });

  it("Given settings was opened before the dialog mounted, When nothing navigates, Then it stays open", () => {
    openSettings("appearance");
    renderWithProbe();

    expect(
      screen.getByRole("dialog", { name: "Settings" }),
    ).toBeInTheDocument();
  });
});
