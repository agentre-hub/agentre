import "@testing-library/jest-dom/vitest";
import {
  act,
  fireEvent,
  render,
  screen,
  waitFor,
} from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { createRef, useLayoutEffect, type RefObject } from "react";
import { describe, expect, it, vi } from "vitest";
import type { Editor } from "@tiptap/react";

import { ChatComposer, type ChatComposerHandle } from "./chat-composer";
import type { AIChatInputHandle } from "../chat-input";
import type { DroppedImageItem } from "../chat-input/drop";
import type { DropZoneRegistrar } from "../chat-input/use-file-drop";

/**
 * ChatComposer 是两端唯一那份输入框外壳：编辑模式、命令模式、拖入、草稿句柄与整条
 * 底栏都在这里，宿主只接能力。
 *
 * 行为规格跟着被守卫的代码住在包里。
 */
describe("ChatComposer", () => {
  it("Given image input is supported, When a PNG is selected and submitted, Then the host receives text and the image data URL", async () => {
    const onSubmit = vi.fn();
    render(<ChatComposer supportsImageInput onSubmit={onSubmit} />);

    await attachPng("shot.png");
    fireEvent.click(sendButton());

    await waitFor(() =>
      expect(onSubmit).toHaveBeenCalledWith({
        images: [
          expect.objectContaining({
            dataUrl: expect.stringMatching(/^data:image\/png;base64,/),
            mediaType: "image/png",
            name: "shot.png",
          }),
        ],
        text: "",
      }),
    );
  });

  it("Given image input is unavailable, When rendered, Then the image affordance is absent", () => {
    render(<ChatComposer supportsImageInput={false} onSubmit={vi.fn()} />);
    expect(screen.queryByLabelText("Add image")).toBeNull();
  });

  it("Given a host input handle, When it restores a draft, Then the shared composer submits that draft", async () => {
    const inputHandle = createRef<AIChatInputHandle>();
    const onSubmit = vi.fn();
    render(<ChatComposer inputHandleRef={inputHandle} onSubmit={onSubmit} />);

    inputHandle.current?.loadDraft("restored");
    await waitFor(() => expect(sendButton()).toBeEnabled());
    fireEvent.click(sendButton());

    await waitFor(() =>
      expect(onSubmit).toHaveBeenCalledWith({ text: "restored" }),
    );
  });

  it("Given attached images and an empty editor, When Enter is pressed, Then the images are sent on their own", async () => {
    const onSubmit = vi.fn();
    const { container } = render(
      <ChatComposer supportsImageInput onSubmit={onSubmit} />,
    );

    await attachPng("only-image.png");
    fireEvent.keyDown(formOf(container), { key: "Enter" });

    await waitFor(() =>
      expect(onSubmit).toHaveBeenCalledWith(
        expect.objectContaining({ text: "" }),
      ),
    );
  });

  it("Given more images than the limit, When they are selected, Then none are attached and the reason is announced", async () => {
    render(<ChatComposer supportsImageInput onSubmit={vi.fn()} />);

    fireEvent.change(imageInput(), {
      target: {
        files: [1, 2, 3, 4, 5].map((n) => pngFile(`shot-${n}.png`)),
      },
    });

    expect(await screen.findByRole("alert")).toHaveTextContent(
      "You can attach up to 4 images",
    );
    expect(screen.queryByAltText("shot-1.png")).toBeNull();
  });

  it("Given a file of the wrong type, When it is selected, Then the accepted formats are stated", async () => {
    render(<ChatComposer supportsImageInput onSubmit={vi.fn()} />);

    fireEvent.change(imageInput(), {
      target: {
        files: [
          new File([new Uint8Array([1])], "a.gif", { type: "image/gif" }),
        ],
      },
    });

    expect(await screen.findByRole("alert")).toHaveTextContent(
      "Use PNG, JPEG, or WebP images up to 5 MB",
    );
  });
});

describe("ChatComposer · sending", () => {
  it("Given a send is in flight, When rendered, Then the submit control says so and is disabled", async () => {
    const inputHandle = createRef<AIChatInputHandle>();
    render(
      <ChatComposer inputHandleRef={inputHandle} sending onSubmit={vi.fn()} />,
    );

    inputHandle.current?.loadDraft("hi");
    await waitFor(() =>
      expect(screen.getByRole("button", { name: "Sending…" })).toBeDisabled(),
    );
  });
});

