import "@testing-library/jest-dom/vitest";

import { render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";

vi.mock("sonner", () => ({
  toast: { error: vi.fn(), success: vi.fn(), warning: vi.fn() },
}));
vi.mock("@/../wailsjs/go/app/App", () => ({
  WorkspaceFsWorkRoots: () => Promise.resolve([]),
  OpenPath: vi.fn(),
  RevealPath: vi.fn(),
}));

import { useChatSidebarStore } from "@/stores/chat-sidebar-store";
import {
  selectActivePreviewTab,
  useFilePreviewTabsStore,
} from "@/stores/file-preview-tabs-store";
import {
  DEFAULT_FILE_SETTINGS,
  useFileSettingsStore,
} from "@/stores/file-settings-store";

import { SessionChangesView } from "../views/session-changes-view";

import type { ChangeRow } from "../derive";

const WT = "/Users/me/proj-wt";

beforeEach(() => {
  localStorage.clear();
  useFilePreviewTabsStore.setState({ previewTabsBySession: {} });
  useChatSidebarStore.setState({ workRootBySession: {} });
  useFileSettingsStore.setState({ settings: { ...DEFAULT_FILE_SETTINGS } });
});

const row: ChangeRow = {
  path: "src/a.ts",
  name: "a.ts",
  dir: "src",
  status: "modified",
  plus: 1,
  minus: 0,
  lastTurn: 1,
};

describe("sidebar row · 预览标签绑定打开时的工作根", () => {
  // Given 侧栏当前选中的是非主根（worktree）, When 点一行的文件,
  // Then 预览标签绑定的是这个根，而不是回落到会话主根。
  it("opens the tab under the sidebar's current non-primary root", async () => {
    useChatSidebarStore.getState().setWorkRoot(1, WT);
    render(
      <SessionChangesView
        sessionId={1}
        rows={[row]}
        cwd={WT}
        remote={false}
        onJumpToTurn={() => {}}
      />,
    );
    const el = screen
      .getAllByTestId(/-row$/)
      .find((e) => e.getAttribute("data-name") === "a.ts")!;
    await userEvent
      .setup({ pointerEventsCheck: 0 })
      .click(within(el).getByText("a.ts"));

    expect(
      selectActivePreviewTab(useFilePreviewTabsStore.getState(), 1),
    ).toMatchObject({ root: WT, path: "src/a.ts" });
  });
});
