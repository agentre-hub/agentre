import "@testing-library/jest-dom/vitest";
import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { createRef } from "react";
import { describe, expect, it, vi } from "vitest";

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

  it("Given the default hint, When the bar is read, Then it states how to send and how to break a line", () => {
    render(<ChatComposer onSubmit={vi.fn()} />);
    expect(screen.getByText("↵ Send · ⇧↵ New line")).toBeInTheDocument();
  });

  it("Given the host supplies its own hint, When rendered, Then it replaces the built-in one", () => {
    render(
      <ChatComposer shortcutsHint={<span>offline</span>} onSubmit={vi.fn()} />,
    );

    expect(screen.getByText("offline")).toBeInTheDocument();
    expect(screen.queryByText("↵ Send · ⇧↵ New line")).toBeNull();
  });

  it("Given the host suppresses the hint, When rendered, Then no hint is shown at all", () => {
    render(<ChatComposer shortcutsHint={null} onSubmit={vi.fn()} />);
    expect(screen.queryByText("↵ Send · ⇧↵ New line")).toBeNull();
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