describe("ChatComposer · running turn", () => {
  it("Given a turn is running and the draft is empty, When the submit slot is read, Then it is a stop control that stops the turn", async () => {
    const onStop = vi.fn();
    render(<ChatComposer running onStop={onStop} onSubmit={vi.fn()} />);

    const stop = screen.getByRole("button", { name: "Stop" });
    expect(stop).toHaveAttribute("title", "Stop current turn (Esc)");
    expect(stop).toBeEnabled();
    expect(screen.queryByRole("button", { name: "Send" })).toBeNull();

    fireEvent.click(stop);
    expect(onStop).toHaveBeenCalledTimes(1);
  });

  it("Given a turn is running and there is a draft, When the submit slot is read, Then stop and send sit side by side and send says it inserts after the AI", async () => {
    const inputHandle = createRef<AIChatInputHandle>();
    const onSubmit = vi.fn();
    const { container } = render(
      <ChatComposer
        inputHandleRef={inputHandle}
        running
        onStop={vi.fn()}
        onSubmit={onSubmit}
      />,
    );

    inputHandle.current?.loadDraft("also handle the empty list");
    const send = await screen.findByRole("button", { name: "Send" });
    await waitFor(() => expect(send).toBeEnabled());
    expect(send).toHaveAttribute("title", "Insert after AI finishes (Enter)");
    const stop = screen.getByRole("button", { name: "Stop" });

    // 两颗都贴在底栏最右，停止在左、发送在最末。
    const bar = container.querySelector("[data-slot='composer-bar']");
    const buttons: Element[] = Array.from(
      bar?.querySelectorAll("button") ?? [],
    );
    expect(buttons.indexOf(stop)).toBe(buttons.length - 2);
    expect(buttons.indexOf(send)).toBe(buttons.length - 1);

    fireEvent.click(send);
    await waitFor(() =>
      expect(onSubmit).toHaveBeenCalledWith({
        text: "also handle the empty list",
      }),
    );
  });

  it("Given stop was pressed, When the turn has not ended yet, Then the stop control spins and is disabled", async () => {
    const onStop = vi.fn();
    render(
      <ChatComposer running stopping onStop={onStop} onSubmit={vi.fn()} />,
    );

    const stop = screen.getByRole("button", { name: "Stopping…" });
    expect(stop).toBeDisabled();
    expect(stop.querySelector(".animate-spin")).not.toBeNull();
    fireEvent.click(stop);
    expect(onStop).not.toHaveBeenCalled();
  });

  // Esc 的用例一律经 pressEscape 发真实按键（带 keyCode 27），走编辑器自己的
  // 按键处理 —— 只带 key 的合成事件会绕过 ProseMirror，让坏掉的 Esc 也测成绿的。
  it("Given a running turn and focus in the empty editor, When the user presses Escape on the keyboard, Then the turn is stopped", async () => {
    const user = userEvent.setup();
    const onStop = vi.fn();
    render(<ChatComposer running onStop={onStop} onSubmit={vi.fn()} />);

    const editor = screen.getByRole("textbox");
    await user.click(editor);
    pressEscape(editor);
    expect(onStop).toHaveBeenCalledTimes(1);
  });

  it("Given a running turn but a draft is typed, When Escape is pressed, Then nothing is stopped", async () => {
    const inputHandle = createRef<AIChatInputHandle>();
    const onStop = vi.fn();
    render(
      <ChatComposer
        inputHandleRef={inputHandle}
        running
        onStop={onStop}
        onSubmit={vi.fn()}
      />,
    );

    inputHandle.current?.loadDraft("half a thought");
    await screen.findByRole("button", { name: "Send" });
    pressEscape(screen.getByRole("textbox"));
    expect(onStop).not.toHaveBeenCalled();
  });

  it("Given a running turn with an image attached, When Escape is pressed, Then nothing is stopped", async () => {
    const onStop = vi.fn();
    render(
      <ChatComposer
        running
        supportsImageInput
        onStop={onStop}
        onSubmit={vi.fn()}
      />,
    );

    await attachPng("pending.png");
    pressEscape(screen.getByRole("textbox"));
    expect(onStop).not.toHaveBeenCalled();
  });

  it("Given an open mention popover, When Escape is pressed, Then it only closes the popover", async () => {
    const editorRef = createRef<Editor | null>();
    const onStop = vi.fn();
    render(
      <ChatComposer
        editorRef={editorRef}
        mentionSources={{
          agents: [{ kind: "agent", refId: 1, label: "Reviewer" }],
          projects: [],
          devices: [],
        }}
        running
        onStop={onStop}
        onSubmit={vi.fn()}
      />,
    );

    act(() => {
      editorRef.current?.commands.insertContent("@");
    });
    await screen.findByRole("listbox");
    pressEscape(screen.getByRole("textbox"));
    await waitFor(() => expect(screen.queryByRole("listbox")).toBeNull());
    expect(onStop).not.toHaveBeenCalled();
  });

  it("Given an open slash popover, When Escape is pressed, Then it only closes the popover", async () => {
    const editorRef = createRef<Editor | null>();
    const onStop = vi.fn();
    render(
      <ChatComposer
        editorRef={editorRef}
        backendType="claudecode"
        onSlashSelect={vi.fn()}
        slashCommands={[
          {
            name: "compact",
            label: "/compact",
            trigger: "/",
            resolve: () => ({ kind: "literal_text", text: "/compact" }),
          },
        ]}
        running
        onStop={onStop}
        onSubmit={vi.fn()}
      />,
    );

    act(() => {
      editorRef.current?.commands.insertContent("/");
    });
    await screen.findByRole("listbox");
    pressEscape(screen.getByRole("textbox"));
    await waitFor(() => expect(screen.queryByRole("listbox")).toBeNull());
    expect(onStop).not.toHaveBeenCalled();
  });

  it("Given an IME composition is in progress, When Escape is pressed, Then it is left to the IME", () => {
    const onStop = vi.fn();
    render(<ChatComposer running onStop={onStop} onSubmit={vi.fn()} />);

    pressEscape(screen.getByRole("textbox"), { isComposing: true });
    expect(onStop).not.toHaveBeenCalled();
  });

  it("Given edit mode during a running turn, When Escape is pressed, Then it cancels the edit instead of stopping", () => {
    const onStop = vi.fn();
    const onCancelEdit = vi.fn();
    render(
      <ChatComposer
        editing
        running
        onStop={onStop}
        onCancelEdit={onCancelEdit}
        onSubmit={vi.fn()}
      />,
    );

    pressEscape(screen.getByRole("textbox"));
    expect(onCancelEdit).toHaveBeenCalledTimes(1);
    expect(onStop).not.toHaveBeenCalled();
    expect(screen.queryByRole("button", { name: "Stop" })).toBeNull();
  });

  it("Given focus is on a bottom-bar control rather than the editor, When Escape is pressed, Then nothing is stopped", () => {
    const onStop = vi.fn();
    render(
      <ChatComposer
        running
        onStop={onStop}
        leadingControls={<button type="button">pill</button>}
        onSubmit={vi.fn()}
      />,
    );

    pressEscape(screen.getByRole("button", { name: "pill" }));
    expect(onStop).not.toHaveBeenCalled();
  });

  it("Given stop is already in flight, When Escape is pressed again, Then the host is not asked twice", () => {
    const onStop = vi.fn();
    render(
      <ChatComposer running stopping onStop={onStop} onSubmit={vi.fn()} />,
    );

    pressEscape(screen.getByRole("textbox"));
    expect(onStop).not.toHaveBeenCalled();
  });

  it("Given a host that wires no stop (agentre-server), When a turn runs, Then the submit slot is the plain send control", () => {
    const { container } = render(<ChatComposer running onSubmit={vi.fn()} />);

    const send = screen.getByRole("button", { name: "Send" });
    expect(send).toHaveAttribute("title", "Send (Enter)");
    expect(screen.queryByRole("button", { name: /Stop/ })).toBeNull();
    expect(
      container.querySelectorAll("[data-slot='composer-bar'] button"),
    ).toHaveLength(2);
  });
});

describe("ChatComposer · editing", () => {
  it("Given the host enters edit mode, When rendered, Then a banner explains it and the submit control becomes Save", async () => {
    render(<ChatComposer editing editDraft="old text" onSubmit={vi.fn()} />);

    expect(
      await screen.findByRole("status", { name: "Editing message" }),
    ).toBeInTheDocument();
    await waitFor(() =>
      expect(screen.getByRole("button", { name: "Save" })).toBeEnabled(),
    );
    expect(screen.queryByRole("button", { name: "Send" })).toBeNull();
  });

  it("Given edit mode, When the banner's cancel is clicked, Then the host is told to leave it", async () => {
    const onCancelEdit = vi.fn();
    render(
      <ChatComposer editing onCancelEdit={onCancelEdit} onSubmit={vi.fn()} />,
    );

    fireEvent.click(await screen.findByRole("button", { name: "Cancel edit" }));
    expect(onCancelEdit).toHaveBeenCalled();
  });

  it("Given edit mode, When Escape is pressed, Then the host is told to leave it", () => {
    const onCancelEdit = vi.fn();
    const { container } = render(
      <ChatComposer editing onCancelEdit={onCancelEdit} onSubmit={vi.fn()} />,
    );

    fireEvent.keyDown(formOf(container), { key: "Escape" });
    expect(onCancelEdit).toHaveBeenCalled();
  });

  it("Given edit mode and focus in the editor, When the user presses Escape on the keyboard, Then the edit is cancelled once", () => {
    const onCancelEdit = vi.fn();
    render(
      <ChatComposer
        editing
        editDraft="old text"
        onCancelEdit={onCancelEdit}
        onSubmit={vi.fn()}
      />,
    );

    pressEscape(screen.getByRole("textbox"));
    expect(onCancelEdit).toHaveBeenCalledTimes(1);
  });

  it("Given edit mode with an open mention popover, When Escape is pressed, Then it only closes the popover and the edit survives", async () => {
    const editorRef = createRef<Editor | null>();
    const onCancelEdit = vi.fn();
    render(
      <ChatComposer
        editing
        editorRef={editorRef}
        mentionSources={{
          agents: [{ kind: "agent", refId: 1, label: "Reviewer" }],
          projects: [],
          devices: [],
        }}
        onCancelEdit={onCancelEdit}
        onSubmit={vi.fn()}
      />,
    );

    act(() => {
      editorRef.current?.commands.insertContent("@");
    });
    await screen.findByRole("listbox");
    pressEscape(screen.getByRole("textbox"));
    await waitFor(() => expect(screen.queryByRole("listbox")).toBeNull());
    expect(onCancelEdit).not.toHaveBeenCalled();
  });

  it("Given edit mode, When Escape dismisses an IME composition, Then the edit is not cancelled", () => {
    const onCancelEdit = vi.fn();
    render(
      <ChatComposer editing onCancelEdit={onCancelEdit} onSubmit={vi.fn()} />,
    );

    pressEscape(screen.getByRole("textbox"), { isComposing: true });
    expect(onCancelEdit).not.toHaveBeenCalled();
  });

  it("Given edit mode, When rendered, Then the image entry and the host's bottom-bar slots stand down", () => {
    render(
      <ChatComposer
        editing
        supportsImageInput
        leadingControls={<span>pills</span>}
        trailingControls={<span>meters</span>}
        onSubmit={vi.fn()}
      />,
    );

    // 编辑一条既有消息时改不了它的附件，也不该看到「这一轮要花多少上下文」。
    expect(screen.queryByLabelText("Add image")).toBeNull();
    expect(screen.queryByText("pills")).toBeNull();
    expect(screen.queryByText("meters")).toBeNull();
  });

  it("Given edit mode, When the shortcut hint is read, Then it describes saving rather than sending", async () => {
    render(<ChatComposer editing onSubmit={vi.fn()} />);

    expect(await screen.findByText("↵ Save · Esc Cancel")).toBeInTheDocument();
  });
});

