import {
  act,
  fireEvent,
  render as renderBare,
  screen,
  within,
} from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import {
  createRef,
  type ReactElement,
  type ReactNode,
  type RefObject,
} from "react";
import { beforeEach, describe, expect, it, onTestFinished, vi } from "vitest";

const sonnerMocks = vi.hoisted(() => ({
  toast: {
    error: vi.fn(),
    success: vi.fn(),
  },
}));

vi.mock("sonner", () => sonnerMocks);

import type { Editor } from "@tiptap/react";

import {
  AIChatInput,
  LocalCommandHistoryProvider,
  type AIChatInputHandle,
  type LocalCommandHistoryScope,
  type LocalCommandSubmitHandler,
} from "@agentre-ai/agentre-ui";

import { useLocalCommandsStore } from "@/stores/local-commands-store";
import { localCommandHistoryStore } from "@/stores/local-command-history-store";

import { desktopLocalCommandHistoryAccess } from "../local-command-history-access-desktop";

const repoScope: LocalCommandHistoryScope = {
  deviceId: "device-command-mode",
  cwd: "/repo/command-mode",
};
const otherScope: LocalCommandHistoryScope = {
  deviceId: "device-command-mode",
  cwd: "/repo/other",
};
const releaseReservedTimestamp =
  localCommandHistoryStore.releaseLastUsedAt.bind(localCommandHistoryStore);

beforeEach(() => {
  vi.restoreAllMocks();
  sonnerMocks.toast.error.mockClear();
  sonnerMocks.toast.success.mockClear();
  localCommandHistoryStore.clear(repoScope);
  localCommandHistoryStore.clear(otherScope);
  useLocalCommandsStore.setState({ entries: {} });
});

/**
 * 本地命令历史是可选的宿主能力：桌面端把 localStorage store 接到包的接缝上，
 * 输入框才渲染 ! 历史弹层。整组用例都要在这个 Provider 里跑。
 */
function render(ui: ReactElement) {
  return renderBare(ui, {
    wrapper: ({ children }: { children: ReactNode }) => (
      <LocalCommandHistoryProvider access={desktopLocalCommandHistoryAccess}>
        {children}
      </LocalCommandHistoryProvider>
    ),
  });
}

/** 在编辑器的 contentEditable DOM 上派发一次 keydown，驱动 TipTap 菜单/提交路径。 */
function pressKey(editor: Editor, key: string, init: KeyboardEventInit = {}) {
  const event = new KeyboardEvent("keydown", {
    key,
    bubbles: true,
    cancelable: true,
    ...init,
  });
  editor.view.dom.dispatchEvent(event);
  return event;
}

function pressEnter(editor: Editor) {
  pressKey(editor, "Enter");
}

function reserveReleasedHistoryBase() {
  const timestamp = localCommandHistoryStore.reserveLastUsedAt();
  releaseReservedTimestamp(timestamp);
  return timestamp;
}

