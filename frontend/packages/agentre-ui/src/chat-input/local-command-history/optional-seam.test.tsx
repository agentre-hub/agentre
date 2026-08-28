import { act, render, screen, waitFor } from "@testing-library/react";
import { useRef } from "react";
import { describe, expect, it, vi } from "vitest";

import type { Editor } from "@tiptap/react";

import { AIChatInput } from "../index";
import {
  LocalCommandHistoryProvider,
  type LocalCommandHistoryAccess,
  type LocalCommandHistoryEntry,
  type LocalCommandHistoryScope,
} from "./access";

/**
 * 本地命令历史是**可选**能力（agentre-server 没有它）。这组用例锁住可选口的
 * 语义：宿主不提供时不渲染弹层，而不是渲染出一个空的/点了没反应的入口。
 */

const scope: LocalCommandHistoryScope = {
  deviceId: "device-optional-seam",
  cwd: "/repo/optional-seam",
};

function fakeAccess(
  entries: LocalCommandHistoryEntry[],
): LocalCommandHistoryAccess {
  return {
    deriveScopeKey: ({ deviceId, cwd }) => `${deviceId}:${cwd}`,
    list: () => entries,
    subscribe: () => () => {},
    reserveLastUsedAt: () => 1,
    releaseLastUsedAt: () => {},
    record: vi.fn(),
    clear: () => true,
  };
}

function Harness() {
  const editorRef = useRef<Editor | null>(null);
  return (
    <>
      <button
        type="button"
        data-testid="ins-bang"
        onClick={() => editorRef.current?.commands.insertContent("!")}
      >
        !
      </button>
      <AIChatInput
        editorRef={editorRef}
        onSubmit={() => {}}
        localCommandHistoryScope={scope}
        autoFocus
      />
    </>
  );
}

describe("AIChatInput local command history seam", () => {
  it("Given a host that provides the history access, When the user types a leading !, Then the recorded commands are offered", async () => {
    render(
      <LocalCommandHistoryProvider
        access={fakeAccess([{ command: "pnpm test", lastUsedAt: 7 }])}
      >
        <Harness />
      </LocalCommandHistoryProvider>,
    );

    act(() => screen.getByTestId("ins-bang").click());

    await waitFor(() => expect(screen.getByRole("listbox")).toBeTruthy());
    expect(screen.getByRole("option", { name: "pnpm test" })).toBeTruthy();
  });

  it("Given a host without the history access, When the user types a leading !, Then no history popover is rendered at all", async () => {
    render(<Harness />);

    act(() => screen.getByTestId("ins-bang").click());

    // 等一拍让编辑器的 update/selectionUpdate 跑完，确认弹层不是「还没来得及开」。
    await waitFor(() =>
      expect(screen.getByRole("textbox").textContent).toContain("!"),
    );
    expect(screen.queryByRole("listbox")).toBeNull();
  });
});