describe("ChatComposer · command mode", () => {
  it("Given local commands are available, When the line starts with !, Then a banner says it is not sent to the AI", async () => {
    const onCommandModeChange = vi.fn();
    const inputHandle = createRef<AIChatInputHandle>();
    render(
      <ChatComposer
        inputHandleRef={inputHandle}
        localCommandsEnabled
        onCommandSubmit={vi.fn()}
        onCommandModeChange={onCommandModeChange}
        onSubmit={vi.fn()}
      />,
    );

    inputHandle.current?.loadDraft("!ls");

    expect(await screen.findByText("Not sent to AI")).toBeInTheDocument();
    await waitFor(() => expect(onCommandModeChange).toHaveBeenCalledWith(true));
  });

  it("Given command mode, When the submit control is read, Then it offers to run rather than send", async () => {
    const inputHandle = createRef<AIChatInputHandle>();
    render(
      <ChatComposer
        inputHandleRef={inputHandle}
        localCommandsEnabled
        trailingControls={<span>meters</span>}
        onCommandSubmit={vi.fn()}
        onSubmit={vi.fn()}
      />,
    );

    inputHandle.current?.loadDraft("!ls");

    expect(
      await screen.findByRole("button", { name: "Run command" }),
    ).toBeInTheDocument();
    // 跑一条本地命令不消耗上下文，计量器留在那儿会读错。
    expect(screen.queryByText("meters")).toBeNull();
  });
});

describe("ChatComposer · draft handle", () => {
  it("Given a send failed, When the host restores the draft, Then text and images both come back", async () => {
    const handle = createRef<ChatComposerHandle>();
    const onSubmit = vi.fn();
    render(
      <ChatComposer ref={handle} supportsImageInput onSubmit={onSubmit} />,
    );

    handle.current?.restoreDraft("retry me", [
      {
        dataUrl: "data:image/png;base64,AAA",
        mediaType: "image/png",
        name: "back.png",
      },
    ]);

    expect(await screen.findByAltText("back.png")).toBeInTheDocument();
    await waitFor(() => expect(sendButton()).toBeEnabled());
    fireEvent.click(sendButton());
    await waitFor(() =>
      expect(onSubmit).toHaveBeenCalledWith(
        expect.objectContaining({ text: "retry me" }),
      ),
    );
  });

  it("Given a restored draft, When the host discards it, Then both the text and the images are cleared", async () => {
    const handle = createRef<ChatComposerHandle>();
    render(<ChatComposer ref={handle} supportsImageInput onSubmit={vi.fn()} />);

    handle.current?.restoreDraft("throw away", [
      {
        dataUrl: "data:image/png;base64,AAA",
        mediaType: "image/png",
        name: "gone.png",
      },
    ]);
    await screen.findByAltText("gone.png");

    handle.current?.clearDraft();

    await waitFor(() => expect(screen.queryByAltText("gone.png")).toBeNull());
    await waitFor(() => expect(sendButton()).toBeDisabled());
  });
});

describe("ChatComposer · append draft", () => {
  it("Given an empty input, When the host appends text, Then the input holds exactly that text", async () => {
    const handle = createRef<ChatComposerHandle>();
    const onSubmit = vi.fn();
    render(<ChatComposer ref={handle} onSubmit={onSubmit} />);

    act(() => handle.current?.appendDraft("moved back"));

    await waitFor(() => expect(sendButton()).toBeEnabled());
    fireEvent.click(sendButton());
    await waitFor(() =>
      expect(onSubmit).toHaveBeenCalledWith(
        expect.objectContaining({ text: "moved back" }),
      ),
    );
  });

  it("Given a draft with an image, When the host appends text, Then the new text follows after a blank line and the image stays", async () => {
    const handle = createRef<ChatComposerHandle>();
    const onSubmit = vi.fn();
    render(
      <ChatComposer ref={handle} supportsImageInput onSubmit={onSubmit} />,
    );
    act(() =>
      handle.current?.restoreDraft("half written", [
        {
          dataUrl: "data:image/png;base64,AAA",
          mediaType: "image/png",
          name: "keep.png",
        },
      ]),
    );
    await screen.findByAltText("keep.png");

    act(() => handle.current?.appendDraft("moved back"));

    expect(screen.getByAltText("keep.png")).toBeInTheDocument();
    fireEvent.click(sendButton());
    await waitFor(() =>
      expect(onSubmit).toHaveBeenCalledWith(
        expect.objectContaining({ text: "half written\n\nmoved back" }),
      ),
    );
  });

  it("Given a draft, When the host appends text, Then the input takes focus with the caret at the end of the appended text", async () => {
    const handle = createRef<ChatComposerHandle>();
    render(<ChatComposer ref={handle} onSubmit={vi.fn()} />);
    act(() => handle.current?.restoreDraft("half written", []));
    const textbox = screen.getByRole("textbox");
    expect(textbox).not.toHaveFocus();

    act(() => handle.current?.appendDraft("moved back"));

    await waitFor(() => expect(textbox).toHaveFocus());
    const selection = window.getSelection();
    expect(selection?.isCollapsed).toBe(true);
    expect(selection?.focusNode?.textContent).toBe("moved back");
    expect(selection?.focusOffset).toBe("moved back".length);
  });

  it("Given the composer is editing a message, When the host appends text, Then the edit draft is untouched and the text lands once editing ends", async () => {
    const handle = createRef<ChatComposerHandle>();
    const onSubmit = vi.fn();
    const { rerender } = render(
      <ChatComposer
        ref={handle}
        editing
        editDraft="old text"
        onSubmit={onSubmit}
      />,
    );
    await waitFor(() =>
      expect(screen.getByRole("button", { name: "Save" })).toBeEnabled(),
    );

    act(() => handle.current?.appendDraft("moved back"));
    fireEvent.click(screen.getByRole("button", { name: "Save" }));
    await waitFor(() =>
      expect(onSubmit).toHaveBeenLastCalledWith(
        expect.objectContaining({ text: "old text" }),
      ),
    );

    rerender(<ChatComposer ref={handle} onSubmit={onSubmit} />);
    await waitFor(() => expect(sendButton()).toBeEnabled());
    fireEvent.click(sendButton());
    await waitFor(() =>
      expect(onSubmit).toHaveBeenLastCalledWith(
        expect.objectContaining({ text: "moved back" }),
      ),
    );
  });

  // 退出编辑之后、输入框还没被清空之前（提交 → 渲染 → 被动 effect 之间）回来的追加，
  // 不能先落进输入框、再被「退出编辑清空」那一下抹掉。
  it("Given editing has just ended but the input is not cleared yet, When the host appends text in that gap, Then the text survives", async () => {
    const handle = createRef<ChatComposerHandle>();
    const onSubmit = vi.fn();
    const { rerender } = render(
      <AppendOnEditExit handle={handle} editing onSubmit={onSubmit} />,
    );
    await waitFor(() =>
      expect(screen.getByRole("button", { name: "Save" })).toBeEnabled(),
    );

    rerender(
      <AppendOnEditExit handle={handle} editing={false} onSubmit={onSubmit} />,
    );
    await waitFor(() => expect(sendButton()).toBeEnabled());
    fireEvent.click(sendButton());
    await waitFor(() =>
      expect(onSubmit).toHaveBeenLastCalledWith(
        expect.objectContaining({ text: "moved back" }),
      ),
    );
  });
});

