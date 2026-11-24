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

import {
  selectActivePreviewTab,
  useFilePreviewTabsStore,
} from "@/stores/file-preview-tabs-store";
import {
  DEFAULT_FILE_SETTINGS,
  useFileSettingsStore,
} from "@/stores/file-settings-store";
import { useSessionRootStore } from "@/stores/session-root-store";

import { SessionChangesView } from "../views/session-changes-view";

import type { ChangeRow } from "../derive";

const CWD = "/Users/me/proj";
const WT = "/Users/me/proj-wt";

/** 会话 1 的两个根；select 给出时把当前根切过去（跟随关）。 */
function seedRoots(select?: string) {
  const store = useSessionRootStore.getState();
  store.applyRoots(1, CWD, [
    {
      path: CWD,
      name: "proj",
      isPrimary: true,
      isWorktree: false,
      aiHere: true,
    },
    {
      path: WT,
      name: "proj-wt",
      isPrimary: false,
      isWorktree: true,
      aiHere: false,
    },
  ]);
  if (select) store.select(1, select);
}

beforeEach(() => {
  localStorage.clear();
  useFilePreviewTabsStore.setState({ previewTabsBySession: {} });
  useSessionRootStore.setState({ bySession: {} });
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
  // Given 会话当前工作根是非主根（worktree）, When 点一行的文件,
  // Then 预览标签绑定的是这个根，而不是回落到会话主根。
  it("opens the tab under the session's current non-primary root", async () => {
    seedRoots(WT);
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

  // Given 会话当前工作根是主根（行的 cwd 是它的绝对路径）, When 点一行的文件,
  // Then 标签以主根的规范形（空串）绑定——与转录链接打开的是同一个标签，不会因
  // 「空串 / 绝对 cwd」两种写法开出两个。
  it("binds a primary-root row to the canonical '' root", async () => {
    seedRoots();
    useFilePreviewTabsStore
      .getState()
      .openPreview(1, { root: "", path: "src/a.ts" }, "session");
    render(
      <SessionChangesView
        sessionId={1}
        rows={[row]}
        cwd={CWD}
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

    const entry = useFilePreviewTabsStore.getState().previewTabsBySession[1];
    expect(entry.tabs).toHaveLength(1);
    expect(entry.tabs[0]).toMatchObject({ root: "", path: "src/a.ts" });
  });
});