describe("AIChatInput command mode", () => {
  it("toggles command mode on leading ! and strips it on submit", () => {
    const onCommandModeChange = vi.fn();
    const onCommandSubmit = vi.fn();
    const onSubmit = vi.fn();
    vi.spyOn(localCommandHistoryStore, "reserveLastUsedAt").mockReturnValue(1);
    const editorRef: RefObject<Editor | null> = { current: null };
    const handleRef = createRef<AIChatInputHandle>();

    render(
      <AIChatInput
        ref={handleRef}
        editorRef={editorRef}
        sendOnEnter
        onSubmit={onSubmit}
        onCommandModeChange={onCommandModeChange}
        onCommandSubmit={onCommandSubmit}
      />,
    );

    const editor = editorRef.current!;

    // Insert content starting with !
    act(() => {
      editor.commands.insertContent("!go test ./...");
    });

    expect(onCommandModeChange).toHaveBeenLastCalledWith(true);

    // Press Enter to submit
    act(() => {
      pressEnter(editor);
    });

    expect(onCommandSubmit).toHaveBeenCalledWith("go test ./...");
    expect(onSubmit).not.toHaveBeenCalled();
    // Editor should be cleared after submit
    expect(editor.getText()).toBe("");
  });

  it("stays normal and routes to onSubmit without leading !", () => {
    const onCommandSubmit = vi.fn();
    const onSubmit = vi.fn();
    const editorRef: RefObject<Editor | null> = { current: null };

    render(
      <AIChatInput
        editorRef={editorRef}
        sendOnEnter
        onSubmit={onSubmit}
        onCommandSubmit={onCommandSubmit}
      />,
    );

    const editor = editorRef.current!;

    act(() => {
      editor.commands.insertContent("hello");
    });

    act(() => {
      pressEnter(editor);
    });

    expect(onSubmit).toHaveBeenCalledWith("hello");
    expect(onCommandSubmit).not.toHaveBeenCalled();
  });

  it("does not call onCommandSubmit or reserve history order for bare ! (empty command)", () => {
    const onCommandSubmit = vi.fn();
    const onSubmit = vi.fn();
    const reserveSpy = vi.spyOn(localCommandHistoryStore, "reserveLastUsedAt");
    const editorRef: RefObject<Editor | null> = { current: null };

    render(
      <AIChatInput
        editorRef={editorRef}
        sendOnEnter
        onSubmit={onSubmit}
        onCommandSubmit={onCommandSubmit}
      />,
    );

    const editor = editorRef.current!;

    act(() => {
      editor.commands.insertContent("!");
    });

    act(() => {
      pressEnter(editor);
    });

    expect(onCommandSubmit).not.toHaveBeenCalled();
    expect(onSubmit).not.toHaveBeenCalled();
    expect(reserveSpy).not.toHaveBeenCalled();
    // Content should be cleared
    expect(editor.getText()).toBe("");
  });

  it.each<{
    label: string;
    onCommandSubmit?: LocalCommandSubmitHandler;
  }>([
    { label: "the callback is missing" },
    {
      label: "the callback returns an undefined execution scope",
      onCommandSubmit: () => undefined,
    },
    {
      label: "the callback throws synchronously",
      onCommandSubmit: () => {
        throw new Error("sync launch failed");
      },
    },
    {
      label: "the callback promise rejects",
      onCommandSubmit: () => Promise.reject(new Error("async launch failed")),
    },
    {
      label: "the callback promise resolves without an execution scope",
      onCommandSubmit: () => Promise.resolve(undefined),
    },
  ])(
    "Given $label, when a command reservation is followed by Clear, then it is released exactly once without recording or retaining the cleared scope",
    async ({ onCommandSubmit }) => {
      const editorRef: RefObject<Editor | null> = { current: null };
      const reserveSpy = vi.spyOn(
        localCommandHistoryStore,
        "reserveLastUsedAt",
      );
      const releaseSpy = vi.spyOn(
        localCommandHistoryStore,
        "releaseLastUsedAt",
      );
      const recordSpy = vi.spyOn(localCommandHistoryStore, "record");
      vi.spyOn(console, "warn").mockImplementation(() => {});

      render(
        <AIChatInput
          editorRef={editorRef}
          localCommandHistoryScope={repoScope}
          onSubmit={vi.fn()}
          onCommandSubmit={onCommandSubmit}
        />,
      );

      act(() => {
        editorRef.current!.commands.insertContent("!pwd");
        pressEnter(editorRef.current!);
      });
      const submittedAt = reserveSpy.mock.results[0]?.value as number;
      onTestFinished(() => releaseReservedTimestamp(submittedAt));

      act(() => localCommandHistoryStore.clear(repoScope));

      await vi.waitFor(() => {
        expect(releaseSpy).toHaveBeenCalledTimes(1);
      });
      expect(releaseSpy).toHaveBeenCalledWith(submittedAt);
      expect(recordSpy).not.toHaveBeenCalled();
      expect(localCommandHistoryStore.list(repoScope)).toEqual([]);

      act(() => {
        localCommandHistoryStore.record(
          repoScope,
          "allowed after settlement",
          submittedAt,
        );
      });
      expect(localCommandHistoryStore.list(repoScope)).toEqual([
        {
          command: "allowed after settlement",
          lastUsedAt: submittedAt,
        },
      ]);
    },
  );

  it("Given a successful command scope resolves after Clear, when history rejects the pre-clear reservation, then final release is idempotent and the command is not resurrected", async () => {
    const editorRef: RefObject<Editor | null> = { current: null };
    let resolveScope!: (scope: LocalCommandHistoryScope) => void;
    const executionScope = new Promise<LocalCommandHistoryScope>((resolve) => {
      resolveScope = resolve;
    });
    const reserveSpy = vi.spyOn(localCommandHistoryStore, "reserveLastUsedAt");
    const releaseSpy = vi.spyOn(localCommandHistoryStore, "releaseLastUsedAt");
    const recordSpy = vi.spyOn(localCommandHistoryStore, "record");

    render(
      <AIChatInput
        editorRef={editorRef}
        localCommandHistoryScope={repoScope}
        onSubmit={vi.fn()}
        onCommandSubmit={() => executionScope}
      />,
    );

    act(() => {
      editorRef.current!.commands.insertContent("!private command");
      pressEnter(editorRef.current!);
    });
    const submittedAt = reserveSpy.mock.results[0]?.value as number;
    onTestFinished(() => releaseReservedTimestamp(submittedAt));
    act(() => localCommandHistoryStore.clear(repoScope));

    await act(async () => {
      resolveScope(repoScope);
      await executionScope;
    });
    await vi.waitFor(() => {
      expect(recordSpy).toHaveBeenCalledWith(
        repoScope,
        "private command",
        submittedAt,
      );
      expect(releaseSpy).toHaveBeenCalledTimes(1);
    });
    expect(localCommandHistoryStore.list(repoScope)).toEqual([]);

    act(() => {
      localCommandHistoryStore.record(
        repoScope,
        "allowed after settlement",
        submittedAt,
      );
    });
    expect(localCommandHistoryStore.list(repoScope)).toEqual([
      { command: "allowed after settlement", lastUsedAt: submittedAt },
    ]);
  });

  it("dedupes onCommandModeChange — does not re-fire when already in command mode", () => {
    const onCommandModeChange = vi.fn();
    const editorRef: RefObject<Editor | null> = { current: null };

    render(
      <AIChatInput
        editorRef={editorRef}
        sendOnEnter
        onSubmit={() => {}}
        onCommandModeChange={onCommandModeChange}
      />,
    );

    const editor = editorRef.current!;

    act(() => {
      editor.commands.insertContent("!a");
    });
    const callsAfterFirst = onCommandModeChange.mock.calls.length;

    act(() => {
      editor.commands.insertContent("b");
    });
    // should not have been called again since still in command mode
    expect(onCommandModeChange.mock.calls.length).toBe(callsAfterFirst);
    expect(onCommandModeChange).toHaveBeenLastCalledWith(true);
  });

  it("exits command mode when ! is removed", () => {
    const onCommandModeChange = vi.fn();
    const editorRef: RefObject<Editor | null> = { current: null };
    const handleRef = createRef<AIChatInputHandle>();

    render(
      <AIChatInput
        ref={handleRef}
        editorRef={editorRef}
        sendOnEnter
        onSubmit={() => {}}
        onCommandModeChange={onCommandModeChange}
      />,
    );

    const editor = editorRef.current!;

    act(() => {
      editor.commands.insertContent("!cmd");
    });
    expect(onCommandModeChange).toHaveBeenLastCalledWith(true);

    act(() => {
      handleRef.current!.clear();
    });

    // After clearing, should report false
    expect(onCommandModeChange).toHaveBeenLastCalledWith(false);
  });

  it("Given active-scope history, When a spaced ! query is typed, Then only ranked matches from that scope open in an accessible menu", async () => {
    const historyBase = reserveReleasedHistoryBase();
    localCommandHistoryStore.record(repoScope, "git status", historyBase + 10);
    localCommandHistoryStore.record(
      repoScope,
      "git checkout main",
      historyBase + 30,
    );
    localCommandHistoryStore.record(
      otherScope,
      "git checkout secret",
      historyBase + 40,
    );
    const editorRef: RefObject<Editor | null> = { current: null };

    render(
      <AIChatInput
        editorRef={editorRef}
        localCommandHistoryScope={repoScope}
        onSubmit={vi.fn()}
        onCommandSubmit={vi.fn()}
      />,
    );

    act(() => {
      editorRef.current!.commands.insertContent("!git ch ma");
    });

    const menu = await screen.findByRole("listbox", {
      name: "Shell command history",
    });
    expect(menu).toBeInTheDocument();
    expect(
      screen.getByRole("option", { name: "git checkout main" }),
    ).toHaveAttribute("aria-selected", "true");
    expect(screen.queryByText("git checkout secret")).not.toBeInTheDocument();
    expect(screen.queryByText("git status")).not.toBeInTheDocument();
  });

  it("Given history exists, When input is not in ! mode or the full query has no match, Then no empty menu is rendered", async () => {
    const historyBase = reserveReleasedHistoryBase();
    localCommandHistoryStore.record(repoScope, "git status", historyBase + 10);
    const editorRef: RefObject<Editor | null> = { current: null };

    render(
      <AIChatInput
        editorRef={editorRef}
        localCommandHistoryScope={repoScope}
        onSubmit={vi.fn()}
        onCommandSubmit={vi.fn()}
      />,
    );

    act(() => {
      editorRef.current!.commands.insertContent("git");
    });
    expect(screen.queryByRole("listbox")).not.toBeInTheDocument();

    await act(async () => {
      editorRef.current!.commands.setContent("!no matching command");
      editorRef.current!.commands.focus("end");
      await Promise.resolve();
    });
    expect(screen.queryByRole("listbox")).not.toBeInTheDocument();
  });

  it("Given scoped history, When arrows move between command rows and footer Clear, Then only rows are options and editor ARIA follows its focused row", async () => {
    const historyBase = reserveReleasedHistoryBase();
    localCommandHistoryStore.record(repoScope, "git status", historyBase + 30);
    localCommandHistoryStore.record(repoScope, "git stash", historyBase + 20);
    const editorRef: RefObject<Editor | null> = { current: null };
    const user = userEvent.setup();

    render(
      <AIChatInput
        editorRef={editorRef}
        localCommandHistoryScope={repoScope}
        onSubmit={vi.fn()}
        onCommandSubmit={vi.fn()}
      />,
    );
    const editor = editorRef.current!;

    act(() => {
      editor.commands.insertContent("!git");
      editor.commands.focus("end");
    });
    // commands.focus 通过 requestAnimationFrame 聚焦，等待焦点落定后再断言/导航。
    await vi.waitFor(() => expect(screen.getByRole("combobox")).toHaveFocus());

    const listbox = await screen.findByRole("listbox", {
      name: "Shell command history",
    });
    const firstOption = screen.getByRole("option", { name: "git status" });
    const secondOption = screen.getByRole("option", { name: "git stash" });
    const clearButton = screen.getByRole("button", {
      name: "Clear history for current directory",
    });
    const combobox = screen.getByRole("combobox");
    const listboxId = listbox.id;
    const firstOptionId = firstOption.id;
    const secondOptionId = secondOption.id;

    expect(combobox).toHaveFocus();
    expect(listboxId).not.toBe("");
    expect(firstOptionId).not.toBe("");
    expect(secondOptionId).not.toBe("");
    expect(firstOptionId).not.toBe(secondOptionId);
    expect(listbox).not.toContainElement(clearButton);
    expect(
      screen.queryByRole("option", {
        name: "Clear history for current directory",
      }),
    ).not.toBeInTheDocument();
    expect(clearButton).not.toHaveAttribute("aria-selected");
    expect(combobox).toHaveAttribute("aria-expanded", "true");
    expect(combobox).toHaveAttribute("aria-haspopup", "listbox");
    expect(combobox).toHaveAttribute("aria-controls", listboxId);
    expect(combobox).toHaveAttribute("aria-activedescendant", firstOptionId);
    expect(firstOption).toHaveAttribute("aria-selected", "true");

    act(() => pressKey(editor, "ArrowUp"));
    expect(clearButton).toHaveFocus();
    expect(combobox).not.toHaveAttribute("aria-activedescendant");
    expect(firstOption).toHaveAttribute("aria-selected", "false");
    expect(secondOption).toHaveAttribute("aria-selected", "false");

    await user.keyboard("{ArrowUp}");
    expect(combobox).toHaveFocus();
    expect(combobox).toHaveAttribute("aria-activedescendant", secondOptionId);
    expect(secondOption).toHaveAttribute("aria-selected", "true");

    act(() => pressKey(editor, "ArrowDown"));
    expect(clearButton).toHaveFocus();
    expect(combobox).not.toHaveAttribute("aria-activedescendant");

    await user.keyboard("{Escape}");
    const textbox = screen.getByRole("textbox");
    expect(textbox).toHaveFocus();
    expect(textbox).not.toHaveAttribute("aria-expanded");
    expect(textbox).not.toHaveAttribute("aria-haspopup");
    expect(textbox).not.toHaveAttribute("aria-controls");
    expect(textbox).not.toHaveAttribute("aria-activedescendant");
    expect(screen.queryByRole("listbox")).not.toBeInTheDocument();
  });

  it("Given cursor coordinates are unavailable, When Enter submits a matching ! query, Then no hidden history row consumes the command", () => {
    const historyBase = reserveReleasedHistoryBase();
    localCommandHistoryStore.record(repoScope, "git status", historyBase + 10);
    const editorRef: RefObject<Editor | null> = { current: null };
    const onCommandSubmit = vi.fn();

    render(
      <AIChatInput
        editorRef={editorRef}
        localCommandHistoryScope={repoScope}
        onSubmit={vi.fn()}
        onCommandSubmit={onCommandSubmit}
      />,
    );
    const editor = editorRef.current!;
    vi.spyOn(editor.view, "coordsAtPos").mockImplementation(() => {
      throw new Error("editor view is not measurable");
    });

    act(() => {
      editor.commands.insertContent("!git");
    });
    expect(screen.queryByRole("listbox")).not.toBeInTheDocument();

    act(() => {
      pressEnter(editor);
    });

    expect(onCommandSubmit).toHaveBeenCalledWith("git");
    expect(editor.getText()).toBe("");
  });

  it("Given open ranked history, When Shift+Tab is pressed and then plain Tab is pressed, Then Shift+Tab preserves the draft, selection, and menu while plain Tab only fills the highlighted command", async () => {
    const historyBase = reserveReleasedHistoryBase();
    localCommandHistoryStore.record(
      repoScope,
      "git cherry-pick master",
      historyBase + 10,
    );
    localCommandHistoryStore.record(
      repoScope,
      "git checkout main",
      historyBase + 30,
    );
    const editorRef: RefObject<Editor | null> = { current: null };
    const onCommandSubmit = vi.fn();
    const onSubmit = vi.fn();

    render(
      <AIChatInput
        editorRef={editorRef}
        localCommandHistoryScope={repoScope}
        onSubmit={onSubmit}
        onCommandSubmit={onCommandSubmit}
      />,
    );
    const editor = editorRef.current!;

    act(() => {
      editor.commands.insertContent("!git ch ma");
      editor.commands.focus("end");
    });
    await screen.findByRole("option", { name: "git checkout main" });
    act(() => pressKey(editor, "ArrowDown"));
    const highlightedOption = screen.getByRole("option", {
      name: "git cherry-pick master",
    });
    expect(highlightedOption).toHaveAttribute("aria-selected", "true");
    const selectionBefore = {
      from: editor.state.selection.from,
      to: editor.state.selection.to,
    };

    let shiftTabEvent!: KeyboardEvent;
    act(() => {
      shiftTabEvent = pressKey(editor, "Tab", { shiftKey: true });
    });

    expect(shiftTabEvent.defaultPrevented).toBe(false);
    expect(editor.getText()).toBe("!git ch ma");
    expect(editor.state.selection).toMatchObject(selectionBefore);
    expect(highlightedOption).toHaveAttribute("aria-selected", "true");
    expect(
      screen.getByRole("listbox", { name: "Shell command history" }),
    ).toBeInTheDocument();
    expect(localCommandHistoryStore.list(repoScope)).toEqual([
      { command: "git checkout main", lastUsedAt: historyBase + 30 },
      { command: "git cherry-pick master", lastUsedAt: historyBase + 10 },
    ]);
    expect(onCommandSubmit).not.toHaveBeenCalled();
    expect(onSubmit).not.toHaveBeenCalled();

    act(() => pressKey(editor, "Tab"));

    expect(editor.getText()).toBe("!git cherry-pick master");
    expect(onCommandSubmit).not.toHaveBeenCalled();
    expect(onSubmit).not.toHaveBeenCalled();
    expect(screen.queryByRole("listbox")).not.toBeInTheDocument();
  });

  it.each(["Enter", "Tab"])(
    "Given ranked history, When ArrowDown and %s choose a row, Then the full ! body is replaced without execution and the next submit records under the returned execution scope",
    async (selectionKey) => {
      const historyBase = reserveReleasedHistoryBase();
      localCommandHistoryStore.record(
        repoScope,
        "git cherry-pick master",
        historyBase + 10,
      );
      localCommandHistoryStore.record(
        repoScope,
        "git checkout main",
        historyBase + 30,
      );
      const editorRef: RefObject<Editor | null> = { current: null };
      const events: string[] = [];
      const originalRecord = localCommandHistoryStore.record.bind(
        localCommandHistoryStore,
      );
      const recordSpy = vi
        .spyOn(localCommandHistoryStore, "record")
        .mockImplementation((scope, command, lastUsedAt) => {
          events.push(`record:${command}`);
          originalRecord(scope, command, lastUsedAt);
        });
      const onCommandSubmit = vi.fn((command: string) => {
        events.push(`submit:${command}`);
        return repoScope;
      });
      const onSubmit = vi.fn();

      render(
        <AIChatInput
          editorRef={editorRef}
          localCommandHistoryScope={repoScope}
          onSubmit={onSubmit}
          onCommandSubmit={onCommandSubmit}
        />,
      );
      const editor = editorRef.current!;

      act(() => {
        editor.commands.insertContent("!git ch ma");
      });
      await screen.findByRole("option", { name: "git checkout main" });

      act(() => {
        pressKey(editor, "ArrowDown");
      });
      expect(
        screen.getByRole("option", { name: "git cherry-pick master" }),
      ).toHaveAttribute("aria-selected", "true");
      act(() => {
        pressKey(editor, "ArrowUp");
      });
      expect(
        screen.getByRole("option", { name: "git checkout main" }),
      ).toHaveAttribute("aria-selected", "true");
      act(() => {
        pressKey(editor, "ArrowDown");
      });
      expect(
        screen.getByRole("option", { name: "git cherry-pick master" }),
      ).toHaveAttribute("aria-selected", "true");

      act(() => {
        pressKey(editor, selectionKey);
      });

      expect(editor.getText()).toBe("!git cherry-pick master");
      act(() => {
        editor.commands.insertContent(" --no-verify");
      });
      expect(editor.getText()).toBe("!git cherry-pick master --no-verify");
      expect(onCommandSubmit).not.toHaveBeenCalled();
      expect(onSubmit).not.toHaveBeenCalled();
      expect(recordSpy).not.toHaveBeenCalled();
      expect(screen.queryByRole("listbox")).not.toBeInTheDocument();
      expect(localCommandHistoryStore.list(repoScope)[1]).toEqual({
        command: "git cherry-pick master",
        lastUsedAt: historyBase + 10,
      });

      act(() => {
        editor.commands.setTextSelection(1);
        editor.commands.setTextSelection(editor.state.doc.content.size - 1);
      });
      expect(screen.queryByRole("listbox")).not.toBeInTheDocument();

      act(() => {
        editor.commands.setContent("!   git cherry-pick master   ");
        editor.commands.focus("end");
      });
      await screen.findByRole("listbox", { name: "Shell command history" });
      act(() => {
        pressKey(editor, "Escape");
      });
      expect(screen.queryByRole("listbox")).not.toBeInTheDocument();
      act(() => {
        pressEnter(editor);
      });

      await vi.waitFor(() => {
        expect(events).toEqual([
          "submit:git cherry-pick master",
          "record:git cherry-pick master",
        ]);
      });
      expect(onCommandSubmit).toHaveBeenCalledWith("git cherry-pick master");
      expect(editor.getText()).toBe("");
    },
  );

  it("Given two nonempty command submissions, When their execution scopes resolve in reverse order, Then each reserves singleton history order immediately before its handler and MRU follows submission order", async () => {
    const historyBase = reserveReleasedHistoryBase();
    const firstSubmittedAt = historyBase + 1;
    const secondSubmittedAt = historyBase + 2;
    let resolveFirst!: (scope: LocalCommandHistoryScope) => void;
    let resolveSecond!: (scope: LocalCommandHistoryScope) => void;
    const firstScope = new Promise<LocalCommandHistoryScope>((resolve) => {
      resolveFirst = resolve;
    });
    const secondScope = new Promise<LocalCommandHistoryScope>((resolve) => {
      resolveSecond = resolve;
    });
    const events: string[] = [];
    const reserveSpy = vi
      .spyOn(localCommandHistoryStore, "reserveLastUsedAt")
      .mockImplementationOnce(() => {
        events.push(`reserve:${firstSubmittedAt}`);
        return firstSubmittedAt;
      })
      .mockImplementationOnce(() => {
        events.push(`reserve:${secondSubmittedAt}`);
        return secondSubmittedAt;
      });
    const onCommandSubmit = vi
      .fn()
      .mockImplementationOnce((command: string) => {
        events.push(`submit:${command}`);
        return firstScope;
      })
      .mockImplementationOnce((command: string) => {
        events.push(`submit:${command}`);
        return secondScope;
      });
    const recordSpy = vi.spyOn(localCommandHistoryStore, "record");
    const editorRef: RefObject<Editor | null> = { current: null };

    render(
      <AIChatInput
        editorRef={editorRef}
        localCommandHistoryScope={repoScope}
        onSubmit={vi.fn()}
        onCommandSubmit={onCommandSubmit}
      />,
    );
    const editor = editorRef.current!;

    act(() => {
      editor.commands.insertContent("!first command");
      pressEnter(editor);
      editor.commands.insertContent("!second command");
      pressEnter(editor);
    });

    expect(events).toEqual([
      `reserve:${firstSubmittedAt}`,
      "submit:first command",
      `reserve:${secondSubmittedAt}`,
      "submit:second command",
    ]);
    expect(reserveSpy).toHaveBeenCalledTimes(2);

    await act(async () => {
      resolveSecond(repoScope);
      await secondScope;
      resolveFirst(repoScope);
      await firstScope;
    });

    expect(onCommandSubmit.mock.calls).toEqual([
      ["first command"],
      ["second command"],
    ]);
    expect(recordSpy.mock.calls).toEqual([
      [repoScope, "second command", secondSubmittedAt],
      [repoScope, "first command", firstSubmittedAt],
    ]);

    act(() => {
      editor.commands.insertContent("!");
    });
    const options = await screen.findAllByRole("option");
    expect(options.map((option) => option.textContent)).toEqual([
      "second command",
      "first command",
    ]);
    expect(
      screen.getByRole("button", {
        name: "Clear history for current directory",
      }),
    ).toBeInTheDocument();
  });

  it("Given a dismissed history menu, When the query is unchanged, Then it stays closed until the command body changes", async () => {
    const historyBase = reserveReleasedHistoryBase();
    localCommandHistoryStore.record(repoScope, "git status", historyBase + 10);
    localCommandHistoryStore.record(repoScope, "git stash", historyBase + 20);
    const editorRef: RefObject<Editor | null> = { current: null };

    render(
      <AIChatInput
        editorRef={editorRef}
        localCommandHistoryScope={repoScope}
        onSubmit={vi.fn()}
        onCommandSubmit={vi.fn()}
      />,
    );
    const editor = editorRef.current!;

    act(() => {
      editor.commands.insertContent("!git");
    });
    await screen.findByRole("listbox", { name: "Shell command history" });

    act(() => {
      pressKey(editor, "Escape");
    });
    expect(editor.getText()).toBe("!git");
    expect(screen.queryByRole("listbox")).not.toBeInTheDocument();

    act(() => {
      editor.commands.setTextSelection(1);
      editor.commands.setTextSelection(editor.state.doc.content.size - 1);
    });
    expect(screen.queryByRole("listbox")).not.toBeInTheDocument();

    act(() => {
      editor.commands.insertContent(" st");
    });
    expect(
      await screen.findByRole("option", { name: "git status" }),
    ).toBeInTheDocument();
  });

  it("Given scope-specific history, When the active device/cwd scope changes, Then the open menu switches immediately without mixing rows", async () => {
    const historyBase = reserveReleasedHistoryBase();
    localCommandHistoryStore.record(
      repoScope,
      "repo command",
      historyBase + 10,
    );
    localCommandHistoryStore.record(
      otherScope,
      "other command",
      historyBase + 20,
    );
    const editorRef: RefObject<Editor | null> = { current: null };

    const { rerender } = render(
      <AIChatInput
        editorRef={editorRef}
        localCommandHistoryScope={repoScope}
        onSubmit={vi.fn()}
        onCommandSubmit={vi.fn()}
      />,
    );

    act(() => {
      editorRef.current!.commands.insertContent("!");
    });
    expect(
      await screen.findByRole("option", { name: "repo command" }),
    ).toBeInTheDocument();

    rerender(
      <AIChatInput
        editorRef={editorRef}
        localCommandHistoryScope={otherScope}
        onSubmit={vi.fn()}
        onCommandSubmit={vi.fn()}
      />,
    );

    expect(
      await screen.findByRole("option", { name: "other command" }),
    ).toBeInTheDocument();
    expect(screen.queryByText("repo command")).not.toBeInTheDocument();

    act(() => {
      localCommandHistoryStore.record(
        repoScope,
        "stale repo command",
        historyBase + 30,
      );
    });
    expect(screen.queryByText("stale repo command")).not.toBeInTheDocument();
    expect(
      screen.getByRole("option", { name: "other command" }),
    ).toBeInTheDocument();

    act(() => {
      localCommandHistoryStore.record(
        otherScope,
        "new other command",
        historyBase + 40,
      );
    });
    expect(
      screen.getAllByRole("option").map((option) => option.textContent),
    ).toEqual(["new other command", "other command"]);
  });

  it("Given two mounted inputs share one history scope while another scope is open, When one input clears history, Then both shared menus close before a cleared row can be selected and the other menu remains", async () => {
    const historyBase = reserveReleasedHistoryBase();
    localCommandHistoryStore.record(repoScope, "git status", historyBase + 20);
    localCommandHistoryStore.record(
      otherScope,
      "other command",
      historyBase + 10,
    );
    const firstRepoEditorRef: RefObject<Editor | null> = { current: null };
    const secondRepoEditorRef: RefObject<Editor | null> = { current: null };
    const otherEditorRef: RefObject<Editor | null> = { current: null };
    const firstRepoSubmit = vi.fn();
    const secondRepoSubmit = vi.fn();

    render(
      <>
        <div data-testid="first-shared-history-input">
          <AIChatInput
            editorRef={firstRepoEditorRef}
            localCommandHistoryScope={repoScope}
            onSubmit={vi.fn()}
            onCommandSubmit={firstRepoSubmit}
          />
        </div>
        <div data-testid="second-shared-history-input">
          <AIChatInput
            editorRef={secondRepoEditorRef}
            localCommandHistoryScope={repoScope}
            onSubmit={vi.fn()}
            onCommandSubmit={secondRepoSubmit}
          />
        </div>
        <div data-testid="other-history-input">
          <AIChatInput
            editorRef={otherEditorRef}
            localCommandHistoryScope={otherScope}
            onSubmit={vi.fn()}
            onCommandSubmit={vi.fn()}
          />
        </div>
      </>,
    );
    const firstInput = within(screen.getByTestId("first-shared-history-input"));
    const secondInput = within(
      screen.getByTestId("second-shared-history-input"),
    );
    const otherInput = within(screen.getByTestId("other-history-input"));

    act(() => {
      firstRepoEditorRef.current!.commands.insertContent("!");
      secondRepoEditorRef.current!.commands.insertContent("!");
      otherEditorRef.current!.commands.insertContent("!");
    });

    await vi.waitFor(() => {
      expect(screen.getAllByRole("listbox")).toHaveLength(3);
    });
    const menuFor = (input: ReturnType<typeof within>) => {
      const listboxId = input
        .getByRole("combobox")
        .getAttribute("aria-controls");
      const listbox = document.getElementById(listboxId!);
      expect(listbox).not.toBeNull();
      return within(listbox!.parentElement!);
    };
    const firstMenu = menuFor(firstInput);
    const secondMenu = menuFor(secondInput);
    const otherMenu = menuFor(otherInput);
    expect(
      firstMenu.getByRole("option", { name: "git status" }),
    ).toBeInTheDocument();
    expect(
      secondMenu.getByRole("option", { name: "git status" }),
    ).toBeInTheDocument();
    expect(
      otherMenu.getByRole("option", { name: "other command" }),
    ).toBeInTheDocument();

    fireEvent.click(
      firstMenu.getByRole("button", {
        name: "Clear history for current directory",
      }),
    );

    expect(screen.getAllByRole("listbox")).toHaveLength(1);
    expect(
      otherMenu.getByRole("option", { name: "other command" }),
    ).toBeInTheDocument();
    expect(localCommandHistoryStore.list(repoScope)).toEqual([]);
    expect(localCommandHistoryStore.list(otherScope)).toEqual([
      { command: "other command", lastUsedAt: historyBase + 10 },
    ]);

    act(() => {
      pressKey(secondRepoEditorRef.current!, "Tab");
    });
    expect(secondRepoEditorRef.current!.getText()).toBe("!");
    expect(firstRepoSubmit).not.toHaveBeenCalled();
    expect(secondRepoSubmit).not.toHaveBeenCalled();
  });

  it("Given a rejected command handler promise, When a nonempty command is submitted, Then no history is recorded and the rejection is consumed", async () => {
    const editorRef: RefObject<Editor | null> = { current: null };
    const rejection = new Error("terminal rpc failed");
    const onCommandSubmit = vi.fn().mockRejectedValue(rejection);
    const recordSpy = vi.spyOn(localCommandHistoryStore, "record");
    const warnSpy = vi.spyOn(console, "warn").mockImplementation(() => {});

    render(
      <AIChatInput
        editorRef={editorRef}
        localCommandHistoryScope={repoScope}
        onSubmit={vi.fn()}
        onCommandSubmit={onCommandSubmit}
      />,
    );

    act(() => {
      editorRef.current!.commands.insertContent("!pwd");
      pressEnter(editorRef.current!);
    });

    await vi.waitFor(() => {
      expect(warnSpy).toHaveBeenCalledWith(
        "[chat-input] local command submission failed",
        rejection,
      );
    });
    expect(recordSpy).not.toHaveBeenCalled();
    expect(editorRef.current!.getText()).toBe("");
  });

  it("Given history order reservation fails, When a nonempty command is submitted, Then execution still runs exactly once and the history failure is consumed", async () => {
    const editorRef: RefObject<Editor | null> = { current: null };
    const reservationFailure = new RangeError("timestamp budget exhausted");
    const onCommandSubmit = vi.fn().mockResolvedValue(repoScope);
    vi.spyOn(localCommandHistoryStore, "reserveLastUsedAt").mockImplementation(
      () => {
        throw reservationFailure;
      },
    );
    const recordSpy = vi.spyOn(localCommandHistoryStore, "record");
    const warnSpy = vi.spyOn(console, "warn").mockImplementation(() => {});
    const unhandledRejection = vi.fn();
    window.addEventListener("unhandledrejection", unhandledRejection);
    onTestFinished(() =>
      window.removeEventListener("unhandledrejection", unhandledRejection),
    );

    render(
      <AIChatInput
        editorRef={editorRef}
        localCommandHistoryScope={repoScope}
        onSubmit={vi.fn()}
        onCommandSubmit={onCommandSubmit}
      />,
    );

    expect(() => {
      act(() => {
        editorRef.current!.commands.insertContent("!pwd");
        pressEnter(editorRef.current!);
      });
    }).not.toThrow();
    await act(async () => {
      await Promise.resolve();
      await Promise.resolve();
    });

    expect(onCommandSubmit).toHaveBeenCalledTimes(1);
    expect(onCommandSubmit).toHaveBeenCalledWith("pwd");
    expect(recordSpy).not.toHaveBeenCalled();
    expect(warnSpy).toHaveBeenCalledWith(
      "[chat-input] failed to reserve local command history order",
      reservationFailure,
    );
    expect(unhandledRejection).not.toHaveBeenCalled();
    expect(editorRef.current!.getText()).toBe("");
  });

  it("Given history reads fail, When a command is entered and submitted, Then the menu stays unavailable without blocking execution", () => {
    const editorRef: RefObject<Editor | null> = { current: null };
    const onCommandSubmit = vi.fn();
    vi.spyOn(localCommandHistoryStore, "list").mockImplementation(() => {
      throw new Error("history read failed");
    });
    vi.spyOn(console, "warn").mockImplementation(() => {});

    render(
      <AIChatInput
        editorRef={editorRef}
        localCommandHistoryScope={repoScope}
        onSubmit={vi.fn()}
        onCommandSubmit={onCommandSubmit}
      />,
    );

    act(() => {
      editorRef.current!.commands.insertContent("!pwd");
    });
    expect(screen.queryByRole("listbox")).not.toBeInTheDocument();

    act(() => {
      pressEnter(editorRef.current!);
    });
    expect(onCommandSubmit).toHaveBeenCalledWith("pwd");
  });

  it("Given scoped history and a focused Clear action, when durable deletion fails, then a privacy-safe toast appears while the menu, draft, history, and focus remain intact", async () => {
    const historyBase = reserveReleasedHistoryBase();
    localCommandHistoryStore.record(repoScope, "git status", historyBase + 20);
    const clearSpy = vi
      .spyOn(localCommandHistoryStore, "clear")
      .mockReturnValue(false);
    const editorRef: RefObject<Editor | null> = { current: null };
    const user = userEvent.setup();

    render(
      <AIChatInput
        editorRef={editorRef}
        localCommandHistoryScope={repoScope}
        onSubmit={vi.fn()}
        onCommandSubmit={vi.fn()}
      />,
    );
    const editor = editorRef.current!;

    act(() => {
      editor.commands.insertContent("!git");
      editor.commands.focus("end");
    });
    await vi.waitFor(() => expect(screen.getByRole("combobox")).toHaveFocus());
    await screen.findByRole("option", { name: "git status" });
    const clearButton = screen.getByRole("button", {
      name: "Clear history for current directory",
    });
    act(() => pressKey(editor, "ArrowUp"));
    expect(clearButton).toHaveFocus();

    await user.keyboard("{Enter}");

    expect(clearSpy).toHaveBeenCalledWith(repoScope);
    expect(sonnerMocks.toast.error).toHaveBeenCalledWith(
      "Couldn’t clear command history. Try again.",
    );
    expect(editor.getText()).toBe("!git");
    expect(clearButton).toHaveFocus();
    expect(
      screen.getByRole("listbox", { name: "Shell command history" }),
    ).toBeInTheDocument();
    expect(
      screen.getByRole("option", { name: "git status" }),
    ).toBeInTheDocument();
    expect(localCommandHistoryStore.list(repoScope)).toEqual([
      { command: "git status", lastUsedAt: historyBase + 20 },
    ]);
  });

  it.each([
    { activationKey: "Enter", userInput: "{Enter}" },
    { activationKey: "Space", userInput: " " },
  ])(
    "Given scoped history and an editor draft, When arrow wrap focuses Clear and native $activationKey activates it, Then only that history is cleared without submitting",
    async ({ userInput }) => {
      const historyBase = reserveReleasedHistoryBase();
      localCommandHistoryStore.record(
        repoScope,
        "git status",
        historyBase + 30,
      );
      localCommandHistoryStore.record(repoScope, "git stash", historyBase + 20);
      localCommandHistoryStore.record(
        otherScope,
        "other command",
        historyBase + 10,
      );
      const editorRef: RefObject<Editor | null> = { current: null };
      const onCommandSubmit = vi.fn();
      const onSubmit = vi.fn();
      const user = userEvent.setup();

      render(
        <AIChatInput
          editorRef={editorRef}
          localCommandHistoryScope={repoScope}
          onSubmit={onSubmit}
          onCommandSubmit={onCommandSubmit}
        />,
      );
      const editor = editorRef.current!;

      act(() => {
        editor.commands.insertContent("!git");
        editor.commands.focus("end");
      });
      await vi.waitFor(() =>
        expect(screen.getByRole("combobox")).toHaveFocus(),
      );
      await screen.findByRole("option", { name: "git status" });
      const clearButton = screen.getByRole("button", {
        name: "Clear history for current directory",
      });

      act(() => pressKey(editor, "ArrowUp"));
      expect(clearButton).toHaveFocus();
      expect(screen.getByRole("combobox")).not.toHaveAttribute(
        "aria-activedescendant",
      );

      await user.keyboard(userInput);

      expect(editor.getText()).toBe("!git");
      expect(screen.getByRole("textbox")).toHaveFocus();
      expect(onCommandSubmit).not.toHaveBeenCalled();
      expect(onSubmit).not.toHaveBeenCalled();
      expect(localCommandHistoryStore.list(repoScope)).toEqual([]);
      expect(localCommandHistoryStore.list(otherScope)).toEqual([
        { command: "other command", lastUsedAt: historyBase + 10 },
      ]);
      expect(screen.queryByRole("listbox")).not.toBeInTheDocument();
      expect(sonnerMocks.toast.error).not.toHaveBeenCalled();

      act(() => {
        editor.commands.insertContent("x");
      });
      expect(screen.queryByRole("listbox")).not.toBeInTheDocument();
    },
  );

  it("Given footer Clear is focused, When Shift+Tab or Tab is pressed, Then native focus moves without clearing, filling, or submitting the draft", async () => {
    const historyBase = reserveReleasedHistoryBase();
    localCommandHistoryStore.record(repoScope, "git status", historyBase + 30);
    const editorRef: RefObject<Editor | null> = { current: null };
    const onCommandSubmit = vi.fn();
    const onSubmit = vi.fn();
    const user = userEvent.setup();

    render(
      <>
        <AIChatInput
          editorRef={editorRef}
          localCommandHistoryScope={repoScope}
          onSubmit={onSubmit}
          onCommandSubmit={onCommandSubmit}
        />
        <button type="button">After composer</button>
      </>,
    );
    const editor = editorRef.current!;

    act(() => {
      editor.commands.insertContent("!git");
      editor.commands.focus("end");
    });
    await vi.waitFor(() => expect(screen.getByRole("combobox")).toHaveFocus());
    await screen.findByRole("listbox", { name: "Shell command history" });

    const firstOption = screen.getByRole("option", { name: "git status" });
    const clearButton = screen.getByRole("button", {
      name: "Clear history for current directory",
    });
    const combobox = screen.getByRole("combobox");

    act(() => pressKey(editor, "ArrowUp"));
    expect(clearButton).toHaveFocus();

    await user.tab({ shift: true });

    expect(combobox).toHaveFocus();
    expect(combobox).toHaveAttribute("aria-activedescendant", firstOption.id);
    expect(firstOption).toHaveAttribute("aria-selected", "true");
    expect(localCommandHistoryStore.list(repoScope)).toHaveLength(1);

    act(() => pressKey(editor, "ArrowUp"));
    expect(clearButton).toHaveFocus();
    await user.tab();

    expect(
      screen.getByRole("button", { name: "After composer" }),
    ).toHaveFocus();
    expect(editor.getText()).toBe("!git");
    expect(onCommandSubmit).not.toHaveBeenCalled();
    expect(onSubmit).not.toHaveBeenCalled();
    expect(localCommandHistoryStore.list(repoScope)).toEqual([
      { command: "git status", lastUsedAt: historyBase + 30 },
    ]);
    expect(screen.queryByRole("listbox")).not.toBeInTheDocument();
  });

  it("Given a long command and transient output, When history is hovered, picked, or cleared, Then dynamic text stays complete and clearing preserves the draft and output card", async () => {
    const historyBase = reserveReleasedHistoryBase();
    const longCommand = `printf '${"x".repeat(180)}'`;
    localCommandHistoryStore.record(repoScope, longCommand, historyBase + 30);
    localCommandHistoryStore.record(repoScope, "git status", historyBase + 20);
    localCommandHistoryStore.record(
      otherScope,
      "other command",
      historyBase + 10,
    );
    useLocalCommandsStore.getState().start({
      id: "running-command",
      sessionId: 42,
      command: "sleep 10",
      createdAt: 1,
    });
    const editorRef: RefObject<Editor | null> = { current: null };
    const onCommandSubmit = vi.fn();

    render(
      <AIChatInput
        editorRef={editorRef}
        localCommandHistoryScope={repoScope}
        onSubmit={vi.fn()}
        onCommandSubmit={onCommandSubmit}
      />,
    );
    const editor = editorRef.current!;

    act(() => {
      editor.commands.insertContent("!");
    });
    const longOption = await screen.findByRole("option", {
      name: longCommand,
    });
    expect(longOption.querySelector("span")).toHaveClass("truncate");

    const statusOption = screen.getByRole("option", { name: "git status" });
    fireEvent.mouseMove(statusOption);
    expect(statusOption).toHaveAttribute("aria-selected", "true");
    fireEvent.mouseDown(statusOption);
    expect(editor.getText()).toBe("!git status");
    expect(onCommandSubmit).not.toHaveBeenCalled();

    act(() => {
      editor.commands.insertContent(" ");
    });
    const historyMenu = await screen.findByRole("listbox", {
      name: "Shell command history",
    });
    const clearButton = screen.getByRole("button", {
      name: "Clear history for current directory",
    });
    expect(historyMenu).not.toContainElement(clearButton);
    fireEvent.click(clearButton);

    expect(editor.getText()).toBe("!git status ");
    expect(localCommandHistoryStore.list(repoScope)).toEqual([]);
    expect(localCommandHistoryStore.list(otherScope)).toEqual([
      { command: "other command", lastUsedAt: historyBase + 10 },
    ]);
    expect(
      useLocalCommandsStore.getState().get("running-command"),
    ).toMatchObject({
      command: "sleep 10",
      output: "",
      status: "running",
    });
    expect(screen.queryByRole("listbox")).not.toBeInTheDocument();
  });

  it("Given an open command history menu, When the user pointer-downs outside the editor, Then the menu closes", async () => {
    const historyBase = reserveReleasedHistoryBase();
    localCommandHistoryStore.record(repoScope, "git status", historyBase + 10);
    const editorRef: RefObject<Editor | null> = { current: null };

    render(
      <>
        <AIChatInput
          editorRef={editorRef}
          localCommandHistoryScope={repoScope}
          onSubmit={vi.fn()}
          onCommandSubmit={vi.fn()}
        />
        <button type="button" data-testid="outside">
          outside
        </button>
      </>,
    );

    act(() => {
      editorRef.current!.commands.insertContent("!git");
    });
    await screen.findByRole("listbox", { name: "Shell command history" });

    fireEvent.pointerDown(screen.getByTestId("outside"));

    await vi.waitFor(() => expect(screen.queryByRole("listbox")).toBeNull());
  });
});