/** 在编辑态翻成 false 的那一次提交的 layout 阶段（被动 effect 之前）追加一段字。 */
function AppendOnEditExit({
  handle,
  editing,
  onSubmit,
}: {
  handle: RefObject<ChatComposerHandle | null>;
  editing: boolean;
  onSubmit: (message: unknown) => void;
}) {
  return (
    <>
      <ChatComposer
        ref={handle}
        editing={editing}
        editDraft={editing ? "old text" : undefined}
        onSubmit={onSubmit}
      />
      <AppendWhen when={!editing} handle={handle} />
    </>
  );
}

function AppendWhen({
  when,
  handle,
}: {
  when: boolean;
  handle: RefObject<ChatComposerHandle | null>;
}) {
  useLayoutEffect(() => {
    if (when) handle.current?.appendDraft("moved back");
  }, [when, handle]);
  return null;
}

describe("ChatComposer · shift+tab", () => {
  it("Given the host wired shift+tab, When it is pressed inside the composer, Then the host is notified once", () => {
    const onShiftTab = vi.fn();
    const { container } = render(
      <ChatComposer onShiftTab={onShiftTab} onSubmit={vi.fn()} />,
    );

    fireEvent.keyDown(formOf(container), { key: "Tab", shiftKey: true });
    expect(onShiftTab).toHaveBeenCalledTimes(1);
  });

  it("Given edit mode, When shift+tab is pressed, Then it is left to the browser", () => {
    const onShiftTab = vi.fn();
    const { container } = render(
      <ChatComposer editing onShiftTab={onShiftTab} onSubmit={vi.fn()} />,
    );

    fireEvent.keyDown(formOf(container), { key: "Tab", shiftKey: true });
    expect(onShiftTab).not.toHaveBeenCalled();
  });
});

describe("ChatComposer · dropping files", () => {
  it("Given the host injected a drop channel, When image paths are dropped, Then they become attachments", async () => {
    const readImages = vi.fn(
      async (paths: string[]): Promise<DroppedImageItem[]> =>
        paths.map((path) => ({
          dataUrl: "data:image/png;base64,AAA",
          kind: "image" as const,
          mediaType: "image/png",
          name: "dropped.png",
          path,
        })),
    );
    const { registrar, drop } = fakeDropZone();

    render(
      <ChatComposer
        supportsImageInput
        dropZone={{ readImages, registerDropZone: registrar }}
        onSubmit={vi.fn()}
      />,
    );

    drop(["/tmp/dropped.png"]);

    expect(await screen.findByAltText("dropped.png")).toBeInTheDocument();
  });

  it("Given a non-image path is dropped, When it resolves, Then it is inserted as text instead of being lost", async () => {
    const readImages = vi.fn(async (): Promise<DroppedImageItem[]> => []);
    const inputHandle = createRef<AIChatInputHandle>();
    const { registrar, drop } = fakeDropZone();

    render(
      <ChatComposer
        inputHandleRef={inputHandle}
        supportsImageInput
        dropZone={{ readImages, registerDropZone: registrar }}
        onSubmit={vi.fn()}
      />,
    );

    drop(["/tmp/notes.md"]);

    await waitFor(() => expect(sendButton()).toBeEnabled());
  });

  it("Given no drop channel, When rendered, Then nothing is registered and the composer still works", () => {
    render(<ChatComposer onSubmit={vi.fn()} />);
    expect(sendButton()).toBeInTheDocument();
  });

  /**
   * 总量预算对**拖入**这条路同样成立。
   *
   * `resolveDroppedPaths` 只卡张数(`remainingImageSlots`),而宿主拒的是字节总量 ——
   * 拖进来的四张各 4 MB 单张都合法、张数也合法,于是全都贴上了;按下发送时
   * `chat_svc.blocksFromSendImages` 那道闸把**整轮**拒掉,回的是一个笼统的
   * InvalidParameter。用户写的那句话连同图一起没了,而屏幕上此前没有一个字说过
   * 这些图超了。
   *
   * 处置与贴/粘那条路逐字相同:这一批不贴上,并说清是总量的缘故。
   */
  it("拖入撑爆总量预算时不贴上,说的是总量,先前贴的仍在", async () => {
    const readImages = vi.fn(
      async (paths: string[]): Promise<DroppedImageItem[]> =>
        paths.map((path) => ({
          // 4 MB 原始字节:单张(5 MB)与张数(4)两道闸都过得去。
          dataUrl: `data:image/png;base64,${b64OfBytes(4 * 1024 * 1024)}`,
          kind: "image" as const,
          mediaType: "image/png",
          name: "huge.png",
          path,
        })),
    );
    const { registrar, drop } = fakeDropZone();

    render(
      <ChatComposer
        supportsImageInput
        dropZone={{ readImages, registerDropZone: registrar }}
        onSubmit={vi.fn()}
      />,
    );

    fireEvent.change(imageInput(), {
      target: { files: [pngOfBytes("first.png", 4 * 1024 * 1024)] },
    });
    await screen.findByAltText("first.png");

    drop(["/tmp/huge.png"]);

    const alert = await screen.findByRole("alert");
    expect(alert.textContent).toBeTruthy();
    expect(alert.textContent).not.toMatch(/up to 4 images/i);
    expect(alert.textContent).not.toMatch(/unsupported|not supported/i);
    expect(screen.queryByAltText("huge.png")).toBeNull();
    // 超限的是拖进来的那一张;把先前贴的一起清掉是另一种数据丢失。
    expect(screen.getByAltText("first.png")).toBeTruthy();
  });

  it("拖入在预算之内时照常贴上,与从前一样", async () => {
    const readImages = vi.fn(
      async (paths: string[]): Promise<DroppedImageItem[]> =>
        paths.map((path) => ({
          dataUrl: "data:image/png;base64,AAAA",
          kind: "image" as const,
          mediaType: "image/png",
          name: "small.png",
          path,
        })),
    );
    const { registrar, drop } = fakeDropZone();

    render(
      <ChatComposer
        supportsImageInput
        dropZone={{ readImages, registerDropZone: registrar }}
        onSubmit={vi.fn()}
      />,
    );

    drop(["/tmp/small.png"]);

    expect(await screen.findByAltText("small.png")).toBeInTheDocument();
    expect(screen.queryByRole("alert")).toBeNull();
  });
});

