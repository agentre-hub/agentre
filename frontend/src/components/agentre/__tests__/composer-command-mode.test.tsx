import { act, render as renderBare, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import type { ReactElement, ReactNode, RefObject } from "react";
import { beforeEach, describe, expect, it, vi } from "vitest";

import type { Editor } from "@tiptap/react";

import {
  LocalCommandHistoryProvider,
  type LocalCommandHistoryScope,
} from "@agentre-ai/agentre-ui";
import { localCommandHistoryStore } from "@/stores/local-command-history-store";

// chat.tsx uses wailsjs runtime (OnFileDrop/OnFileDropOff via useFileDropZone)
vi.mock("../../../../wailsjs/runtime/runtime", async () => {
  const actual = await vi.importActual<
    typeof import("../../../../wailsjs/runtime/runtime")
  >("../../../../wailsjs/runtime/runtime");
  return {
    ...actual,
    OnFileDrop: vi.fn(),
    OnFileDropOff: vi.fn(),
  };
});

import { ChatComposer } from "../chat";
import { desktopLocalCommandHistoryAccess } from "../local-command-history-access-desktop";

/**
 * ! Shell 历史是可选的宿主能力：桌面端挂上 Provider，composer 才渲染历史弹层
 * 并把命令记进 localStorage store。
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

const historyScope: LocalCommandHistoryScope = {
  deviceId: "composer-device",
  cwd: "/composer/repo",
};
const newRemoteProjectScope: LocalCommandHistoryScope = {
  deviceId: "7",
  cwd: "/local/repo",
};
const resolvedRemoteProjectScope: LocalCommandHistoryScope = {
  deviceId: "7",
  cwd: "/home/me/proj",
};

beforeEach(() => {
  vi.restoreAllMocks();
  localCommandHistoryStore.clear(historyScope);
  localCommandHistoryStore.clear(newRemoteProjectScope);
  localCommandHistoryStore.clear(resolvedRemoteProjectScope);
});

function pressKey(editor: Editor, key: string, init: KeyboardEventInit = {}) {
  editor.view.dom.dispatchEvent(
    new KeyboardEvent("keydown", {
      key,
      bubbles: true,
      cancelable: true,
      ...init,
    }),
  );
}

function pressEnter(editor: Editor) {
  pressKey(editor, "Enter");
}

describe("ChatComposer command mode", () => {
  it("shows command-mode banner when input starts with !", async () => {
    const editorRef: RefObject<Editor | null> = { current: null };
    const onRunCommand = vi.fn();

    render(
      <ChatComposer
        editorRef={editorRef}
        onSubmit={() => undefined}
        onCommandSubmit={onRunCommand}
      />,
    );

    // Wait for the editor to mount
    await screen.findByRole("textbox");

    act(() => {
      editorRef.current!.commands.insertContent("!ls");
    });

    expect(screen.getByText(/命令模式|Command mode/)).toBeInTheDocument();
  });

  it("run button replaces send button in command mode", async () => {
    const editorRef: RefObject<Editor | null> = { current: null };

    render(
      <ChatComposer
        editorRef={editorRef}
        onSubmit={() => undefined}
        onCommandSubmit={vi.fn()}
      />,
    );

    await screen.findByRole("textbox");

    // Initially: normal Send button present
    expect(screen.getByRole("button", { name: "Send" })).toBeInTheDocument();

    act(() => {
      editorRef.current!.commands.insertContent("!echo hi");
    });

    // In command mode: Run button should be present, Send should not
    expect(
      screen.getByRole("button", { name: /运行命令|Run command/i }),
    ).toBeInTheDocument();
    expect(
      screen.queryByRole("button", { name: "Send" }),
    ).not.toBeInTheDocument();
  });

  it("banner disappears when leading ! is removed (cleared)", async () => {
    const editorRef: RefObject<Editor | null> = { current: null };

    render(
      <ChatComposer
        editorRef={editorRef}
        onSubmit={() => undefined}
        onCommandSubmit={vi.fn()}
      />,
    );

    await screen.findByRole("textbox");

    act(() => {
      editorRef.current!.commands.insertContent("!cmd");
    });
    expect(screen.getByText(/命令模式|Command mode/)).toBeInTheDocument();

    act(() => {
      editorRef.current!.commands.clearContent();
    });

    expect(screen.queryByText(/命令模式|Command mode/)).not.toBeInTheDocument();
  });

  it("Given an execution scope, When ! mode opens, Then ChatComposer passes that scope to the history menu", async () => {
    const historyBase = localCommandHistoryStore.reserveLastUsedAt();
    localCommandHistoryStore.record(
      historyScope,
      "pnpm test",
      historyBase + 10,
    );
    const editorRef: RefObject<Editor | null> = { current: null };

    render(
      <ChatComposer
        editorRef={editorRef}
        localCommandHistoryScope={historyScope}
        onSubmit={() => undefined}
        onCommandSubmit={vi.fn()}
      />,
    );

    await screen.findByRole("textbox");
    act(() => {
      editorRef.current!.commands.insertContent("!");
    });

    expect(
      await screen.findByRole("option", { name: "pnpm test" }),
    ).toBeInTheDocument();
  });

  it("Given open history in ChatComposer, When Shift+Tab is pressed from the focused combobox, Then the draft, selection, highlighted row, and history stay intact while permission cycles exactly once", async () => {
    const historyBase = localCommandHistoryStore.reserveLastUsedAt();
    localCommandHistoryStore.record(
      historyScope,
      "pnpm test --filter composer",
      historyBase + 20,
    );
    localCommandHistoryStore.record(
      historyScope,
      "pnpm test",
      historyBase + 10,
    );
    const editorRef: RefObject<Editor | null> = { current: null };
    const onSubmit = vi.fn();
    const onRunCommand = vi.fn();
    const onShiftTab = vi.fn();

    render(
      <ChatComposer
        editorRef={editorRef}
        localCommandHistoryScope={historyScope}
        onSubmit={onSubmit}
        onCommandSubmit={onRunCommand}
        onShiftTab={onShiftTab}
      />,
    );

    await screen.findByRole("textbox");
    act(() => {
      editorRef.current!.commands.insertContent("!pnpm");
      editorRef.current!.commands.focus("end");
    });
    await screen.findByRole("option", {
      name: "pnpm test --filter composer",
    });
    act(() => pressKey(editorRef.current!, "ArrowDown"));
    const highlightedOption = screen.getByRole("option", {
      name: "pnpm test",
    });
    expect(highlightedOption).toHaveAttribute("aria-selected", "true");
    const selectionBefore = {
      from: editorRef.current!.state.selection.from,
      to: editorRef.current!.state.selection.to,
    };

    act(() => pressKey(editorRef.current!, "Tab", { shiftKey: true }));

    expect(onShiftTab).toHaveBeenCalledTimes(1);
    expect(editorRef.current!.getText()).toBe("!pnpm");
    expect(editorRef.current!.state.selection).toMatchObject(selectionBefore);
    expect(highlightedOption).toHaveAttribute("aria-selected", "true");
    expect(
      screen.getByRole("listbox", { name: "Shell command history" }),
    ).toBeInTheDocument();
    expect(localCommandHistoryStore.list(historyScope)).toEqual([
      {
        command: "pnpm test --filter composer",
        lastUsedAt: historyBase + 20,
      },
      { command: "pnpm test", lastUsedAt: historyBase + 10 },
    ]);
    expect(onRunCommand).not.toHaveBeenCalled();
    expect(onSubmit).not.toHaveBeenCalled();
  });

  it("Given a child control consumes Shift+Tab, When the event bubbles through ChatComposer, Then permission cycling is not invoked", async () => {
    const editorRef: RefObject<Editor | null> = { current: null };
    const onShiftTab = vi.fn();

    render(
      <ChatComposer
        editorRef={editorRef}
        onSubmit={() => undefined}
        onShiftTab={onShiftTab}
        leadingControls={
          <button
            type="button"
            data-testid="consuming-control"
            onKeyDown={(event) => event.preventDefault()}
          >
            Mode
          </button>
        }
      />,
    );

    const consumingControl = await screen.findByTestId("consuming-control");
    consumingControl.focus();
    act(() => {
      consumingControl.dispatchEvent(
        new KeyboardEvent("keydown", {
          key: "Tab",
          shiftKey: true,
          bubbles: true,
          cancelable: true,
        }),
      );
    });

    expect(onShiftTab).not.toHaveBeenCalled();
    expect(consumingControl).toHaveFocus();
  });

  it("Given ChatComposer history Clear is keyboard-focused, When Shift+Tab is pressed, Then native reverse focus bypasses permission cycling", async () => {
    const historyBase = localCommandHistoryStore.reserveLastUsedAt();
    const lastUsedAt = historyBase + 10;
    localCommandHistoryStore.record(historyScope, "pnpm test", lastUsedAt);
    const editorRef: RefObject<Editor | null> = { current: null };
    const onShiftTab = vi.fn();
    const user = userEvent.setup();

    render(
      <ChatComposer
        editorRef={editorRef}
        localCommandHistoryScope={historyScope}
        onSubmit={() => undefined}
        onCommandSubmit={vi.fn()}
        onShiftTab={onShiftTab}
      />,
    );

    await screen.findByRole("textbox");
    act(() => {
      editorRef.current!.commands.insertContent("!");
      editorRef.current!.commands.focus("end");
    });
    await screen.findByRole("option", { name: "pnpm test" });

    act(() => pressKey(editorRef.current!, "ArrowUp"));
    const clearButton = document.querySelector<HTMLButtonElement>(
      "[data-local-command-history-clear]",
    );
    expect(clearButton).not.toBeNull();
    expect(clearButton).toHaveFocus();

    await user.tab({ shift: true });

    expect(onShiftTab).not.toHaveBeenCalled();
    expect(screen.getByRole("combobox")).toHaveFocus();
    expect(localCommandHistoryStore.list(historyScope)).toEqual([
      { command: "pnpm test", lastUsedAt },
    ]);
  });

  it("Given Shift+Tab starts from the editor or an ordinary composer control, When permission cycling is enabled, Then ChatComposer still cycles mode", async () => {
    const editorRef: RefObject<Editor | null> = { current: null };
    const onShiftTab = vi.fn();
    const user = userEvent.setup();

    render(
      <ChatComposer
        editorRef={editorRef}
        onSubmit={() => undefined}
        onShiftTab={onShiftTab}
        leadingControls={
          <button type="button" data-testid="permission-control">
            Mode
          </button>
        }
        supportsImageInput={false}
      />,
    );

    await screen.findByRole("textbox");
    act(() => editorRef.current!.commands.focus("end"));
    await user.tab({ shift: true });
    expect(onShiftTab).toHaveBeenCalledTimes(1);
    expect(screen.getByRole("textbox")).toHaveFocus();

    const permissionControl = screen.getByTestId("permission-control");
    permissionControl.focus();
    await user.tab({ shift: true });
    expect(onShiftTab).toHaveBeenCalledTimes(2);
    expect(permissionControl).toHaveFocus();
  });

  it("Given a new remote project chat, When command execution resolves its scope, Then history records the resolved execution cwd instead of the local project path", async () => {
    const editorRef: RefObject<Editor | null> = { current: null };
    const onRunCommand = vi.fn().mockResolvedValue(resolvedRemoteProjectScope);

    render(
      <ChatComposer
        editorRef={editorRef}
        localCommandHistoryScope={newRemoteProjectScope}
        onSubmit={() => undefined}
        onCommandSubmit={onRunCommand}
      />,
    );

    await screen.findByRole("textbox");
    act(() => {
      editorRef.current!.commands.insertContent("!pwd");
      pressEnter(editorRef.current!);
    });

    expect(onRunCommand).toHaveBeenCalledWith("pwd");
    await vi.waitFor(() => {
      expect(localCommandHistoryStore.list(resolvedRemoteProjectScope)).toEqual(
        [expect.objectContaining({ command: "pwd" })],
      );
    });
    expect(localCommandHistoryStore.list(newRemoteProjectScope)).toEqual([]);
  });

  it("Given history persistence fails, When a command is submitted, Then execution still starts", async () => {
    const editorRef: RefObject<Editor | null> = { current: null };
    const submittedAt = 1_000;
    vi.spyOn(localCommandHistoryStore, "reserveLastUsedAt").mockReturnValue(
      submittedAt,
    );
    const onRunCommand = vi.fn().mockReturnValue(resolvedRemoteProjectScope);
    const recordSpy = vi
      .spyOn(localCommandHistoryStore, "record")
      .mockImplementation(() => {
        throw new Error("storage failed");
      });

    render(
      <ChatComposer
        editorRef={editorRef}
        localCommandHistoryScope={newRemoteProjectScope}
        onSubmit={() => undefined}
        onCommandSubmit={onRunCommand}
      />,
    );

    await screen.findByRole("textbox");
    act(() => {
      editorRef.current!.commands.insertContent("!pwd");
      pressEnter(editorRef.current!);
    });

    expect(onRunCommand).toHaveBeenCalledWith("pwd");
    await vi.waitFor(() => {
      expect(recordSpy).toHaveBeenCalledWith(
        resolvedRemoteProjectScope,
        "pwd",
        submittedAt,
      );
    });
  });
});