describe("ChatComposer · surface", () => {
  /** 不带变体前缀的 `border` / `border-*`：静止态画了边。 */
  const restingEdges = (el: Element) =>
    Array.from(el.classList).filter((c) => /^border(?:-|$)/.test(c));

  it("Given the composer at rest, When the shell is inspected, Then it is a borderless field fill with a larger radius", () => {
    const { container } = render(<ChatComposer onSubmit={vi.fn()} />);

    const shell = container.querySelector("form")!;
    expect(restingEdges(shell)).toEqual([]);
    expect(shell).toHaveClass("bg-field-fill");
    // 悬停加深只在未聚焦时：聚焦后鼠标停在上面也不能把内容页色盖回填充色。
    expect(shell).toHaveClass("not-focus-within:hover:bg-field-fill-hover");
    expect(shell).not.toHaveClass("hover:bg-field-fill-hover");
    expect(shell).toHaveClass("rounded-xl");
    expect(shell).not.toHaveClass("bg-card");
    expect(shell).not.toHaveClass("shadow-xs");
  });

  it("Given focus lands inside the composer, When the shell is inspected, Then it returns to the sheet colour and shows only the focus ring", () => {
    const { container } = render(<ChatComposer onSubmit={vi.fn()} />);

    const shell = container.querySelector("form")!;
    expect(shell).toHaveClass(
      "focus-within:bg-sheet",
      "focus-within:focus-outline",
    );
    expect(Array.from(shell.classList).join(" ")).not.toMatch(
      /focus-within:(focus-halo|border)/,
    );
  });

  it("Given edit mode, When rendered, Then the banner is a fill with no dividing line under it and the shell stays borderless", async () => {
    const { container } = render(
      <ChatComposer editing editDraft="old text" onSubmit={vi.fn()} />,
    );

    const banner = await screen.findByRole("status", {
      name: "Editing message",
    });
    expect(banner).toHaveClass("bg-primary-soft");
    expect(restingEdges(banner)).toEqual([]);
    expect(restingEdges(container.querySelector("form")!)).toEqual([]);
  });
});

describe("ChatComposer · bottom bar", () => {
  it("Given any width, When the bar is inspected, Then it never wraps and the submit control never shrinks", () => {
    const { container } = render(<ChatComposer onSubmit={vi.fn()} />);

    const bar = container.querySelector("[data-slot='composer-bar']");
    expect(bar).toHaveClass("flex-nowrap");
    expect(sendButton().className).toContain("shrink-0");
  });

  it("Given host slots, When the bar is laid out, Then leading controls sit left of the gap and trailing controls hug the submit control", () => {
    const { container } = render(
      <ChatComposer
        leadingControls={<span data-testid="lead">pills</span>}
        trailingControls={<span data-testid="trail">meters</span>}
        onSubmit={vi.fn()}
      />,
    );

    const bar = container.querySelector("[data-slot='composer-bar']");
    if (!bar) throw new Error("composer bar not rendered");
    const order = Array.from(bar.children).map(
      (child) =>
        child.getAttribute("data-slot") ??
        child.getAttribute("data-testid") ??
        child.tagName.toLowerCase(),
    );

    expect(order.indexOf("lead")).toBeLessThan(order.indexOf("composer-gap"));
    expect(order.indexOf("composer-gap")).toBeLessThan(order.indexOf("trail"));
    expect(order.indexOf("trail")).toBeLessThan(order.length - 1);
  });

  it("Given the host passes no hint, When the bar is read, Then no standing shortcut hint is shown", () => {
    const { container } = render(<ChatComposer onSubmit={vi.fn()} />);

    // 发送与换行是人人都会的操作，常驻教学文案只占底栏（C7）。
    expect(container.querySelector("[data-slot='composer-shortcuts']")).toBe(
      null,
    );
    expect(screen.queryByText(/New line/)).toBeNull();
  });

  it("Given the host supplies its own hint, When rendered, Then it replaces the built-in one", () => {
    render(
      <ChatComposer shortcutsHint={<span>offline</span>} onSubmit={vi.fn()} />,
    );

    expect(screen.getByText("offline")).toBeInTheDocument();
  });

  it("Given the host suppresses the hint in edit mode, When rendered, Then not even the edit hint is shown", () => {
    render(<ChatComposer editing shortcutsHint={null} onSubmit={vi.fn()} />);
    expect(screen.queryByText("↵ Save · Esc Cancel")).toBeNull();
  });
});

describe("ChatComposer · autofocus", () => {
  it("Given autofocus is on at mount, When the composer appears, Then the editor already has focus", async () => {
    render(<ChatComposer autoFocusOnMount onSubmit={vi.fn()} />);

    // 新建会话进来就能直接打字,不用再点一次输入框。TipTap 的 autofocus 是异步的。
    await waitFor(() => expect(screen.getByRole("textbox")).toHaveFocus());
  });

  it("Given autofocus turns on after mount, When it flips, Then the editor takes focus then", async () => {
    const { rerender } = render(<ChatComposer onSubmit={vi.fn()} />);
    expect(screen.getByRole("textbox")).not.toHaveFocus();

    rerender(<ChatComposer autoFocusOnMount onSubmit={vi.fn()} />);

    await waitFor(() => expect(screen.getByRole("textbox")).toHaveFocus());
  });

  it("Given autofocus is off, When the composer mounts, Then focus is left where it was", () => {
    render(<ChatComposer onSubmit={vi.fn()} />);
    expect(screen.getByRole("textbox")).not.toHaveFocus();
  });
});

describe("ChatComposer · top slot", () => {
  it("Given the host supplies a top slot, When rendered, Then it sits above the editor inside the card", () => {
    render(<ChatComposer topSlot={<div>queued</div>} onSubmit={vi.fn()} />);
    expect(screen.getByText("queued")).toBeInTheDocument();
  });

  /**
   * 总量上限(规格 2026-09-07-attachment-render-and-send-budget)。
   *
   * 此前只卡单张:MAX_IMAGE_COUNT=4 与 MAX_IMAGE_BYTES=5MB,没有任何一处卡总量。
   * 而 runtime.run 把一条消息的所有图装在同一个请求里,4 × 5MB base64 之后是 26MB,
   * 而中继单帧上限是 10MB —— 超限不是「这一次发送失败了」,是 gorilla 回 1009、
   * 整条物理连接被拆掉,那台机器上每一条会话一起重连。
   */
  it("贴到撑爆总量预算时拒收这一张,说明说的是总量,先前贴的仍在", async () => {
    render(<ChatComposer onSubmit={() => {}} />);

    // 两张各 4 MB:单张都合法(< 5MB),加起来 8 MB 超过 6990506 的总量预算。
    fireEvent.change(imageInput(), {
      target: { files: [pngOfBytes("first.png", 4 * 1024 * 1024)] },
    });
    await screen.findByAltText("first.png");

    fireEvent.change(imageInput(), {
      target: { files: [pngOfBytes("second.png", 4 * 1024 * 1024)] },
    });

    // 规格要的是「说的是总量」,而它的可观察形态是:这句话与既有那两条不同 ——
    // 三件事的处置不同(张数超了删一张就行,格式不支持换个文件,总量超了要换小图),
    // 复用措辞会把用户指向错的动作。断在「不是那两句」上,而不是钉某个英文词。
    const alert = await screen.findByRole("alert");
    expect(alert.textContent).toBeTruthy();
    expect(alert.textContent).not.toMatch(/up to 4 images/i);
    expect(alert.textContent).not.toMatch(/unsupported|not supported/i);
    // 超限的是**这一张**;把先前贴的一起清掉是另一种数据丢失。
    expect(screen.getByAltText("first.png")).toBeTruthy();
    expect(screen.queryByAltText("second.png")).toBeNull();
  });

  it("总量在预算内时照常贴上,与从前一样", async () => {
    render(<ChatComposer onSubmit={() => {}} />);

    await attachPng("a.png");
    fireEvent.change(imageInput(), {
      target: { files: [pngOfBytes("b.png", 1024)] },
    });

    await screen.findByAltText("b.png");
    expect(screen.queryByRole("alert")).toBeNull();
  });
});

/**
 * 触屏档（`inputMode="touch"`）：手机上回车只换行、只有按钮发送；提交位两格常驻、
 * 各 44px；空草稿是胶囊、有字是卡片，两种形态之间输入框不重挂载。
 */
describe("ChatComposer · touch input mode", () => {
  it("Given touch mode and a typed draft, When Enter is pressed, Then a new line is inserted and nothing is sent", async () => {
    const editorRef = createRef<Editor | null>();
    const inputHandle = createRef<AIChatInputHandle>();
    const onSubmit = vi.fn();
    render(
      <ChatComposer
        inputMode="touch"
        editorRef={editorRef}
        inputHandleRef={inputHandle}
        onSubmit={onSubmit}
      />,
    );

    act(() => inputHandle.current?.loadDraft("first line"));
    pressEnter(screen.getByRole("textbox"));

    expect(onSubmit).not.toHaveBeenCalled();
    expect(editorRef.current!.state.doc.childCount).toBe(2);
  });

  it("Given touch mode and an image-only draft, When Enter is pressed, Then the image is not sent", async () => {
    const editorRef = createRef<Editor | null>();
    const onSubmit = vi.fn();
    render(
      <ChatComposer
        inputMode="touch"
        editorRef={editorRef}
        supportsImageInput
        onSubmit={onSubmit}
      />,
    );

    await attachPng("only-image.png");
    act(() => {
      editorRef.current!.commands.focus("end");
    });
    pressEnter(screen.getByRole("textbox"));

    expect(onSubmit).not.toHaveBeenCalled();
    expect(screen.getByAltText("only-image.png")).toBeInTheDocument();
  });

  it("Given touch mode, When Mod+Enter is pressed, Then nothing is sent either", async () => {
    const inputHandle = createRef<AIChatInputHandle>();
    const onSubmit = vi.fn();
    render(
      <ChatComposer
        inputMode="touch"
        sendOnEnter={false}
        inputHandleRef={inputHandle}
        onSubmit={onSubmit}
      />,
    );

    act(() => inputHandle.current?.loadDraft("hello"));
    pressEnter(screen.getByRole("textbox"), { metaKey: true });
    pressEnter(screen.getByRole("textbox"), { ctrlKey: true });

    expect(onSubmit).not.toHaveBeenCalled();
  });

  it("Given touch mode and an IME composition, When Enter confirms a candidate, Then nothing is sent and no line is added", async () => {
    const editorRef = createRef<Editor | null>();
    const inputHandle = createRef<AIChatInputHandle>();
    const onSubmit = vi.fn();
    render(
      <ChatComposer
        inputMode="touch"
        editorRef={editorRef}
        inputHandleRef={inputHandle}
        onSubmit={onSubmit}
      />,
    );

    act(() => inputHandle.current?.loadDraft("ni"));
    const editor = screen.getByRole("textbox");
    fireEvent.compositionStart(editor);
    pressEnter(editor, { isComposing: true, keyCode: 229, which: 229 });

    expect(onSubmit).not.toHaveBeenCalled();
    expect(editorRef.current!.state.doc.childCount).toBe(1);
  });

  it("Given touch mode and a typed draft, When the send button is tapped, Then the draft is sent", async () => {
    const inputHandle = createRef<AIChatInputHandle>();
    const onSubmit = vi.fn();
    render(
      <ChatComposer
        inputMode="touch"
        inputHandleRef={inputHandle}
        onSubmit={onSubmit}
      />,
    );

    act(() => inputHandle.current?.loadDraft("tap to send"));
    const send = screen.getByRole("button", { name: "Send" });
    await waitFor(() => expect(send).toBeEnabled());
    fireEvent.click(send);

    await waitFor(() =>
      expect(onSubmit).toHaveBeenCalledWith({ text: "tap to send" }),
    );
  });

  it("Given touch mode and a running turn with an empty draft, When the submit slot is read, Then stop and a disabled up-arrow send sit 8px apart with 44px targets", () => {
    const { container } = render(
      <ChatComposer
        inputMode="touch"
        running
        onStop={vi.fn()}
        onSubmit={vi.fn()}
      />,
    );

    const stop = screen.getByRole("button", { name: "Stop" });
    const send = screen.getByRole("button", { name: "Send" });
    expect(send).toBeDisabled();
    expect(send.querySelector(".lucide-arrow-up")).not.toBeNull();

    const slot = container.querySelector("[data-slot='composer-submit']");
    expect(slot).not.toBeNull();
    expect(slot).toHaveClass("gap-2");
    expect(Array.from(slot!.children)).toEqual([stop, send]);
    for (const cell of [stop, send]) expect(cell).toHaveClass("size-11");
  });

  it("Given touch mode, a running turn and a typed steer, When it is sent, Then both cells stay exactly where they were and send greys out", async () => {
    const inputHandle = createRef<AIChatInputHandle>();
    const onSubmit = vi.fn();
    const { container } = render(
      <ChatComposer
        inputMode="touch"
        inputHandleRef={inputHandle}
        running
        onStop={vi.fn()}
        onSubmit={onSubmit}
      />,
    );

    act(() => inputHandle.current?.loadDraft("also cover the empty state"));
    const send = screen.getByRole("button", { name: "Send" });
    await waitFor(() => expect(send).toBeEnabled());
    const stop = screen.getByRole("button", { name: "Stop" });
    const slot = container.querySelector("[data-slot='composer-submit']")!;
    const before = Array.from(slot.children);

    fireEvent.click(send);

    await waitFor(() => expect(onSubmit).toHaveBeenCalledTimes(1));
    await waitFor(() => expect(send).toBeDisabled());
    expect(container.querySelector("[data-slot='composer-submit']")).toBe(slot);
    expect(Array.from(slot.children)).toEqual(before);
    expect(before).toEqual([stop, send]);
  });

  it("Given touch mode, When titles, banners and hints are read, Then none of them name a keyboard shortcut", async () => {
    const inputHandle = createRef<AIChatInputHandle>();
    const { container, rerender } = render(
      <ChatComposer
        inputMode="touch"
        inputHandleRef={inputHandle}
        running
        onStop={vi.fn()}
        localCommandsEnabled
        onCommandSubmit={vi.fn()}
        onSubmit={vi.fn()}
      />,
    );

    act(() => inputHandle.current?.loadDraft("steer"));
    await waitFor(() =>
      expect(screen.getByRole("button", { name: "Send" })).toBeEnabled(),
    );
    expectNoShortcutCopy(container);
    // 文案说的仍是同一件事，只是去掉了键名。
    expect(screen.getByRole("button", { name: "Send" })).toHaveAttribute(
      "title",
      "Insert after AI finishes",
    );
    expect(screen.getByRole("button", { name: "Stop" })).toHaveAttribute(
      "title",
      "Stop current turn",
    );

    act(() => inputHandle.current?.loadDraft("!ls"));
    await screen.findByRole("button", { name: "Run command" });
    expectNoShortcutCopy(container);

    rerender(
      <ChatComposer
        inputMode="touch"
        inputHandleRef={inputHandle}
        editing
        editDraft="old"
        onSubmit={vi.fn()}
      />,
    );
    await screen.findByRole("status", { name: "Editing message" });
    expectNoShortcutCopy(container);
  });

  it("Given touch mode, When the shape changes, Then pill and card share the same right inset class for the submit slot (jsdom has no layout)", async () => {
    const editorRef = createRef<Editor | null>();
    const { container } = render(
      <ChatComposer
        inputMode="touch"
        editorRef={editorRef}
        onSubmit={vi.fn()}
      />,
    );
    const inset = () => {
      const bar = container.querySelector('[data-slot="composer-bar"]')!;
      const holder = bar
        .closest("form")!
        .querySelector('[data-slot="composer-editor"]')!.parentElement!;
      return holder.className.split(/\s+/).filter((c) => /^pr-|^px-/.test(c));
    };
    const pillInset = inset().filter((c) => c.startsWith("pr-"));
    act(() => {
      editorRef.current!.commands.insertContent("hi");
    });
    await waitFor(() =>
      expect(formOf(container)).toHaveAttribute("data-shape", "card"),
    );
    // 卡片的 px-* 与胶囊的 pr-* 必须是同一档（px-3.5 ⇒ 右 14px）。
    const cardInset = inset();
    expect(pillInset).toEqual(["pr-3.5"]);
    expect(cardInset).toContain("px-3.5");
  });

  it("Given touch mode, When the shape changes, Then the submit slot keeps the same bottom distance (2px) in pill and card, and desktop classes are unchanged", async () => {
    const editorRef = createRef<Editor | null>();
    const { container } = render(
      <ChatComposer
        inputMode="touch"
        editorRef={editorRef}
        onSubmit={vi.fn()}
      />,
    );
    const bottom = () => {
      const bar = container.querySelector('[data-slot="composer-bar"]')!;
      const holder = bar
        .closest("form")!
        .querySelector('[data-slot="composer-editor"]')!.parentElement!;
      // 提交位到外壳底边 = 外层容器底内边距 + 底栏底内边距；胶囊里 44px 键在 48px 里居中 ⇒ 2px。
      return bottomPaddingPx(holder) + bottomPaddingPx(bar);
    };
    const pill = bottom();
    act(() => {
      editorRef.current!.commands.insertContent("hi");
    });
    await waitFor(() =>
      expect(formOf(container)).toHaveAttribute("data-shape", "card"),
    );
    expect(pill).toBe(2 + 0);
    expect(bottom()).toBe(pill);

    const desktop = render(<ChatComposer onSubmit={vi.fn()} />);
    const dbar = desktop.container.querySelector('[data-slot="composer-bar"]')!;
    expect(dbar.className).toContain("py-1");
    expect(dbar.parentElement!.className).toContain("pb-1");
  });

  it("Given touch mode, When an attachment error is the last line of the card, Then it keeps the same 4px bottom inset as desktop instead of sitting on the rounded edge", async () => {
    const { container } = render(
      <ChatComposer inputMode="touch" supportsImageInput onSubmit={vi.fn()} />,
    );

    fireEvent.change(imageInput(), {
      target: {
        files: [1, 2, 3, 4, 5].map((n) => pngFile(`shot-${n}.png`)),
      },
    });

    const alert = await screen.findByRole("alert");
    expect(formOf(container)).toHaveAttribute("data-shape", "card");
    const holder = alert.parentElement!;
    expect(holder.lastElementChild).toBe(alert);
    expect(bottomPaddingPx(holder) + bottomPaddingPx(alert)).toBe(4);
  });

  it("Given touch mode, When the draft goes from empty to typed and back, Then the shell turns pill → card → pill without remounting the editor or losing focus", async () => {
    const user = userEvent.setup();
    const editorRef = createRef<Editor | null>();
    const { container } = render(
      <ChatComposer
        inputMode="touch"
        editorRef={editorRef}
        onSubmit={vi.fn()}
      />,
    );
    const form = formOf(container);
    expect(form).toHaveAttribute("data-shape", "pill");

    const editable = screen.getByRole("textbox");
    await user.click(editable);
    // 内容经编辑器自己的命令改，不靠 jsdom 里模拟的逐键输入：后者要等 ProseMirror 的
    // DOM 观察者回读，机器一忙就会读出一个多余的空段落，草稿永远不空。这里要钉的是
    // 外壳随草稿空 / 非空换形、不重挂、不丢焦点，与按键怎么变成文档无关。
    const editor = editorRef.current!;
    act(() => {
      editor.commands.insertContent("hi");
    });
    await waitFor(() => expect(form).toHaveAttribute("data-shape", "card"));
    expect(screen.getByRole("textbox")).toBe(editable);
    expect(editable).toHaveFocus();

    act(() => {
      editor.commands.clearContent(true);
    });
    await waitFor(() => expect(form).toHaveAttribute("data-shape", "pill"));
    expect(screen.getByRole("textbox")).toBe(editable);
    expect(editable).toHaveFocus();
  });

  it("Given touch mode and an image-only draft, When the shell is read, Then it is a card", async () => {
    const { container } = render(
      <ChatComposer inputMode="touch" supportsImageInput onSubmit={vi.fn()} />,
    );

    await attachPng("pending.png");
    expect(formOf(container)).toHaveAttribute("data-shape", "card");
  });

  it("Given touch mode with a physical keyboard, When Escape is pressed in the empty editor during a turn, Then the turn still stops", () => {
    const onStop = vi.fn();
    render(
      <ChatComposer
        inputMode="touch"
        running
        onStop={onStop}
        onSubmit={vi.fn()}
      />,
    );

    pressEscape(screen.getByRole("textbox"));
    expect(onStop).toHaveBeenCalledTimes(1);
  });

  it("Given no input mode, When rendered, Then the shell has no touch shape and the submit slot is not regrouped", () => {
    const { container } = render(
      <ChatComposer running onStop={vi.fn()} onSubmit={vi.fn()} />,
    );

    expect(formOf(container)).not.toHaveAttribute("data-shape");
    expect(container.querySelector("[data-slot='composer-submit']")).toBe(null);
  });
});

/** 触屏档里任何可见文案 / 提示都不许出现键名。 */
function expectNoShortcutCopy(container: HTMLElement): void {
  const shortcut = /\(Esc\)|\(Enter\)|Enter to run|↵|Esc /;
  expect(container.textContent ?? "").not.toMatch(shortcut);
  for (const el of Array.from(container.querySelectorAll("[title]"))) {
    expect(el.getAttribute("title")).not.toMatch(shortcut);
  }
}

/** 真实的回车按键（带 keyCode 13），让 ProseMirror 的按键表也接得到。 */
function pressEnter(target: Element, init: KeyboardEventInit = {}): boolean {
  return fireEvent.keyDown(target, {
    key: "Enter",
    code: "Enter",
    keyCode: 13,
    which: 13,
    ...init,
  });
}

/**
 * 浏览器里真实的 Esc 按键：除了 key 还带 keyCode 27。ProseMirror 按 keyCode 认
 * Esc，并对没人处理的 Esc 一律 preventDefault —— 只带 key 的合成事件（happy-dom 下
 * user-event 也是 keyCode 0）绕过了这一层，测不到真机上的行为。
 */
function pressEscape(target: Element, init: KeyboardEventInit = {}): boolean {
  return fireEvent.keyDown(target, {
    key: "Escape",
    code: "Escape",
    keyCode: 27,
    which: 27,
    ...init,
  });
}

function sendButton(): HTMLElement {
  return screen.getByRole("button", { name: /Send|Save|Run command|Sending/ });
}

function imageInput(): HTMLElement {
  return screen.getByLabelText("Add image", { selector: "input" });
}

function pngFile(name: string): File {
  return new File([new Uint8Array([1, 2, 3])], name, { type: "image/png" });
}

async function attachPng(name: string): Promise<void> {
  fireEvent.change(imageInput(), { target: { files: [pngFile(name)] } });
  await screen.findByAltText(name);
}

/** 一张指定原始字节数的假图(内容不重要,量才重要)。 */
function pngOfBytes(name: string, bytes: number): File {
  return new File([new Uint8Array(bytes)], name, { type: "image/png" });
}

/** 指定**原始**字节数的一段 base64(拖入那条路交回的是 dataUrl,不是 File)。 */
function b64OfBytes(bytes: number): string {
  return "A".repeat(Math.ceil(bytes / 3) * 4);
}

/** 元素自身类名里的底内边距（px）：`p-*` / `py-*` / `pb-*`，一档 = 4px（jsdom 没有布局）。 */
function bottomPaddingPx(el: Element): number {
  return el.className
    .split(/\s+/)
    .filter((c) => /^p[by]?-/.test(c))
    .map((c) => c.replace(/^p[by]?-/, ""))
    .reduce((n, v) => n + Number(v) * 4, 0);
}

function formOf(container: HTMLElement): HTMLElement {
  const form = container.querySelector("form");
  if (!form) throw new Error("composer form not rendered");
  return form;
}

/** 落盘路径的注册器是宿主能力（桌面端是 Wails OnFileDrop），这里替一个假的。 */
function fakeDropZone(): {
  registrar: DropZoneRegistrar;
  drop: (paths: string[]) => void;
} {
  let handler: ((paths: string[]) => void) | null = null;
  return {
    drop: (paths) => handler?.(paths),
    registrar: (_element, onPaths) => {
      handler = onPaths;
      return () => {
        handler = null;
      };
    },
  };
}

describe("ChatComposer · remove last append", () => {
  it("Given a draft and a host append, When the host removes the last append, Then the draft is back to what it was and it returns true", async () => {
    const handle = createRef<ChatComposerHandle>();
    const onSubmit = vi.fn();
    render(<ChatComposer ref={handle} onSubmit={onSubmit} />);
    act(() => handle.current?.restoreDraft("half written", []));
    act(() => handle.current?.appendDraft("moved back"));

    let removed: boolean | undefined;
    act(() => {
      removed = handle.current?.removeLastAppend();
    });

    expect(removed).toBe(true);
    fireEvent.click(sendButton());
    await waitFor(() =>
      expect(onSubmit).toHaveBeenCalledWith(
        expect.objectContaining({ text: "half written" }),
      ),
    );
  });

  it("Given an empty input and a host append, When the host removes the last append, Then the input is empty again", async () => {
    const handle = createRef<ChatComposerHandle>();
    render(<ChatComposer ref={handle} onSubmit={vi.fn()} />);
    act(() => handle.current?.appendDraft("moved back"));
    await waitFor(() => expect(sendButton()).toBeEnabled());

    let removed: boolean | undefined;
    act(() => {
      removed = handle.current?.removeLastAppend();
    });

    expect(removed).toBe(true);
    await waitFor(() => expect(sendButton()).toBeDisabled());
  });

  it("Given the user typed after the append, When the host removes the last append, Then nothing changes and it returns false", async () => {
    const user = userEvent.setup();
    const handle = createRef<ChatComposerHandle>();
    const onSubmit = vi.fn();
    render(<ChatComposer ref={handle} onSubmit={onSubmit} />);
    act(() => handle.current?.appendDraft("moved back"));
    await waitFor(() => expect(sendButton()).toBeEnabled());
    await user.keyboard("!");

    let removed: boolean | undefined;
    act(() => {
      removed = handle.current?.removeLastAppend();
    });

    expect(removed).toBe(false);
    fireEvent.click(sendButton());
    await waitFor(() =>
      expect(onSubmit).toHaveBeenCalledWith(
        expect.objectContaining({ text: "moved back!" }),
      ),
    );
  });

  it("Given no append happened, When the host removes the last append, Then it returns false", () => {
    const handle = createRef<ChatComposerHandle>();
    render(<ChatComposer ref={handle} onSubmit={vi.fn()} />);
    expect(handle.current?.removeLastAppend()).toBe(false);
  });

  it("Given a host append, When the user then types, Then onAppendEdited fires once and the append can no longer be removed", async () => {
    const user = userEvent.setup();
    const handle = createRef<ChatComposerHandle>();
    const onAppendEdited = vi.fn();
    render(
      <ChatComposer
        ref={handle}
        onAppendEdited={onAppendEdited}
        onSubmit={vi.fn()}
      />,
    );
    act(() => handle.current?.appendDraft("moved back"));
    await waitFor(() => expect(sendButton()).toBeEnabled());
    expect(onAppendEdited).not.toHaveBeenCalled();

    await user.keyboard("!?");

    expect(onAppendEdited).toHaveBeenCalledTimes(1);
    expect(handle.current?.removeLastAppend()).toBe(false);
  });

  it("Given a host append, When the host removes it, Then onAppendEdited is not called", () => {
    const handle = createRef<ChatComposerHandle>();
    const onAppendEdited = vi.fn();
    render(
      <ChatComposer
        ref={handle}
        onAppendEdited={onAppendEdited}
        onSubmit={vi.fn()}
      />,
    );
    act(() => handle.current?.appendDraft("moved back"));
    act(() => {
      handle.current?.removeLastAppend();
    });
    expect(onAppendEdited).not.toHaveBeenCalled();
  });

  it("Given an append was already removed, When the host removes again, Then it returns false", () => {
    const handle = createRef<ChatComposerHandle>();
    render(<ChatComposer ref={handle} onSubmit={vi.fn()} />);
    act(() => handle.current?.appendDraft("moved back"));
    act(() => {
      handle.current?.removeLastAppend();
    });
    expect(handle.current?.removeLastAppend()).toBe(false);
  });
});

describe("ChatComposer · 附件查看大图", () => {
  const TWO = [
    {
      dataUrl: "data:image/png;base64,AAA",
      mediaType: "image/png",
      name: "a.png",
    },
    {
      dataUrl: "data:image/png;base64,BBB",
      mediaType: "image/png",
      name: "b.png",
    },
  ];

  async function renderWithAttachments() {
    const handle = createRef<ChatComposerHandle>();
    render(<ChatComposer ref={handle} supportsImageInput onSubmit={vi.fn()} />);
    act(() => handle.current?.restoreDraft("", TWO));
    await screen.findByAltText("b.png");
  }

  it("Given two attachments, When the second thumbnail is clicked, Then the viewer opens on it with every attachment in the group", async () => {
    await renderWithAttachments();

    fireEvent.click(screen.getByRole("button", { name: "View image: b.png" }));

    expect(screen.getByRole("dialog")).toHaveAccessibleName(
      "Image viewer: b.png",
    );
    expect(screen.getByText("2 / 2")).toBeInTheDocument();
  });

  it("Given an attachment, When its remove button is clicked, Then it is removed and no viewer opens", async () => {
    await renderWithAttachments();

    fireEvent.click(screen.getByRole("button", { name: "Remove image a.png" }));

    expect(screen.queryByRole("dialog")).toBeNull();
    expect(screen.queryByAltText("a.png")).toBeNull();
    expect(screen.getByAltText("b.png")).toBeInTheDocument();
  });

  it("Given the viewer was opened from an attachment, When it closes, Then focus returns to that thumbnail", async () => {
    await renderWithAttachments();
    const thumb = screen.getByRole("button", { name: "View image: a.png" });

    fireEvent.click(thumb);
    fireEvent.keyDown(screen.getByRole("dialog"), { key: "Escape" });

    expect(screen.queryByRole("dialog")).toBeNull();
    await waitFor(() =>
      expect(
        screen.getByRole("button", { name: "View image: a.png" }),
      ).toHaveFocus(),
    );
  });
});
