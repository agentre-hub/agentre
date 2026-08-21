import {
  act,
  fireEvent,
  render as rtlRender,
  screen,
  waitFor,
  within,
  type RenderOptions,
} from "@testing-library/react";
import {
  LocalCommandsProvider,
  TranscriptPortsProvider,
} from "@agentre-ai/agentre-ui";
import * as React from "react";
import { describe, expect, it, vi } from "vitest";

import { desktopLocalCommandsAccess } from "@/components/agentre/local-commands-access-desktop";

// 转录里的审批/回答卡片从 TranscriptPortsProvider 取动作端口,而 Provider 由宿主
// (App.tsx)挂载。本文件渲染的是 ChatTranscript 子树,所以自己补一个 ——
// 这些用例只验渲染,不验动作,端口给 no-op 即可。
const testTranscriptPorts = {
  answerToolPermission: async () => {},
  answerUserQuestion: async () => {},
  answerToolApproval: async () => {},
  resolveExecApproval: async () => ({ status: "resolved" }),
  resolvePlanAction: async () => ({}),
};

// 本地命令卡片同样是转录里的一张卡,它从 LocalCommandsProvider 取宿主状态接缝;
// 这里用桌面实现(而不是替身),因为用例正是拿 useLocalCommandsStore 造条目的。
function PortsWrapper({ children }: { children: React.ReactNode }) {
  return (
    <TranscriptPortsProvider ports={testTranscriptPorts}>
      <LocalCommandsProvider access={desktopLocalCommandsAccess}>
        {children}
      </LocalCommandsProvider>
    </TranscriptPortsProvider>
  );
}

function render(
  ui: React.ReactElement,
  options?: Omit<RenderOptions, "wrapper">,
) {
  return rtlRender(ui, { wrapper: PortsWrapper, ...options });
}

const sonnerMocks = vi.hoisted(() => ({
  toast: {
    error: vi.fn(),
    success: vi.fn(),
  },
}));
const runtimeMocks = vi.hoisted(() => ({
  EventsOn: vi.fn((_name: string, _handler: (event: unknown) => void) =>
    vi.fn(),
  ),
}));

vi.mock("sonner", () => sonnerMocks);

// chat.tsx 现在通过 useFileDropZone → file-drop → OnFileDrop 间接依赖 wailsjs runtime。
// happy-dom 下 window.runtime 不存在,故把 OnFileDrop/OnFileDropOff 桩成 no-op,其余保持真实。
vi.mock("../../../../wailsjs/runtime/runtime", async () => {
  const actual = await vi.importActual<
    typeof import("../../../../wailsjs/runtime/runtime")
  >("../../../../wailsjs/runtime/runtime");
  return {
    ...actual,
    EventsOn: runtimeMocks.EventsOn,
    OnFileDrop: vi.fn(),
    OnFileDropOff: vi.fn(),
  };
});

import {
  ChatComposer,
  ChatTranscript,
  type ChatComposerHandle,
  type ChatTranscriptHandle,
  formatResetIn,
  formatTokens,
} from "@/components/agentre/chat";
import { ChatStreamsHost } from "@/components/agentre/chat-streams-host";
import {
  streamForMessage,
  useChatStreamsStore,
  type ChatBlockData,
} from "@/stores/chat-streams-store";
import { useLocalCommandsStore } from "@/stores/local-commands-store";
import type { chat_svc } from "../../../../wailsjs/go/models";

function renderTranscriptWithSubagent() {
  render(
    <ChatTranscript
      agentColor="agent-1"
      agentName="CEO 助手"
      messages={[
        chatMessage([
          {
            toolInput: {
              description: "probe",
              prompt: "Run echo hello and return.",
              subagent_type: "general-purpose",
            },
            toolName: "Agent",
            toolUseId: "toolu-parent",
            type: "tool_use",
            canonical: {
              kind: "agent.spawn",
              agentSpawn: {
                taskId: "toolu-parent",
                subagentType: "general-purpose",
                taskDescription: "probe",
                prompt: "Run echo hello and return.",
                toolUses: 1,
                totalTokens: 14500,
                durationMs: 7800,
                lastToolName: "Bash",
                status: "completed",
              },
            },
          } as unknown as ChatBlockData,
          {
            parentToolUseId: "toolu-parent",
            toolInput: { command: "echo hello" },
            toolName: "Bash",
            toolUseId: "toolu-child",
            type: "tool_use",
          } as ChatBlockData,
          {
            parentToolUseId: "toolu-parent",
            text: "hello",
            toolUseId: "toolu-child",
            type: "tool_result",
          } as ChatBlockData,
          {
            text: "Raw output:\n```\nhello\n```",
            toolUseId: "toolu-parent",
            type: "tool_result",
          } as ChatBlockData,
        ]),
      ]}
    />,
  );
  return screen.getByRole("region", { name: /^Subagent/ });
}

function chatMessage(blocks: ChatBlockData[]): chat_svc.ChatMessage {
  return {
    blocks,
    completionTokens: 0,
    createtime: new Date("2026-05-17T10:30:00Z").getTime(),
    durationMs: 0,
    errorText: "",
    id: 1,
    model: "",
    promptTokens: 0,
    role: "assistant",
    seq: 1,
    sessionId: 1,
  } as chat_svc.ChatMessage;
}

function textMessage(
  id: number,
  role: "user" | "assistant",
  text: string,
): chat_svc.ChatMessage {
  return {
    blocks: [{ type: "text", text } as ChatBlockData],
    cachedTokens: 0,
    cacheCreationTokens: 0,
    completionTokens: 0,
    createtime: new Date("2026-05-17T10:30:00Z").getTime(),
    durationMs: 0,
    errorText: "",
    id,
    model: "",
    promptTokens: 0,
    reasoningTokens: 0,
    role,
    seq: id,
    sessionId: 1,
    totalInputTokens: 0,
  } as unknown as chat_svc.ChatMessage;
}

function sizedScrollElement(): HTMLDivElement {
  const el = document.createElement("div");
  let clientHeight = 480;
  Object.defineProperty(el, "clientHeight", {
    configurable: true,
    get: () => clientHeight,
  });
  Object.defineProperty(el, "__setClientHeightForTest", {
    configurable: true,
    value: (next: number) => {
      clientHeight = next;
    },
  });
  Object.defineProperty(el, "scrollHeight", {
    configurable: true,
    get: () => 10_000,
  });
  el.getBoundingClientRect = () =>
    ({
      bottom: 480,
      height: 480,
      left: 0,
      right: 800,
      top: 0,
      width: 800,
      x: 0,
      y: 0,
      toJSON: () => ({}),
    }) as DOMRect;
  return el;
}

function setScrollElementHeightForTest(el: HTMLElement, height: number) {
  (
    el as HTMLElement & { __setClientHeightForTest: (next: number) => void }
  ).__setClientHeightForTest(height);
}

function mockTextSelectionWithin(node: Node) {
  const range = { commonAncestorContainer: node } as Range;
  return vi.spyOn(window, "getSelection").mockReturnValue({
    anchorNode: node,
    focusNode: node,
    getRangeAt: () => range,
    isCollapsed: false,
    rangeCount: 1,
    toString: () => "selected",
  } as unknown as Selection);
}

describe("ChatComposer context meter", () => {
  // 占位文案的判据是「这次渲染真正接上了什么」,不是 backendType 查表
  // (见包内 chat-input/placeholder.ts)。宿主这边要验的是**接线**:
  // Skill 目录拉回来后有没有标成 kind: "skill"、`!` 那段有没有跟着
  // onRunCommand 走 —— 拼装规则本身由包的用例覆盖。
  function placeholderText(): string {
    return (
      screen
        .getByRole("textbox")
        .querySelector("p")
        ?.getAttribute("data-placeholder") ?? ""
    );
  }

  function stubSkillCatalog(commands: { name: string }[]) {
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
    (window as any).go = {
      app: {
        App: {
          ListAgentSkillCommands: vi.fn().mockResolvedValue({ commands }),
        },
      },
    };
  }

  it("Given a Codex agent whose skills load, When the composer is empty, Then / and $ are offered separately", async () => {
    stubSkillCatalog([{ name: "browser:browser" }]);
    render(
      <ChatComposer
        backendType="codex"
        agentId={7}
        onSubmit={() => undefined}
      />,
    );

    await waitFor(() =>
      expect(placeholderText()).toBe(
        "Type a message · / for commands · $ for skills",
      ),
    );
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
    delete (window as any).go;
  });

  it("Given a Claude Code agent whose skills load, When the composer is empty, Then / covers commands and skills in one segment", async () => {
    // claudecode 的 Skill 也走 /,包里靠 trigger 分不出命令与 Skill ——
    // 全靠这里把目录来的那批标成 kind: "skill"。
    stubSkillCatalog([{ name: "brainstorm" }]);
    render(
      <ChatComposer
        backendType="claudecode"
        agentId={7}
        onSubmit={() => undefined}
      />,
    );

    await waitFor(() =>
      expect(placeholderText()).toBe(
        "Type a message · / for commands and skills",
      ),
    );
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
    delete (window as any).go;
  });

  it("Given no local-command handler, When the composer renders, Then the placeholder does not promise !", () => {
    // 没传 onRunCommand 时 AIChatInput 会把 `!foo` **静默吞掉** —— 提示里写着、
    // 按下去没反应,比不写更糟。
    render(<ChatComposer backendType="codex" onSubmit={() => undefined} />);

    expect(placeholderText()).not.toContain("!");
  });

  it("Given a local-command handler, When the composer renders, Then the placeholder offers !", () => {
    render(
      <ChatComposer
        backendType="codex"
        onSubmit={() => undefined}
        onRunCommand={() => undefined}
      />,
    );

    expect(placeholderText()).toBe(
      "Type a message · / for commands · ! to run in terminal",
    );
  });

  it("submits an image-only message with image data URLs", async () => {
    const onSubmit = vi.fn();
    const { container } = render(<ChatComposer onSubmit={onSubmit} />);
    const input = container.querySelector(
      'input[type="file"]',
    ) as HTMLInputElement;
    const file = new File([new Uint8Array([1, 2, 3])], "shot.png", {
      type: "image/png",
    });

    fireEvent.change(input, { target: { files: [file] } });

    await waitFor(() => {
      expect(screen.getByAltText("shot.png")).toBeInTheDocument();
    });

    fireEvent.click(screen.getByRole("button", { name: "Send" }));

    await waitFor(() => {
      expect(onSubmit).toHaveBeenCalledWith({
        text: "",
        images: [
          {
            dataUrl: "data:image/png;base64,AQID",
            mediaType: "image/png",
            name: "shot.png",
          },
        ],
      });
    });
  });

  it("Given only an image attachment, When Enter is pressed, Then it submits without placeholder text", async () => {
    const onSubmit = vi.fn();
    const { container } = render(<ChatComposer onSubmit={onSubmit} />);
    const input = container.querySelector(
      'input[type="file"]',
    ) as HTMLInputElement;
    const file = new File([new Uint8Array([1, 2, 3])], "shot.png", {
      type: "image/png",
    });

    fireEvent.change(input, { target: { files: [file] } });
    await waitFor(() => {
      expect(screen.getByAltText("shot.png")).toBeInTheDocument();
    });

    fireEvent.keyDown(screen.getByRole("textbox"), { key: "Enter" });

    await waitFor(() => {
      expect(onSubmit).toHaveBeenCalledWith({
        text: "",
        images: [
          {
            dataUrl: "data:image/png;base64,AQID",
            mediaType: "image/png",
            name: "shot.png",
          },
        ],
      });
    });
  });

  it("Given an image on the clipboard, When it is pasted into the composer, Then it is added as an attachment", async () => {
    const onSubmit = vi.fn();
    render(<ChatComposer onSubmit={onSubmit} />);
    const editor = screen.getByRole("textbox");
    const file = new File([new Uint8Array([1, 2, 3])], "clip.png", {
      type: "image/png",
    });

    fireEvent.paste(editor, {
      clipboardData: {
        files: [file],
        items: [
          {
            kind: "file",
            type: "image/png",
            getAsFile: () => file,
          },
        ],
      },
    });

    await waitFor(() => {
      expect(screen.getByAltText("clip.png")).toBeInTheDocument();
    });

    fireEvent.click(screen.getByRole("button", { name: "Send" }));

    await waitFor(() => {
      expect(onSubmit).toHaveBeenCalledWith({
        text: "",
        images: [
          {
            dataUrl: "data:image/png;base64,AQID",
            mediaType: "image/png",
            name: "clip.png",
          },
        ],
      });
    });
  });

  it("Given too many images on the clipboard, When they are pasted, Then the composer rejects the paste", async () => {
    const onSubmit = vi.fn();
    render(<ChatComposer onSubmit={onSubmit} />);
    const editor = screen.getByRole("textbox");
    const files = Array.from(
      { length: 5 },
      (_, idx) =>
        new File([new Uint8Array([idx])], `clip-${idx}.png`, {
          type: "image/png",
        }),
    );

    fireEvent.paste(editor, {
      clipboardData: {
        files,
        items: files.map((file) => ({
          kind: "file",
          type: file.type,
          getAsFile: () => file,
        })),
      },
    });

    expect(await screen.findByRole("alert")).toHaveTextContent(
      "Add at most 4 images",
    );
    expect(screen.queryByAltText("clip-0.png")).not.toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "Send" }));
    expect(onSubmit).not.toHaveBeenCalled();
  });

  it("rejects unsupported image attachments before submit", async () => {
    const onSubmit = vi.fn();
    const { container } = render(<ChatComposer onSubmit={onSubmit} />);
    const input = container.querySelector(
      'input[type="file"]',
    ) as HTMLInputElement;
    Object.defineProperty(input, "value", {
      configurable: true,
      value: "bad-file",
      writable: true,
    });

    fireEvent.change(input, {
      target: {
        files: [
          new File(["hello"], "note.txt", {
            type: "text/plain",
          }),
        ],
      },
    });

    expect(await screen.findByRole("alert")).toHaveTextContent(
      "Only PNG, JPEG, and WebP are supported. Each image must be under 5 MB.",
    );
    expect(input.value).toBe("");
    fireEvent.click(screen.getByRole("button", { name: "Send" }));
    expect(onSubmit).not.toHaveBeenCalled();
  });

  it("hides image attachment controls when image input is unsupported", () => {
    const { container } = render(
      <ChatComposer onSubmit={() => undefined} supportsImageInput={false} />,
    );

    expect(
      screen.queryByRole("button", { name: "Add Image" }),
    ).not.toBeInTheDocument();
    expect(container.querySelector('input[type="file"]')).toBeNull();
  });

  it("does not accept or submit image attachments while disabled", async () => {
    const onSubmit = vi.fn();
    const { container } = render(<ChatComposer disabled onSubmit={onSubmit} />);
    const input = container.querySelector(
      'input[type="file"]',
    ) as HTMLInputElement;
    const file = new File([new Uint8Array([1, 2, 3])], "blocked.png", {
      type: "image/png",
    });

    expect(screen.getByRole("button", { name: "Add Image" })).toBeDisabled();
    fireEvent.change(input, { target: { files: [file] } });
    await new Promise((resolve) => setTimeout(resolve, 0));
    expect(screen.queryByAltText("blocked.png")).not.toBeInTheDocument();

    fireEvent.click(screen.getByRole("button", { name: "Send" }));
    expect(onSubmit).not.toHaveBeenCalled();
  });

  it("renders warning-level context usage with defined waiting color tokens", () => {
    render(
      <ChatComposer
        contextUsage={{ used: 206000, max: 258000 }}
        onSubmit={() => undefined}
      />,
    );

    expect(screen.getByText("206k")).toBeInTheDocument();
    expect(screen.getByText("258k")).toBeInTheDocument();
    const percent = screen.getByText("80%");
    expect(percent).toHaveClass("text-status-waiting");

    const progress = screen.getByRole("progressbar");
    const fill = progress.firstElementChild;
    expect(fill).toHaveClass("bg-status-waiting");
    expect(fill).toHaveStyle({ width: "80%" });
  });
});

describe("ChatComposer imperative draft restore", () => {
  it("Given a send failure, When restoreDraft is called, Then the text and image attachments come back", async () => {
    const ref = React.createRef<ChatComposerHandle>();
    render(<ChatComposer ref={ref} onSubmit={vi.fn()} />);
    const image = {
      dataUrl: "data:image/png;base64,AQID",
      mediaType: "image/png",
      name: "restored.png",
    };

    act(() => {
      ref.current?.restoreDraft("restored draft", [image]);
    });

    // 文本回到编辑器
    expect(screen.getByRole("textbox")).toHaveTextContent("restored draft");
    // 图片附件恢复
    expect(await screen.findByAltText("restored.png")).toBeInTheDocument();
  });

  it("Given a restored draft, When clearDraft is called, Then text and images are cleared", async () => {
    const ref = React.createRef<ChatComposerHandle>();
    render(<ChatComposer ref={ref} onSubmit={vi.fn()} />);
    const image = {
      dataUrl: "data:image/png;base64,AQID",
      mediaType: "image/png",
      name: "clear.png",
    };
    act(() => {
      ref.current?.restoreDraft("to clear", [image]);
    });
    expect(await screen.findByAltText("clear.png")).toBeInTheDocument();

    act(() => {
      ref.current?.clearDraft();
    });

    expect(screen.queryByAltText("clear.png")).toBeNull();
    expect(screen.getByRole("textbox")).not.toHaveTextContent("to clear");
  });
});

describe("ChatTranscript local command lifecycle controls", () => {
  it("Given ChatPanel supplies a stop callback, When a local-command row renders, Then the card delegates its terminal id through transcript context", () => {
    const onStopLocalCommand = vi.fn();
    useLocalCommandsStore.getState().start({
      id: "terminal-local-1",
      sessionId: 1,
      command: "sleep 30",
      createdAt: 1,
    });
    try {
      render(
        <ChatTranscript
          agentColor="agent-1"
          agentName="A"
          messages={[]}
          onStopLocalCommand={onStopLocalCommand}
          sessionId={1}
        />,
      );

      fireEvent.click(screen.getByRole("button", { name: /停止|Stop/ }));
      expect(onStopLocalCommand).toHaveBeenCalledTimes(1);
      expect(onStopLocalCommand).toHaveBeenCalledWith("terminal-local-1");
    } finally {
      useLocalCommandsStore.setState({ entries: {} });
    }
  });
});

describe("ChatTranscript image blocks", () => {
  it("renders persisted image blocks", () => {
    render(
      <ChatTranscript
        agentColor="agent-1"
        agentName="CEO 助手"
        messages={[
          chatMessage([
            {
              type: "image",
              image: {
                dataUrl: "data:image/png;base64,AQID",
                mediaType: "image/png",
                name: "shot.png",
              },
            } as unknown as ChatBlockData,
          ]),
        ]}
      />,
    );

    const image = screen.getByRole("img", { name: "shot.png" });
    expect(image).toHaveAttribute("src", "data:image/png;base64,AQID");
  });
});

describe("ChatTranscript source device pill (R17)", () => {
  // 测试 mock 里 RemoteDeviceFingerprint 返回 "sha256:test-local-device"(wailsApp.ts)。
  const LOCAL_FP = "sha256:test-local-device";

  it("renders the source pill after the role label for a foreign user message", async () => {
    render(
      <ChatTranscript
        agentColor="agent-1"
        agentName="CEO 助手"
        messages={[
          {
            ...textMessage(1, "user", "跑吧"),
            sourceDevice: "sha256:other-device",
            sourceDeviceName: "iPhone",
          } as chat_svc.ChatMessage,
          textMessage(2, "assistant", "已追加"),
        ]}
      />,
    );
    expect(await screen.findByText("From iPhone")).toBeTruthy();
  });

  it("falls back to the fingerprint when no device name is carried", async () => {
    render(
      <ChatTranscript
        agentColor="agent-1"
        agentName="CEO 助手"
        messages={[
          {
            ...textMessage(1, "user", "跑吧"),
            sourceDevice: "sha256:other-device",
          } as chat_svc.ChatMessage,
        ]}
      />,
    );
    expect(await screen.findByText("From sha256:other-device")).toBeTruthy();
  });

  it("never renders the pill for this device's own messages (single-client zero change)", async () => {
    render(
      <ChatTranscript
        agentColor="agent-1"
        agentName="CEO 助手"
        messages={[
          {
            ...textMessage(1, "user", "跑吧"),
            sourceDevice: LOCAL_FP,
            sourceDeviceName: "iPhone",
          } as chat_svc.ChatMessage,
          textMessage(2, "assistant", "已追加"),
        ]}
      />,
    );
    expect(screen.queryByText(/^From /)).toBeNull();
    await act(async () => {}); // 等指纹 Promise 落定再确认一次没有药丸
    expect(screen.queryByText(/^From /)).toBeNull();
  });

  it("renders no pill without any source (single-client default)", async () => {
    render(
      <ChatTranscript
        agentColor="agent-1"
        agentName="CEO 助手"
        messages={[textMessage(1, "user", "跑吧")]}
      />,
    );
    expect(screen.queryByText(/^From /)).toBeNull();
    await act(async () => {});
    expect(screen.queryByText(/^From /)).toBeNull();
  });

  // R18:浏览器在空闲会话上「开新一轮」跑起的一轮,user 行带发起方设备名到达转录 ——
  // 渲染层复用同一枚 inline pill,不新造控件;本机消息零变化由上面两个守卫锁住。
  it("renders the source pill for a browser-initiated round and keeps the user row before the assistant", async () => {
    render(
      <ChatTranscript
        agentColor="agent-1"
        agentName="CEO 助手"
        messages={[
          {
            ...textMessage(1, "user", "帮我跑一下测试"),
            sourceDevice: "sha256:web-device",
            sourceDeviceName: "Chrome · macOS",
          } as chat_svc.ChatMessage,
          textMessage(2, "assistant", "好,正在跑"),
        ]}
      />,
    );
    expect(await screen.findByText("From Chrome · macOS")).toBeTruthy();
    const body = document.body.textContent ?? "";
    expect(body.indexOf("帮我跑一下测试")).toBeLessThan(
      body.indexOf("好,正在跑"),
    );
  });
});

describe("ChatTranscript autonomous turn banner", () => {
  function msg(
    id: number,
    role: "user" | "assistant",
    text: string,
  ): chat_svc.ChatMessage {
    return {
      blocks: [{ type: "text", text } as ChatBlockData],
      completionTokens: 0,
      createtime: new Date("2026-05-17T10:30:00Z").getTime(),
      durationMs: 0,
      errorText: "",
      id,
      model: "",
      promptTokens: 0,
      role,
      seq: id,
      sessionId: 1,
    } as chat_svc.ChatMessage;
  }

  // emptyMsg:轮刚起、内容还没落地的 assistant 行(autonomous_turn.go / Send 建行时
  // BlocksJSON 恒为 "[]")。与 noticeMsg 的区别正是判定要认的那条界:没有块 ≠ 块全是
  // notice。
  function emptyMsg(id: number): chat_svc.ChatMessage {
    return {
      blocks: [] as ChatBlockData[],
      completionTokens: 0,
      createtime: new Date("2026-05-17T10:30:00Z").getTime(),
      durationMs: 0,
      errorText: "",
      id,
      model: "",
      promptTokens: 0,
      role: "assistant",
      seq: id,
      sessionId: 1,
    } as chat_svc.ChatMessage;
  }

  // noticeMsg:供应商切换/回退 notice 的落库形状——独立的 assistant 消息,blocks 只有
  // 一个 type: "notice" 块(session_provider.go 的 encodeProviderSwitch 同源同形)。
  function noticeMsg(id: number): chat_svc.ChatMessage {
    return {
      blocks: [
        {
          level: "info",
          noticeKind: "switch",
          providerKey: "session-key",
          providerName: "中转 · GLM 5.2",
          type: "notice",
        } as ChatBlockData,
      ],
      completionTokens: 0,
      createtime: new Date("2026-05-17T10:30:00Z").getTime(),
      durationMs: 0,
      errorText: "",
      id,
      model: "",
      promptTokens: 0,
      role: "assistant",
      seq: id,
      sessionId: 1,
    } as chat_svc.ChatMessage;
  }

  it("自主续轮(assistant 紧邻前一条 assistant,无 user 行)前渲染 AutoTriggerBanner", () => {
    render(
      <ChatTranscript
        agentColor="agent-1"
        agentName="CEO 助手"
        messages={[
          msg(1, "user", "后台跑 sleep 10 完成后看目录"),
          msg(2, "assistant", "已在后台启动"),
          msg(3, "assistant", "当前目录如下…"),
        ]}
      />,
    );
    // 仅 msg(3) 是自主续轮 → 恰好一条 banner;msg(2) 紧邻 user 不挂。
    // (无 compact boundary,banner 是唯一的 role=separator。)
    const banners = screen.getAllByRole("separator");
    expect(banners).toHaveLength(1);
    expect(banners[0]).toHaveTextContent(/auto-continued|自动继续/);
  });

  it("每条 assistant 都紧跟 user 时不渲染 banner", () => {
    render(
      <ChatTranscript
        agentColor="agent-1"
        agentName="CEO 助手"
        messages={[
          msg(1, "user", "hi"),
          msg(2, "assistant", "hello"),
          msg(3, "user", "again"),
          msg(4, "assistant", "ok"),
        ]}
      />,
    );
    expect(screen.queryAllByRole("separator")).toHaveLength(0);
  });

  it("供应商切换 notice 行本身不触发 banner,且不吞掉它之后紧跟的真实自主续轮", () => {
    const { container } = render(
      <ChatTranscript
        agentColor="agent-1"
        agentName="CEO 助手"
        messages={[
          msg(1, "user", "后台跑吧，完了叫我"),
          msg(2, "assistant", "后台任务我先跑着，完了叫你。"),
          noticeMsg(3),
          msg(4, "assistant", "后台任务跑完了，结果是……"),
        ]}
      />,
    );
    // notice 行(msg 3)在判定里透明:它自己不算自主轮 —— 它所在的行里没有 banner。
    const noticeRow = container.querySelector('[data-message-id="3"]');
    expect(noticeRow).not.toBeNull();
    expect(
      within(noticeRow as HTMLElement).queryByRole("separator"),
    ).toBeNull();
    // msg(4) 紧邻的前一条真实消息仍是 assistant(notice 被跳过)→ 真自主续轮的 banner
    // 必须照常出现在 msg(4) 的行里(决策 3 的反向边界,mockup「R2 的反向边界」)。
    const realRow = container.querySelector('[data-message-id="4"]');
    expect(realRow).not.toBeNull();
    expect(
      within(realRow as HTMLElement).getByRole("separator"),
    ).toHaveTextContent(/auto-continued|自动继续/);
    // 全局也应恰好一条 banner,不多不少。
    expect(screen.getAllByRole("separator")).toHaveLength(1);
  });

  it("供应商切换 notice 之后接正常 user→assistant 轮不产生 banner", () => {
    render(
      <ChatTranscript
        agentColor="agent-1"
        agentName="CEO 助手"
        messages={[
          msg(1, "user", "hi"),
          msg(2, "assistant", "先切个供应商"),
          noticeMsg(3),
          msg(4, "user", "继续"),
          msg(5, "assistant", "好的"),
        ]}
      />,
    );
    expect(screen.queryAllByRole("separator")).toHaveLength(0);
  });

  it("notice 垫在 user 与它的 assistant 回复之间,不把这一轮误判成自主续轮", () => {
    // 决策 3 的另一半:notice 行「在判断其它消息的『紧邻前一条』时被跳过」。这里
    // msg(3) 紧邻的前一条**真实**消息是 msg(1) 这条 user —— 正常轮,不该出 banner。
    // 真实成因:用户发完消息、assistant 还没落地时在 pill 上切了供应商,切换 notice
    // 就落在两者中间(NextSeq 排在在途 assistant 之后同理)。notice 若不透明,它会
    // 顶替 user 成为「紧邻前一条」,把一条普通轮误判成自主续轮 —— 正是 R2 的形状。
    render(
      <ChatTranscript
        agentColor="agent-1"
        agentName="CEO 助手"
        messages={[
          msg(1, "user", "帮我查一下"),
          noticeMsg(2),
          msg(3, "assistant", "好的,我查到……"),
        ]}
      />,
    );
    expect(screen.queryAllByRole("separator")).toHaveLength(0);
  });

  it("自主续轮刚起、内容还没落地(blocks 为空)时横幅就该在", () => {
    // 决策 3 认的是「内容块**全部是 notice**」,空 blocks 不是 notice 行 —— 这条守的
    // 是判定别把「还没有内容的消息」一并吞掉。真实形状:autonomous_turn.go 建自主轮
    // assistant 行时 BlocksJSON 恒为 "[]",chat-panel 的 autonomous_started 分支立刻
    // 把这条空消息插进 messages,横幅正要在这一刻出现,而不是等第一段文字流完。
    const { container } = render(
      <ChatTranscript
        agentColor="agent-1"
        agentName="CEO 助手"
        messages={[
          msg(1, "user", "后台跑吧，完了叫我"),
          msg(2, "assistant", "后台任务我先跑着，完了叫你。"),
          emptyMsg(3),
        ]}
      />,
    );
    const pendingRow = container.querySelector('[data-message-id="3"]');
    expect(pendingRow).not.toBeNull();
    expect(
      within(pendingRow as HTMLElement).getByRole("separator"),
    ).toHaveTextContent(/auto-continued|自动继续/);
  });
});

describe("ChatTranscript virtualization", () => {
  it("Given many messages, When rendered in a scroll container, Then it mounts only the visible window", async () => {
    const scrollElement = sizedScrollElement();
    const messages = Array.from({ length: 240 }, (_, idx) =>
      textMessage(
        idx + 1,
        idx % 2 === 0 ? "user" : "assistant",
        `message-${idx + 1}`,
      ),
    );

    render(
      <ChatTranscript
        agentColor="agent-1"
        agentName="A"
        messages={messages}
        scrollElement={scrollElement}
      />,
    );

    expect(await screen.findByText("message-1")).toBeInTheDocument();
    expect(document.querySelectorAll("[data-message-id]").length).toBeLessThan(
      12,
    );
    expect(screen.queryByText("message-240")).toBeNull();
  });

  it("Given a target message is outside the mounted window, When scrollToMessage is called, Then it asks the virtual list to reveal it", async () => {
    const scrollElement = sizedScrollElement();
    const scrollTo = vi.fn();
    scrollElement.scrollTo = scrollTo;
    const ref = React.createRef<ChatTranscriptHandle>();
    const messages = Array.from({ length: 240 }, (_, idx) =>
      textMessage(
        idx + 1,
        idx % 2 === 0 ? "user" : "assistant",
        `jump-message-${idx + 1}`,
      ),
    );

    render(
      <ChatTranscript
        ref={ref}
        agentColor="agent-1"
        agentName="A"
        messages={messages}
        scrollElement={scrollElement}
      />,
    );

    act(() => {
      ref.current?.scrollToMessage(220);
    });

    await waitFor(() => {
      expect(scrollTo).toHaveBeenCalled();
    });
  });

  it("Given an anchor message id, When scrollToAnchor is called, Then it returns true and scrolls toward that message; an unknown id returns false and does not scroll", async () => {
    const scrollElement = sizedScrollElement();
    const ref = React.createRef<ChatTranscriptHandle>();
    const messages = Array.from({ length: 120 }, (_, idx) =>
      textMessage(
        idx + 1,
        idx % 2 === 0 ? "user" : "assistant",
        `anchor-msg-${idx + 1}`,
      ),
    );

    render(
      <ChatTranscript
        ref={ref}
        agentColor="agent-1"
        agentName="A"
        messages={messages}
        scrollElement={scrollElement}
        virtualize
      />,
    );
    await screen.findByText("anchor-msg-1");

    // 已加载的锚点消息:scrollToAnchor 返回 true 并把滚动钉向该消息(深处 → scrollTop>0)。
    let movedOk: boolean | undefined;
    act(() => {
      movedOk = ref.current?.scrollToAnchor(80, 0);
    });
    expect(movedOk).toBe(true);
    expect(scrollElement.scrollTop).toBeGreaterThan(0);

    // 不在 displayMessages 的 id:返回 false 且不滚动(调用方据此回退像素恢复)。
    scrollElement.scrollTop = 0;
    let missingOk: boolean | undefined;
    act(() => {
      missingOk = ref.current?.scrollToAnchor(999_999, 0);
    });
    expect(missingOk).toBe(false);
    expect(scrollElement.scrollTop).toBe(0);
  });

  it("Given a virtualized transcript is hidden after being visible, When its tab becomes active again, Then the visible window is measured again", async () => {
    const scrollElement = sizedScrollElement();
    const messages = Array.from({ length: 120 }, (_, idx) =>
      textMessage(
        idx + 1,
        idx % 2 === 0 ? "user" : "assistant",
        `active-message-${idx + 1}`,
      ),
    );

    const { rerender } = render(
      <ChatTranscript
        active
        agentColor="agent-1"
        agentName="A"
        messages={messages}
        scrollElement={scrollElement}
        virtualize
      />,
    );

    expect(await screen.findByText("active-message-1")).toBeInTheDocument();
    setScrollElementHeightForTest(scrollElement, 0);
    rerender(
      <ChatTranscript
        active={false}
        agentColor="agent-1"
        agentName="A"
        messages={messages}
        scrollElement={scrollElement}
        virtualize
      />,
    );
    expect(document.querySelectorAll("[data-message-id]")).toHaveLength(0);

    setScrollElementHeightForTest(scrollElement, 480);
    rerender(
      <ChatTranscript
        active
        agentColor="agent-1"
        agentName="A"
        messages={messages}
        scrollElement={scrollElement}
        virtualize
      />,
    );

    expect(await screen.findByText("active-message-1")).toBeInTheDocument();
    expect(document.querySelectorAll("[data-message-id]").length).toBeLessThan(
      80,
    );
  });

  it("Given a virtualized transcript has a visible window, When the tab is hidden, Then message rows unmount while scroll state is retained", async () => {
    const scrollElement = sizedScrollElement();
    const messages = Array.from({ length: 120 }, (_, idx) =>
      textMessage(
        idx + 1,
        idx % 2 === 0 ? "user" : "assistant",
        `hidden-message-${idx + 1}`,
      ),
    );

    const { rerender } = render(
      <ChatTranscript
        active
        agentColor="agent-1"
        agentName="A"
        messages={messages}
        scrollElement={scrollElement}
        virtualize
      />,
    );

    expect(await screen.findByText("hidden-message-1")).toBeInTheDocument();
    setScrollElementHeightForTest(scrollElement, 0);
    rerender(
      <ChatTranscript
        active={false}
        agentColor="agent-1"
        agentName="A"
        messages={messages}
        scrollElement={scrollElement}
        virtualize
      />,
    );

    expect(screen.queryByText("hidden-message-1")).toBeNull();
    expect(document.querySelectorAll("[data-message-id]")).toHaveLength(0);
  });

  it("Given a virtualized transcript was scrolled away from the top, When its tab is hidden and restored, Then it keeps the same scroll window", async () => {
    const scrollElement = sizedScrollElement();
    const messages = Array.from({ length: 160 }, (_, idx) =>
      textMessage(
        idx + 1,
        idx % 2 === 0 ? "user" : "assistant",
        `restore-message-${idx + 1}`,
      ),
    );

    const { rerender } = render(
      <ChatTranscript
        active
        agentColor="agent-1"
        agentName="A"
        messages={messages}
        scrollElement={scrollElement}
        virtualize
      />,
    );

    await screen.findByText("restore-message-1");
    act(() => {
      scrollElement.scrollTop = 4_200;
      fireEvent.scroll(scrollElement);
    });
    await waitFor(() => {
      expect(screen.queryByText("restore-message-1")).toBeNull();
    });
    const visibleBeforeHide = Array.from(
      document.querySelectorAll("[data-message-id]"),
    ).map((node) => node.getAttribute("data-message-id"));

    setScrollElementHeightForTest(scrollElement, 0);
    act(() => {
      scrollElement.scrollTop = 0;
    });
    rerender(
      <ChatTranscript
        active={false}
        agentColor="agent-1"
        agentName="A"
        messages={messages}
        scrollElement={scrollElement}
        virtualize
      />,
    );

    setScrollElementHeightForTest(scrollElement, 480);
    rerender(
      <ChatTranscript
        active
        agentColor="agent-1"
        agentName="A"
        messages={messages}
        scrollElement={scrollElement}
        virtualize
      />,
    );

    await waitFor(() => {
      expect(screen.queryByText("restore-message-1")).toBeNull();
      expect(scrollElement.scrollTop).toBe(4_200);
    });
    expect(
      Array.from(document.querySelectorAll("[data-message-id]")).map((node) =>
        node.getAttribute("data-message-id"),
      ),
    ).toEqual(visibleBeforeHide);
  });

  it("Given a tool row is expanded, When its row unmounts and returns, Then the expanded state is preserved", async () => {
    const scrollElement = sizedScrollElement();
    const messages = Array.from({ length: 160 }, (_, idx) => {
      const id = idx + 1;
      if (id === 2) {
        return {
          ...textMessage(id, "assistant", ""),
          blocks: [
            {
              toolInput: { command: "echo persistent-state" },
              toolName: "Bash",
              toolUseId: "toolu-persist",
              type: "tool_use",
            } as ChatBlockData,
            {
              // 多行结果:折叠的活动行对多行结果只报规模,所以「结果正文可见」
              // 确实证明这一行被展开过,而不是行尾那段单行预览。
              text: "persistent-state\npersistent-state-tail",
              toolUseId: "toolu-persist",
              type: "tool_result",
            } as ChatBlockData,
          ],
        } as chat_svc.ChatMessage;
      }
      return textMessage(
        id,
        id % 2 === 0 ? "assistant" : "user",
        `state-message-${id}`,
      );
    });

    const { rerender } = render(
      <ChatTranscript
        agentColor="agent-1"
        agentName="A"
        messages={messages}
        scrollElement={scrollElement}
        virtualize
      />,
    );

    const row = await screen.findByTestId("activity-row");
    fireEvent.click(row);
    expect(screen.getByText(/persistent-state-tail/)).toBeInTheDocument();

    rerender(
      <ChatTranscript
        agentColor="agent-1"
        agentName="A"
        messages={messages.filter((m) => m.id !== 2)}
        scrollElement={scrollElement}
        virtualize
      />,
    );
    expect(screen.queryByTestId("activity-row")).toBeNull();

    rerender(
      <ChatTranscript
        agentColor="agent-1"
        agentName="A"
        messages={messages}
        scrollElement={scrollElement}
        virtualize
      />,
    );
    const remountedRow = await screen.findByTestId("activity-row");

    expect(remountedRow).toHaveAttribute("aria-expanded", "true");
    expect(screen.getByText(/persistent-state-tail/)).toBeInTheDocument();
  });

  it("Given a visible row contains many tool rows, When the tab is hidden, Then tool row DOM unmounts", async () => {
    const scrollElement = sizedScrollElement();
    const toolBlocks = Array.from({ length: 40 }, (_, idx) => {
      const toolUseId = `toolu-heavy-${idx + 1}`;
      return [
        // 每次调用前先说一句 —— 正文打断活动块聚合,于是这 40 次调用仍是 40 行
        // 独立的活动行(单条不成组),测的还是「一条消息内按行窗口挂载」。
        {
          text: `narration-${idx + 1}`,
          type: "text",
        } as ChatBlockData,
        {
          toolInput: { command: `echo heavy-${idx + 1}` },
          toolName: "Bash",
          toolUseId,
          type: "tool_use",
        } as ChatBlockData,
        {
          text: `heavy-result-${idx + 1}`,
          toolUseId,
          type: "tool_result",
        } as ChatBlockData,
      ];
    }).flat();
    const messages = [
      {
        ...textMessage(1, "assistant", ""),
        blocks: toolBlocks,
      } as chat_svc.ChatMessage,
    ];

    const { rerender } = render(
      <ChatTranscript
        active
        agentColor="agent-1"
        agentName="A"
        messages={messages}
        scrollElement={scrollElement}
        virtualize
      />,
    );

    // 行级虚拟化后,可见态只挂视口窗口内的行(40 行全挂正是被修掉的 bug);
    // 本测试关注的是「隐藏后全部卸载」,可见态只断言窗口语义。
    await waitFor(() => {
      const mounted = screen.getAllByTestId("activity-row").length;
      expect(mounted).toBeGreaterThan(0);
      expect(mounted).toBeLessThan(40);
    });

    setScrollElementHeightForTest(scrollElement, 0);
    rerender(
      <ChatTranscript
        active={false}
        agentColor="agent-1"
        agentName="A"
        messages={messages}
        scrollElement={scrollElement}
        virtualize
      />,
    );

    expect(screen.queryByTestId("activity-row")).toBeNull();
    expect(document.querySelectorAll("[data-message-id]")).toHaveLength(0);
  });
});

// 行级虚拟化核心验收:单条消息内部的 tool 卡也按视口窗口挂载/卸载,
// 这是「切 tab 卡顿(单 message 数百 tool 卡击穿 per-message 虚拟化)」的修复本体。
describe("ChatTranscript block-level virtualization", () => {
  // 一条消息 = count 次「说一句 + 调一次工具」。正文打断活动块聚合(Hard
  // invariant 4 / 决策 5),所以每次调用仍是独立的一行活动行 —— 这正是行级
  // 虚拟化要窗口化的形态。连续调用会折成一个活动块,那种形态由下面单独一条
  // 用例钉住(折叠态只出一行、一步都不 mount)。
  //
  // 结果刻意多行:折叠的活动行对多行结果只报规模(「N 行」),所以下面「点开
  // 才看得到结果正文」的断言测的是展开,不是行尾那段单行预览。
  function manyToolMessage(id: number, count: number): chat_svc.ChatMessage {
    const blocks = Array.from({ length: count }, (_, idx) => {
      const toolUseId = `toolu-big-${idx + 1}`;
      return [
        {
          text: `big-say-${idx + 1}`,
          type: "text",
        } as ChatBlockData,
        {
          toolInput: { command: `echo big-${idx + 1}` },
          toolName: "Bash",
          toolUseId,
          type: "tool_use",
        } as ChatBlockData,
        {
          text: `filler\nbig-result-${idx + 1}`,
          toolUseId,
          type: "tool_result",
        } as ChatBlockData,
      ];
    }).flat();
    return {
      ...textMessage(id, "assistant", ""),
      blocks,
    } as chat_svc.ChatMessage;
  }

  function toolPairBlocks(count: number): ChatBlockData[] {
    return Array.from({ length: count }, (_, idx) => {
      const toolUseId = `toolu-agg-${idx + 1}`;
      return [
        {
          toolInput: { command: `echo agg-${idx + 1}` },
          toolName: "Bash",
          toolUseId,
          type: "tool_use",
        } as ChatBlockData,
        {
          text: `agg-result-${idx + 1}`,
          toolUseId,
          type: "tool_result",
        } as ChatBlockData,
      ];
    }).flat();
  }

  it("Given one message with 300 consecutive tool pairs, When rendered, Then they collapse into a single activity row that mounts no step DOM", async () => {
    const scrollElement = sizedScrollElement();

    render(
      <ChatTranscript
        active
        agentColor="agent-1"
        agentName="A"
        messages={[
          {
            ...textMessage(1, "assistant", ""),
            blocks: toolPairBlocks(300),
          } as chat_svc.ChatMessage,
        ]}
        scrollElement={scrollElement}
        virtualize
      />,
    );

    const header = await screen.findByTestId("activity-header");
    expect(header).toHaveTextContent("300 steps");
    expect(document.querySelectorAll("[data-row-key]")).toHaveLength(1);
    // 折叠态一步都不 mount —— 300 步的结果文本不进 DOM(Hard invariant 9)。
    expect(screen.queryByTestId("raw-tool-card")).toBeNull();
    expect(screen.queryByTestId("activity-row")).toBeNull();
    expect(screen.queryByText("agg-result-1")).toBeNull();
  });

  it("Given one message with 300 tool pairs, When rendered in a 480px viewport, Then only the visible row window mounts", async () => {
    const scrollElement = sizedScrollElement();

    render(
      <ChatTranscript
        active
        agentColor="agent-1"
        agentName="A"
        messages={[manyToolMessage(1, 300)]}
        scrollElement={scrollElement}
        virtualize
      />,
    );

    await screen.findAllByTestId("activity-row");
    const mounted = screen.getAllByTestId("activity-row").length;
    expect(mounted).toBeGreaterThan(0);
    expect(mounted).toBeLessThan(60);
  });

  it("Given the 300-tool message, When a visible row is expanded, Then it shows its paired tool result", async () => {
    const scrollElement = sizedScrollElement();

    render(
      <ChatTranscript
        active
        agentColor="agent-1"
        agentName="A"
        messages={[manyToolMessage(1, 300)]}
        scrollElement={scrollElement}
        virtualize
      />,
    );

    const rows = await screen.findAllByTestId("activity-row");
    expect(screen.queryByText(/big-result-1$/)).toBeNull();
    fireEvent.click(rows[0]);
    expect(screen.getByText(/big-result-1$/)).toBeInTheDocument();
  });

  it("Given a row expanded inside a long message, When scrolled away and back, Then the row unmounts and returns expanded", async () => {
    const scrollElement = sizedScrollElement();

    render(
      <ChatTranscript
        active
        agentColor="agent-1"
        agentName="A"
        messages={[manyToolMessage(1, 300)]}
        scrollElement={scrollElement}
        virtualize
      />,
    );

    const rows = await screen.findAllByTestId("activity-row");
    fireEvent.click(rows[0]);
    expect(screen.getByText(/big-result-1$/)).toBeInTheDocument();

    // 滚到消息深处:同一条消息内的首行应被虚拟卸载 —— 这正是 message 级
    // 虚拟化做不到的(整条消息一行,行内永不细分)。
    act(() => {
      scrollElement.scrollTop = 8_000;
      fireEvent.scroll(scrollElement);
    });
    await waitFor(() => {
      expect(screen.queryByText(/big-result-1$/)).toBeNull();
    });

    act(() => {
      scrollElement.scrollTop = 0;
      fireEvent.scroll(scrollElement);
    });
    const first = await screen.findAllByTestId("activity-row");
    expect(first[0]).toHaveAttribute("aria-expanded", "true");
    expect(screen.getByText(/big-result-1$/)).toBeInTheDocument();
  });

  // 单条不成组:被正文夹住的一次调用不套「1 步」的壳,也不退回成一张等重的
  // 工具卡 —— 一条 assistant 消息只由「正文 / 活动块 / 出组卡片 / 脚注」组成。
  it("Given a lone tool call between two paragraphs, When rendered, Then it is a bare activity row rather than a tool card", async () => {
    const scrollElement = sizedScrollElement();

    render(
      <ChatTranscript
        active
        agentColor="agent-1"
        agentName="A"
        messages={[
          {
            ...textMessage(1, "assistant", ""),
            blocks: [
              { text: "先看一眼", type: "text" } as ChatBlockData,
              ...toolPairBlocks(1),
              { text: "看完了", type: "text" } as ChatBlockData,
            ],
          } as chat_svc.ChatMessage,
        ]}
        scrollElement={scrollElement}
        virtualize
      />,
    );

    const row = await screen.findByTestId("activity-row");
    expect(screen.queryByTestId("activity-header")).toBeNull();
    expect(screen.queryByTestId("raw-tool-card")).toBeNull();
    expect(within(row).getByTestId("activity-name")).toHaveTextContent("Bash");
  });

  it("Given a streaming message whose last block is a tool, When rendered, Then the typing indicator mounts with that message", async () => {
    const scrollElement = sizedScrollElement();

    render(
      <ChatTranscript
        liveByMessageId={new Map([[2, {}]])}
        active
        agentColor="agent-1"
        agentName="A"
        messages={[textMessage(1, "user", "go"), manyToolMessage(2, 3)]}
        scrollElement={scrollElement}
        streaming
        virtualize
      />,
    );

    const indicator = await screen.findByLabelText("Generating");
    expect(
      indicator.closest("[data-message-id]")?.getAttribute("data-message-id"),
    ).toBe("2");
  });

  it("perf probe: a 356-tool message mounts only the window, never the full step list", async () => {
    // 常驻性能验收:硬断言挂载数(窗口语义),耗时只打日志不做墙钟断言(CI 抖动)。
    // 重构前同场景为 356 卡全挂、同步渲染 ~136ms。
    const scrollElement = sizedScrollElement();
    const t0 = performance.now();

    render(
      <ChatTranscript
        active
        agentColor="agent-1"
        agentName="A"
        messages={[manyToolMessage(1, 356)]}
        scrollElement={scrollElement}
        virtualize
      />,
    );
    await screen.findAllByTestId("activity-row");

    const elapsed = performance.now() - t0;
    const mountedSteps = screen.getAllByTestId("activity-row").length;
    const mountedRows = document.querySelectorAll("[data-row-key]").length;
    // 防「步骤没挂但行壳全挂」的假优化:行 wrapper 数与步骤数同界。
    expect(mountedSteps).toBeGreaterThan(0);
    expect(mountedSteps).toBeLessThan(40);
    expect(mountedRows).toBeLessThan(40);
    console.info(
      `[perf-probe] 356-tool message: ${mountedSteps} steps / ${mountedRows} rows mounted in ${elapsed.toFixed(1)}ms`,
    );
  });

  // 行模型下流式 tool 卡从「live 行内生长(resize 钉底)」变成「行追加」,而
  // followOnAppend 因 wrong-restore 刻意不开 —— 贴底跟随由 ChatTranscript 自己的
  // follow effect 补上。这两条测试钉住:贴底才追、上滑不抢。
  function liveToolBlocks(n: number): ChatBlockData[] {
    return Array.from({ length: n }, (_, idx) => [
      // 每次调用前的一句正文打断聚合,于是流式里每多一次调用就真的多出行 ——
      // 连续调用会长进同一个活动块(同一行),那种形态测不到「追加行」。
      { text: `live-say-${idx + 1}`, type: "text" } as ChatBlockData,
      {
        toolInput: { command: `echo live-${idx + 1}` },
        toolName: "Bash",
        toolUseId: `toolu-live-${idx + 1}`,
        type: "tool_use",
      } as ChatBlockData,
    ]).flat();
  }

  it("Given the transcript is at the bottom, When a live tool row is appended, Then it follows to the new end", async () => {
    const scrollElement = sizedScrollElement();
    const scrollTo = vi.fn();
    scrollElement.scrollTo = scrollTo;
    const messages = [
      textMessage(1, "user", "go"),
      {
        ...textMessage(2, "assistant", ""),
        blocks: [] as ChatBlockData[],
      } as chat_svc.ChatMessage,
    ];

    const { rerender } = render(
      <ChatTranscript
        liveByMessageId={new Map([[2, { liveBlocks: liveToolBlocks(1) }]])}
        active
        agentColor="agent-1"
        agentName="A"
        messages={messages}
        scrollElement={scrollElement}
        streaming
        virtualize
      />,
    );
    await screen.findAllByTestId("activity-row");
    scrollTo.mockClear();

    rerender(
      <ChatTranscript
        liveByMessageId={new Map([[2, { liveBlocks: liveToolBlocks(2) }]])}
        active
        agentColor="agent-1"
        agentName="A"
        messages={messages}
        scrollElement={scrollElement}
        streaming
        virtualize
      />,
    );

    await waitFor(() => {
      expect(scrollTo).toHaveBeenCalled();
    });
  });

  it("Given a row key deep inside a long message, When scrollToAnchor targets it, Then it pins that row instead of the message top", async () => {
    const scrollElement = sizedScrollElement();
    const ref = React.createRef<ChatTranscriptHandle>();

    render(
      <ChatTranscript
        ref={ref}
        active
        agentColor="agent-1"
        agentName="A"
        messages={[manyToolMessage(1, 300)]}
        scrollElement={scrollElement}
        virtualize
      />,
    );
    await screen.findAllByTestId("activity-row");

    // 行级锚点:钉到消息深处第 200 次调用那一行,而不是消息首行。
    let okDeep: boolean | undefined;
    act(() => {
      okDeep = ref.current?.scrollToAnchor(
        1,
        0,
        "message:1:activity:tool:toolu-big-200",
      );
    });
    expect(okDeep).toBe(true);
    expect(scrollElement.scrollTop).toBeGreaterThan(5_000);

    // rowKey 失效(行已消失/旧快照)→ 回退该消息首行。
    let okFallback: boolean | undefined;
    act(() => {
      okFallback = ref.current?.scrollToAnchor(1, 0, "message:1:gone");
    });
    expect(okFallback).toBe(true);
    expect(scrollElement.scrollTop).toBeLessThan(100);

    // 消息不存在 → false(调用方回退像素恢复),语义不变。
    let missing: boolean | undefined;
    act(() => {
      missing = ref.current?.scrollToAnchor(999_999, 0, "message:999999:x");
    });
    expect(missing).toBe(false);
  });

  it("Given the user scrolled away from the bottom, When a live tool row is appended, Then the scroll position is left alone", async () => {
    const scrollElement = sizedScrollElement();
    const scrollTo = vi.fn();
    scrollElement.scrollTo = scrollTo;
    // 300 个 tool 行 → totalSize 远超视口;scrollTop=0 等价于用户上滑读历史。
    const messages = [manyToolMessage(2, 300)];

    const { rerender } = render(
      <ChatTranscript
        liveByMessageId={new Map([[2, { liveBlocks: [] }]])}
        active
        agentColor="agent-1"
        agentName="A"
        messages={messages}
        scrollElement={scrollElement}
        streaming
        virtualize
      />,
    );
    await screen.findAllByTestId("activity-row");
    scrollTo.mockClear();

    rerender(
      <ChatTranscript
        liveByMessageId={new Map([[2, { liveBlocks: liveToolBlocks(1) }]])}
        active
        agentColor="agent-1"
        agentName="A"
        messages={messages}
        scrollElement={scrollElement}
        streaming
        virtualize
      />,
    );

    expect(scrollTo).not.toHaveBeenCalled();
  });
});

// 特征化测试:钉住 transcript 行模型重构会触碰、但此前无直接覆盖的现状行为
// (ErrorCard / RetryNoticeCard / 虚拟化路径下的 indicator·banner·空占位行)。
describe("ChatTranscript message tail attachments", () => {
  it("Given an assistant message with errorText, When rendered, Then the ErrorCard offers regenerate and continue actions", () => {
    const calls: number[] = [];
    const continueCalls: number[] = [];
    const failed = {
      ...textMessage(7, "assistant", "partial output"),
      errorText: "api timeout",
    } as chat_svc.ChatMessage;

    render(
      <ChatTranscript
        agentColor="agent-1"
        agentName="A"
        messages={[failed]}
        onContinue={(messageId) => continueCalls.push(messageId)}
        onRerun={(messageId) => calls.push(messageId)}
      />,
    );

    expect(
      screen.getByText("Agent call failed: api timeout"),
    ).toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: /Regenerate/ }));
    expect(calls).toEqual([7]);
    fireEvent.click(screen.getByRole("button", { name: "Continue" }));
    expect(continueCalls).toEqual([7]);
  });

  it("Given an errorText assistant on the virtualized path, When its row mounts, Then the ErrorCard mounts with it", async () => {
    const scrollElement = sizedScrollElement();
    const failed = {
      ...textMessage(2, "assistant", "partial"),
      errorText: "boom",
    } as chat_svc.ChatMessage;

    render(
      <ChatTranscript
        active
        agentColor="agent-1"
        agentName="A"
        messages={[textMessage(1, "user", "hi"), failed]}
        scrollElement={scrollElement}
        virtualize
      />,
    );

    expect(
      await screen.findByText("Agent call failed: boom"),
    ).toBeInTheDocument();
  });

  it("Given a live retry notice, When rendered on the live target, Then the RetryNoticeCard is visible", () => {
    render(
      <ChatTranscript
        liveByMessageId={
          new Map([
            [
              2,
              {
                liveRetry: {
                  attempt: 2,
                  maxAttempts: 5,
                  message: "overloaded",
                  details: "",
                  at: new Date("2026-05-18T10:00:00Z").getTime(),
                },
              },
            ],
          ])
        }
        agentColor="agent-1"
        agentName="A"
        messages={[
          textMessage(1, "user", "hi"),
          {
            ...textMessage(2, "assistant", ""),
            blocks: [] as ChatBlockData[],
          } as chat_svc.ChatMessage,
        ]}
        streaming
      />,
    );

    expect(
      screen.getByRole("status", { name: "Retrying" }),
    ).toBeInTheDocument();
    expect(screen.getByText("Retrying 2/5")).toBeInTheDocument();
  });

  it("Given a streaming empty assistant placeholder on the virtualized path, When rendered, Then its row and the typing indicator mount", async () => {
    const scrollElement = sizedScrollElement();
    render(
      <ChatTranscript
        active
        agentColor="agent-1"
        agentName="A"
        messages={[
          textMessage(1, "user", "go"),
          {
            ...textMessage(2, "assistant", ""),
            blocks: [] as ChatBlockData[],
          } as chat_svc.ChatMessage,
        ]}
        scrollElement={scrollElement}
        streaming
        virtualize
      />,
    );

    expect(await screen.findByLabelText("Generating")).toBeInTheDocument();
    expect(document.querySelector('[data-message-id="2"]')).not.toBeNull();
  });

  it("Given an empty-blocks assistant without streaming on the virtualized path, When rendered, Then it still mounts a row", async () => {
    const scrollElement = sizedScrollElement();
    render(
      <ChatTranscript
        active
        agentColor="agent-1"
        agentName="A"
        messages={[
          textMessage(1, "user", "go"),
          {
            ...textMessage(2, "assistant", ""),
            blocks: [] as ChatBlockData[],
          } as chat_svc.ChatMessage,
        ]}
        scrollElement={scrollElement}
        virtualize
      />,
    );

    await screen.findByText("go");
    expect(document.querySelector('[data-message-id="2"]')).not.toBeNull();
  });

  it("Given an autonomous assistant turn on the virtualized path, When rendered, Then exactly one AutoTriggerBanner mounts", async () => {
    const scrollElement = sizedScrollElement();
    render(
      <ChatTranscript
        active
        agentColor="agent-1"
        agentName="A"
        messages={[
          textMessage(1, "user", "后台跑 sleep 10 完成后看目录"),
          textMessage(2, "assistant", "已在后台启动"),
          textMessage(3, "assistant", "当前目录如下…"),
        ]}
        scrollElement={scrollElement}
        virtualize
      />,
    );

    await screen.findByText("当前目录如下…");
    expect(screen.getAllByRole("separator")).toHaveLength(1);
  });
});

describe("formatTokens", () => {
  it("小于 1000 原样输出", () => {
    expect(formatTokens(0)).toBe("0");
    expect(formatTokens(999)).toBe("999");
  });

  it("[1e3, 1e6) 走 k 档: 商 >=100 取整, 否则一位小数", () => {
    expect(formatTokens(1_000)).toBe("1.0k");
    expect(formatTokens(12_340)).toBe("12.3k");
    expect(formatTokens(120_500)).toBe("121k");
    expect(formatTokens(999_000)).toBe("999k");
  });

  it(">=1e6 走 M 档: 商 >=10 取整, 否则一位小数", () => {
    expect(formatTokens(1_000_000)).toBe("1M");
    expect(formatTokens(1_200_000)).toBe("1.2M");
    expect(formatTokens(9_900_000)).toBe("9.9M");
    expect(formatTokens(10_000_000)).toBe("10M");
    expect(formatTokens(12_500_000)).toBe("13M");
  });

  it("k 档取整到 1000 就进 M: 1000k 在任何输入下都不出现", () => {
    // 999_999 按量级本该落 k 档, 但商四舍五入后是 1000 —— 那正是本轮要消灭的字符串。
    expect(formatTokens(999_999)).toBe("1M");
    expect(formatTokens(999_500)).toBe("1M");
    // 边界另一侧: 取整后还是 999, 仍留在 k 档。
    expect(formatTokens(999_499)).toBe("999k");
    expect(formatTokens(1_000_000)).toBe("1M");
  });
});

describe("formatResetIn", () => {
  const now = Date.parse("2026-05-28T00:00:00Z");

  it("4d21h 后重置 → '4d21h'", () => {
    const target = new Date(now + (4 * 24 + 21) * 3_600_000).toISOString();
    expect(formatResetIn(target, now)).toBe("4d21h");
  });

  it("整 4 天后重置 → '4d'(省略 0h)", () => {
    const target = new Date(now + 4 * 24 * 3_600_000).toISOString();
    expect(formatResetIn(target, now)).toBe("4d");
  });

  it("3 小时后重置 → '3h'", () => {
    const target = new Date(now + 3 * 3_600_000).toISOString();
    expect(formatResetIn(target, now)).toBe("3h");
  });

  it("不到 1 小时(40min) → '40m'", () => {
    const target = new Date(now + 40 * 60_000).toISOString();
    expect(formatResetIn(target, now)).toBe("40m");
  });

  it("不到 1 分钟但尚未重置 → '1m'", () => {
    const target = new Date(now + 30_000).toISOString();
    expect(formatResetIn(target, now)).toBe("1m");
  });

  it("已经过期 → '0m'", () => {
    const target = new Date(now - 60_000).toISOString();
    expect(formatResetIn(target, now)).toBe("0m");
  });

  it("空 / null / 非法值 → ''", () => {
    expect(formatResetIn(null, now)).toBe("");
    expect(formatResetIn(undefined, now)).toBe("");
    expect(formatResetIn("", now)).toBe("");
    expect(formatResetIn("not-a-date", now)).toBe("");
  });

  it("Date 实例也兼容", () => {
    const target = new Date(now + 25 * 3_600_000);
    expect(formatResetIn(target, now)).toBe("1d1h");
  });
});

describe("ChatComposer quota meter", () => {
  const resetNow = Date.parse("2026-05-28T00:00:00Z");

  it("不渲染 QuotaMeter 当 quotaUsage 未传", () => {
    render(<ChatComposer onSubmit={() => undefined} />);
    expect(screen.queryByLabelText(/Claude.*quota/)).toBeNull();
  });

  it("不渲染 QuotaMeter 当 reason='no_credentials' (API key 用户)", () => {
    render(
      <ChatComposer
        onSubmit={() => undefined}
        quotaUsage={{ reason: "no_credentials", fetchedAtMs: 1 } as never}
      />,
    );
    expect(screen.queryByLabelText(/Claude.*quota/)).toBeNull();
  });

  it("正常渲染百分比文本 当 reason='ok'", () => {
    render(
      <ChatComposer
        onSubmit={() => undefined}
        quotaUsage={
          {
            reason: "ok",
            data: { fiveHourPercent: 42.6, weeklyPercent: 18.2 },
            fetchedAtMs: 1,
          } as never
        }
      />,
    );
    // 前缀与数值分处两个 span(供窄屏单独隐藏前缀), 故按整块文本断言。
    const meter = screen.getByLabelText(/Claude.*quota/);
    expect(meter).toHaveTextContent("5h 43%");
    expect(meter).toHaveTextContent("7d 18%");
  });

  it("stale=true 时仍显示上次数字, 但不渲染可见的 stale 角标", () => {
    render(
      <ChatComposer
        onSubmit={() => undefined}
        quotaUsage={
          {
            reason: "rate_limited",
            stale: true,
            data: { fiveHourPercent: 30, weeklyPercent: 10 },
            fetchedAtMs: 1,
          } as never
        }
      />,
    );
    const meter = screen.getByLabelText(/Claude.*quota/);
    expect(meter).toHaveTextContent("5h 30%");
    expect(meter).toHaveTextContent("7d 10%");
    expect(screen.queryByText(/stale/)).toBeNull();
  });

  it("HoverCard 面板展示 5h 重置还剩多少分钟(取代原生 title)", () => {
    vi.useFakeTimers();
    vi.setSystemTime(resetNow);
    try {
      render(
        <ChatComposer
          onSubmit={() => undefined}
          quotaUsage={
            {
              reason: "ok",
              data: {
                fiveHourPercent: 42,
                weeklyPercent: 18,
                fiveHourResetsAt: new Date(resetNow + 40 * 60_000),
              },
              fetchedAtMs: 1,
            } as never
          }
        />,
      );
      const trigger = screen.getByLabelText(/Claude.*quota/);
      // 触发区可聚焦, 聚焦即展开面板(键盘用户也能读到详情)。
      expect(trigger).not.toHaveAttribute("title");
      act(() => {
        fireEvent.focusIn(trigger);
        // Radix 的 open 走 openDelay 定时器, fake timers 下必须推进才会展开。
        vi.advanceTimersByTime(500);
      });
      expect(screen.getByText(/resets in 40m/)).toBeInTheDocument();
    } finally {
      vi.useRealTimers();
    }
  });

  it("auth_expired 时渲染占位文本而不是数字", () => {
    render(
      <ChatComposer
        onSubmit={() => undefined}
        quotaUsage={{ reason: "auth_expired", fetchedAtMs: 1 } as never}
      />,
    );
    expect(screen.getByLabelText(/Claude.*quota/)).toHaveTextContent("5h —%");
  });
});

describe("ChatTranscript message meta", () => {
  function assistantWithUsage(): chat_svc.ChatMessage {
    return {
      blocks: [{ type: "text", text: "hi" } as ChatBlockData],
      cacheCreationTokens: 11,
      cachedTokens: 22,
      completionTokens: 50,
      createtime: new Date("2026-05-17T10:30:00Z").getTime(),
      durationMs: 1200,
      errorText: "",
      id: 7,
      model: "claude-sonnet-4-6",
      promptTokens: 100,
      reasoningTokens: 33,
      role: "assistant",
      seq: 1,
      sessionId: 1,
    } as chat_svc.ChatMessage;
  }

  it("renders prompt/completion as inline arrow counters and exposes a tooltip trigger", () => {
    render(
      <ChatTranscript
        agentColor="agent-1"
        agentName="CEO 助手"
        messages={[assistantWithUsage()]}
        onRerun={() => undefined}
      />,
    );

    const trigger = screen.getByRole("button", { name: "Token usage details" });
    expect(trigger).toHaveTextContent("claude-sonnet-4-6");
    expect(trigger).toHaveTextContent("100");
    expect(trigger).toHaveTextContent("50");
    expect(trigger).toHaveTextContent("1.2s");
    expect(within(trigger).queryByText("tokens")).not.toBeInTheDocument();
  });

  it("renders the meta strip below the message, always visible without hover gating", () => {
    // 之前用 group-hover / React state 控制 meta 显隐，在 Wails WebKit 下
    // 多次出现 meta 一直亮起的 bug。现在改成常驻显示，靠 text-muted-foreground
    // + text-2xs 自身弱化样式，不再依赖任何 hover/focus 状态。
    render(
      <ChatTranscript
        agentColor="agent-1"
        agentName="CEO 助手"
        messages={[assistantWithUsage()]}
        onRerun={() => undefined}
      />,
    );

    const trigger = screen.getByRole("button", { name: "Token usage details" });
    const metaContainer = trigger.parentElement!.parentElement!;
    const tokens = metaContainer.className.split(/\s+/);

    expect(tokens).not.toContain("opacity-0");
    expect(tokens).not.toContain("opacity-100");
    expect(metaContainer.className).not.toMatch(/transition-opacity/);
    expect(metaContainer.className).not.toMatch(/group-hover/);
    expect(metaContainer.className).not.toMatch(/focus-visible/);
    expect(metaContainer.className).toContain("text-meta");
    expect(metaContainer.className).toContain("text-muted-foreground");
  });

  it("renders the content column under max-w-measure inside the article", () => {
    // 历史上是 760px;统一与 ToolCall / ApprovalGate / ErrorCard 一致为 720px,
    // 现在统一走 --container-measure token(max-w-measure),
    // 避免三种 max-w 在 transcript 里错位形成阶梯式 dead space。
    render(
      <ChatTranscript
        agentColor="agent-1"
        agentName="CEO 助手"
        messages={[assistantWithUsage()]}
        onRerun={() => undefined}
      />,
    );

    const trigger = screen.getByRole("button", { name: "Token usage details" });
    const contentColumn = trigger.parentElement!.parentElement!.parentElement!;

    expect(contentColumn.tagName).toBe("DIV");
    expect(contentColumn.className).toMatch(/max-w-measure/);
    expect(contentColumn.parentElement!.tagName).toBe("ARTICLE");
  });

  it("labels the rerun action as 重新生成 and passes the target message id to onRerun", () => {
    const calls: number[] = [];
    render(
      <ChatTranscript
        agentColor="agent-1"
        agentName="CEO 助手"
        messages={[assistantWithUsage()]}
        onRerun={(messageId) => calls.push(messageId)}
      />,
    );

    expect(screen.queryByText("重跑")).not.toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: /Regenerate/ }));
    expect(calls).toEqual([7]);
  });

  it("renders 重新生成 on every assistant message, not just the last one", () => {
    // 历史中段也要能重跑：上一轮设计只在最后一条挂按钮，现在每条都要。
    const olderAssistant = {
      ...assistantWithUsage(),
      id: 3,
      seq: 1,
    } as chat_svc.ChatMessage;
    const newerAssistant = {
      ...assistantWithUsage(),
      id: 9,
      seq: 3,
    } as chat_svc.ChatMessage;

    const clicks: number[] = [];
    render(
      <ChatTranscript
        agentColor="agent-1"
        agentName="CEO 助手"
        messages={[olderAssistant, newerAssistant]}
        onRerun={(messageId) => clicks.push(messageId)}
      />,
    );

    const buttons = screen.getAllByRole("button", { name: /Regenerate/ });
    expect(buttons).toHaveLength(2);
    fireEvent.click(buttons[0]);
    fireEvent.click(buttons[1]);
    expect(clicks).toEqual([3, 9]);
  });

  // claude/codex 后端走 CLI 自身 login（llmProviderKey 为空）或绑了 provider 但 Model
  // 字段留空时，落库的 assistantMsg.Model 是空串。之前 chat.tsx 用 `m.model` 作
  // 门槛把整个 meta 行藏掉，连耗时和「重新生成」按钮一起没了。门槛改成
  // durationMs > 0（turn 完成的可靠信号）后这些会话也能正常显示 meta。
  it("shows the meta row with rerun button when model is empty but the turn completed", () => {
    const noModelAssistant = {
      ...assistantWithUsage(),
      model: "",
    } as chat_svc.ChatMessage;

    render(
      <ChatTranscript
        agentColor="agent-1"
        agentName="CEO 助手"
        messages={[noModelAssistant]}
        onRerun={() => undefined}
      />,
    );

    expect(
      screen.getByRole("button", { name: /Regenerate/ }),
    ).toBeInTheDocument();
    expect(
      screen.getByRole("button", { name: "Token usage details" }),
    ).toHaveTextContent("1.2s");
  });

  it("hides the model chip text when model is empty so no empty span shows", () => {
    const noModelAssistant = {
      ...assistantWithUsage(),
      model: "",
    } as chat_svc.ChatMessage;

    render(
      <ChatTranscript
        agentColor="agent-1"
        agentName="CEO 助手"
        messages={[noModelAssistant]}
        onRerun={() => undefined}
      />,
    );

    const trigger = screen.getByRole("button", { name: "Token usage details" });
    expect(trigger).not.toHaveTextContent("claude-sonnet-4-6");
    // 第一个 token chip 应该紧贴左边、不带 leading 「·」 分隔符
    expect(trigger.textContent ?? "").not.toMatch(/^\s*·/);
  });

  it("Given an assistant message with multiple output sections, When copying AI output, Then only the last section is written to clipboard", async () => {
    const writeText = vi.fn().mockResolvedValue(undefined);
    Object.defineProperty(navigator, "clipboard", {
      configurable: true,
      value: { writeText },
    });
    const assistant = {
      ...assistantWithUsage(),
      blocks: [
        { type: "text", text: "## Plan\n\n- keep **markdown**" },
        { type: "thinking", text: "private chain" },
        { type: "text", text: "\n\n```ts\nconst ok = true;\n```" },
      ] as ChatBlockData[],
    } as chat_svc.ChatMessage;

    render(
      <ChatTranscript
        agentColor="agent-1"
        agentName="CEO 助手"
        messages={[assistant]}
        onRerun={() => undefined}
      />,
    );

    fireEvent.click(screen.getByRole("button", { name: "Copy AI output" }));

    await waitFor(() => {
      expect(writeText).toHaveBeenCalledWith(
        "\n\n```ts\nconst ok = true;\n```",
      );
    });
    expect(sonnerMocks.toast.success).toHaveBeenCalledWith(
      "AI output copied",
      expect.any(Object),
    );
  });

  it("Given an assistant message without text output, When rendered, Then no copy AI output button is shown", () => {
    const assistant = {
      ...assistantWithUsage(),
      blocks: [
        { type: "thinking", text: "still reasoning" },
      ] as ChatBlockData[],
    } as chat_svc.ChatMessage;

    render(
      <ChatTranscript
        agentColor="agent-1"
        agentName="CEO 助手"
        messages={[assistant]}
        onRerun={() => undefined}
      />,
    );

    expect(
      screen.queryByRole("button", { name: "Copy AI output" }),
    ).not.toBeInTheDocument();
  });

  it("Given the last assistant output section has no text, When rendered, Then earlier text cannot be copied", () => {
    const assistant = {
      ...assistantWithUsage(),
      blocks: [
        { type: "text", text: "outdated section" },
        { type: "thinking", text: "still reasoning" },
      ] as ChatBlockData[],
    } as chat_svc.ChatMessage;

    render(
      <ChatTranscript
        agentColor="agent-1"
        agentName="CEO 助手"
        messages={[assistant]}
        onRerun={() => undefined}
      />,
    );

    expect(
      screen.queryByRole("button", { name: "Copy AI output" }),
    ).not.toBeInTheDocument();
  });
});

describe("ChatTranscript typing indicator", () => {
  function userMessage(id: number, text: string): chat_svc.ChatMessage {
    return {
      blocks: [{ type: "text", text } as ChatBlockData],
      completionTokens: 0,
      createtime: new Date("2026-05-18T10:00:00Z").getTime(),
      durationMs: 0,
      errorText: "",
      id,
      model: "",
      promptTokens: 0,
      role: "user",
      seq: id,
      sessionId: 1,
    } as chat_svc.ChatMessage;
  }

  function assistantMessage(
    id: number,
    blocks: ChatBlockData[],
  ): chat_svc.ChatMessage {
    return {
      blocks,
      completionTokens: 0,
      createtime: new Date("2026-05-18T10:00:00Z").getTime(),
      durationMs: 0,
      errorText: "",
      id,
      model: "",
      promptTokens: 0,
      role: "assistant",
      seq: id,
      sessionId: 1,
    } as chat_svc.ChatMessage;
  }

  it("shows the indicator on the empty assistant placeholder when streaming", () => {
    render(
      <ChatTranscript
        agentColor="agent-1"
        agentName="CEO 助手"
        messages={[userMessage(1, "hi"), assistantMessage(2, [])]}
        streaming
      />,
    );

    expect(screen.getByLabelText("Generating")).toBeInTheDocument();
  });

  it("轮中切换供应商追加的 notice 行不抢走生成指示器", () => {
    // 供应商 pill 允许在轮中切换,切换成功后 chat-panel 立刻 reloadSession(),把新落库
    // 的 notice 消息(role=assistant、只有一个 notice 块)拉进 messages —— 它排在在跑
    // 的 assistant 之后,成了 messages 的末条 assistant。指示器宿主必须仍是真正在流的
    // 那条,否则三个点会跳到 notice 行上、在跑的那条看着像已经停了。
    render(
      <ChatTranscript
        liveByMessageId={new Map([[2, { liveTail: "streaming chunk" }]])}
        agentColor="agent-1"
        agentName="CEO 助手"
        messages={[
          userMessage(1, "hi"),
          assistantMessage(2, []),
          assistantMessage(3, [
            {
              level: "info",
              noticeKind: "switch",
              providerKey: "session-key",
              providerName: "中转 · GLM 5.2",
              type: "notice",
            } as ChatBlockData,
          ]),
        ]}
        streaming
      />,
    );

    const indicator = screen.getByLabelText("Generating");
    expect(
      indicator.closest("[data-message-id]")?.getAttribute("data-message-id"),
    ).toBe("2");
  });

  it("places the indicator after the live tail text in DOM order", () => {
    render(
      <ChatTranscript
        liveByMessageId={new Map([[2, { liveTail: "streaming chunk" }]])}
        agentColor="agent-1"
        agentName="CEO 助手"
        messages={[userMessage(1, "hi"), assistantMessage(2, [])]}
        streaming
      />,
    );

    const indicator = screen.getByLabelText("Generating");
    const tail = screen.getByText("streaming chunk");
    expect(
      tail.compareDocumentPosition(indicator) &
        Node.DOCUMENT_POSITION_FOLLOWING,
    ).toBeTruthy();
  });

  it("renders multi-block live streaming text fully within one markdown-body", () => {
    // 流式尾巴跨多个 block(段落 + 空行 + 段落)时,增量渲染路径不能丢内容,
    // 且所有 block 落在同一个 .markdown-body 容器里(间距与一次性解析一致)。
    render(
      <ChatTranscript
        liveByMessageId={
          new Map([[2, { liveTail: "committed para\n\ngrowing tail" }]])
        }
        agentColor="agent-1"
        agentName="CEO 助手"
        messages={[userMessage(1, "hi"), assistantMessage(2, [])]}
        streaming
      />,
    );

    const committed = screen.getByText("committed para");
    const tail = screen.getByText("growing tail");
    expect(committed.closest(".markdown-body")).not.toBeNull();
    expect(tail.closest(".markdown-body")).toBe(
      committed.closest(".markdown-body"),
    );
  });

  it("does not render the indicator when streaming is false", () => {
    render(
      <ChatTranscript
        agentColor="agent-1"
        agentName="CEO 助手"
        messages={[userMessage(1, "hi"), assistantMessage(2, [])]}
      />,
    );

    expect(screen.queryByLabelText("Generating")).not.toBeInTheDocument();
  });

  it("does not render the indicator when the trailing message is a user one", () => {
    render(
      <ChatTranscript
        agentColor="agent-1"
        agentName="CEO 助手"
        messages={[
          assistantMessage(1, [
            { type: "text", text: "old reply" } as ChatBlockData,
          ]),
          userMessage(2, "follow up"),
        ]}
        streaming
      />,
    );

    expect(screen.queryByLabelText("Generating")).not.toBeInTheDocument();
  });

  it("renders the indicator only on the last assistant message", () => {
    render(
      <ChatTranscript
        agentColor="agent-1"
        agentName="CEO 助手"
        messages={[
          assistantMessage(1, [
            { type: "text", text: "first" } as ChatBlockData,
          ]),
          assistantMessage(2, [
            { type: "text", text: "second" } as ChatBlockData,
          ]),
        ]}
        streaming
      />,
    );

    const indicators = screen.getAllByLabelText("Generating");
    expect(indicators).toHaveLength(1);
    const second = screen.getByText("second");
    expect(second.closest("article")).toContainElement(indicators[0]);
  });
});

describe("ChatTranscript thinking blocks", () => {
  function assistantMsg(
    id: number,
    blocks: ChatBlockData[],
  ): chat_svc.ChatMessage {
    return {
      blocks,
      completionTokens: 0,
      createtime: new Date("2026-05-18T10:00:00Z").getTime(),
      durationMs: 0,
      errorText: "",
      id,
      model: "",
      promptTokens: 0,
      role: "assistant",
      seq: id,
      sessionId: 1,
    } as chat_svc.ChatMessage;
  }

  it("renders a persisted thinking block as a collapsed thought row, not a card", () => {
    // 已落定的思考是活动块的组内成员;这里它落单,于是「单条不成组」直接就是
    // 那一行活动行(不再是一张与工具等重的整卡)。
    const reasoning = "Plan: check A then B.";
    render(
      <ChatTranscript
        agentColor="agent-1"
        agentName="CEO 助手"
        messages={[
          assistantMsg(1, [
            { text: reasoning, type: "thinking" } as ChatBlockData,
            { type: "text", text: "结果是 42" } as ChatBlockData,
          ]),
        ]}
      />,
    );

    const row = screen.getByTestId("activity-row");
    expect(within(row).getByTestId("activity-name")).toHaveTextContent(
      "Thought",
    );
    expect(within(row).getByText(`${reasoning.length} chars`)).toBeVisible();
    expect(row).toHaveAttribute("aria-expanded", "false");
    // 折叠是收起,不是藏:正文点开就在。
    expect(screen.queryByText(reasoning)).toBeNull();
    fireEvent.click(row);
    expect(screen.getByText(reasoning)).toBeInTheDocument();
  });

  it("renders liveThinking as a streaming thinking card on the live target", () => {
    render(
      <ChatTranscript
        liveByMessageId={new Map([[2, { liveThinking: "正在分析问题…" }]])}
        agentColor="agent-1"
        agentName="CEO 助手"
        messages={[assistantMsg(2, [])]}
        streaming
      />,
    );

    expect(screen.getByText("Thinking…")).toBeInTheDocument();
    expect(screen.getByText("正在分析问题…")).toBeInTheDocument();
  });

  it("renders current-round liveThinking AFTER liveBlocks (chronological order)", () => {
    // 回归 guard:liveBlocks 已含前一轮冻结的 tool_use 时,当前轮未冻结的
    // liveThinking 是它的「第 2 轮思考」,必须排在工具卡之后 —— 否则工具循环里
    // 后几轮的思考会被全堆到最顶(「思考完成过程都在最顶部叠加」)。
    render(
      <ChatTranscript
        liveByMessageId={
          new Map([
            [
              2,
              {
                liveThinking: "再看一下结果",
                liveBlocks: [
                  {
                    toolInput: { path: "." },
                    toolName: "ls",
                    toolUseId: "call_x",
                    type: "tool_use",
                  } as ChatBlockData,
                ],
              },
            ],
          ])
        }
        agentColor="agent-1"
        agentName="CEO 助手"
        messages={[assistantMsg(2, [])]}
        streaming
      />,
    );

    // liveTail 空 → 当前轮思考仍在进行,文案是「Thinking…」;它排在那次调用之后。
    const thinking = screen.getByText("Thinking…");
    const tool = screen.getByTestId("activity-row");
    expect(
      tool.compareDocumentPosition(thinking) & Node.DOCUMENT_POSITION_FOLLOWING,
    ).toBeTruthy();
  });

  // 回归 guard:活动块后面开始流一段思考时,那一组必须**保持展开**。流式思考按
  // 设计不进组(它是那一刻唯一的实时表面),于是它会短暂地排在活动块后面 ——
  // 但它一结束就并回同一个块。把「后面多了一行」读成「这一组已经落定」就会自动
  // 收起,用户看到的是「思考中整组收起、思考完又展开」的来回抖动。
  it("keeps the activity block expanded while a thinking stream follows it", () => {
    render(
      <ChatTranscript
        liveByMessageId={
          new Map([
            [
              2,
              {
                liveThinking: "再看一下结果",
                liveBlocks: [
                  {
                    toolInput: { command: "echo 1" },
                    toolName: "Bash",
                    toolUseId: "call_1",
                    type: "tool_use",
                  } as ChatBlockData,
                  {
                    toolInput: { command: "echo 2" },
                    toolName: "Bash",
                    toolUseId: "call_2",
                    type: "tool_use",
                  } as ChatBlockData,
                ],
              },
            ],
          ])
        }
        agentColor="agent-1"
        agentName="CEO 助手"
        messages={[assistantMsg(2, [])]}
        streaming
      />,
    );

    expect(screen.getByText("Thinking…")).toBeInTheDocument();
    expect(screen.getByTestId("activity-header")).toHaveAttribute(
      "aria-expanded",
      "true",
    );
    expect(screen.getAllByTestId("activity-row")).toHaveLength(2);
  });

  it("liveThinking joins the activity as a done thought row when text deltas start (liveDelta non-empty)", () => {
    render(
      <ChatTranscript
        liveByMessageId={
          new Map([[2, { liveTail: "结果是", liveThinking: "正在分析问题…" }]])
        }
        agentColor="agent-1"
        agentName="CEO 助手"
        messages={[assistantMsg(2, [])]}
        streaming
      />,
    );

    // 流式的思考才是整卡;它一结束就并入活动,成为一条普通的思考行。
    expect(screen.queryByText("Thinking…")).not.toBeInTheDocument();
    expect(screen.queryByText("Thought complete")).toBeNull();
    expect(
      within(screen.getByTestId("activity-row")).getByTestId("activity-name"),
    ).toHaveTextContent("Thought");
    // Live text appears
    expect(screen.getByText("结果是")).toBeInTheDocument();
  });

  it("flushed thinking block in liveBlocks renders as a done thought row before the tool that follows it", () => {
    // 回归 guard:store 在 tool_use 边界把已完成的 liveThinking 冻成 thinking block
    // 推进 liveBlocks。已完成的思考与紧随其后的 tool_use 是一段连续活动,聚合成
    // 一个活动块(正在流的思考才是整卡);展开后它仍是一条「已完成的思考行」,
    // 且排在那次工具调用之前(时间顺序不重排)。
    render(
      <ChatTranscript
        liveByMessageId={
          new Map([
            [
              2,
              {
                liveBlocks: [
                  {
                    text: "先看一下目录结构",
                    type: "thinking",
                  } as ChatBlockData,
                  {
                    toolInput: { command: "ls" },
                    toolName: "Bash",
                    toolUseId: "call_x",
                    type: "tool_use",
                  } as ChatBlockData,
                ],
              },
            ],
          ])
        }
        agentColor="agent-1"
        agentName="CEO 助手"
        messages={[assistantMsg(2, [])]}
        streaming
      />,
    );

    // 这一组正在跑(消息仍在流式,活动块是末行)→ 自动展开,两步都在。
    expect(screen.queryByText("Thinking…")).not.toBeInTheDocument();
    const rows = screen.getAllByTestId("activity-row");
    expect(rows).toHaveLength(2);
    expect(within(rows[0]).getByTestId("activity-name")).toHaveTextContent(
      "Thought",
    );
    expect(within(rows[1]).getByTestId("activity-name")).toHaveTextContent(
      "Bash",
    );
    expect(
      rows[0].compareDocumentPosition(rows[1]) &
        Node.DOCUMENT_POSITION_FOLLOWING,
    ).toBeTruthy();
    // 思考正文只在点开那一行之后出现(折叠是收起,不是藏)。
    expect(screen.queryByText("先看一下目录结构")).toBeNull();
    fireEvent.click(rows[0]);
    expect(screen.getByText("先看一下目录结构")).toBeInTheDocument();
  });
});

describe("ChatStreamsHost subagent run identity", () => {
  it("Given run-scoped live child events, When the host stores them, Then tool use and result retain the run ID for grouped rendering", async () => {
    useChatStreamsStore.setState({ streams: new Map() });
    runtimeMocks.EventsOn.mockClear();
    useChatStreamsStore.getState().openStream({
      assistantMessageId: 1,
      name: "chat:event:7:1",
      sessionId: 7,
      streamStartedAt: 1,
    });
    render(<ChatStreamsHost />);
    await waitFor(() => expect(runtimeMocks.EventsOn).toHaveBeenCalled());
    const handler = runtimeMocks.EventsOn.mock
      .calls[0]?.[1] as unknown as (event: {
      kind: "tool_use" | "tool_result";
      toolUseId: string;
      toolName?: string;
      toolResult?: string;
      parentToolUseId: string;
      subagentRunId: string;
    }) => void;

    act(() => {
      handler({
        kind: "tool_use",
        toolUseId: "shared-call",
        toolName: "Read",
        parentToolUseId: "toolu-parent",
        subagentRunId: "run-a",
      });
      handler({
        kind: "tool_result",
        toolUseId: "shared-call",
        toolResult: "done",
        parentToolUseId: "toolu-parent",
        subagentRunId: "run-a",
      });
    });

    expect(
      streamForMessage(useChatStreamsStore.getState(), 7, 1)?.liveBlocks.map(
        (block) => block.subagentRunId,
      ),
    ).toEqual(["run-a", "run-a"]);
    useChatStreamsStore.setState({ streams: new Map() });
  });
});

describe("ChatTranscript subagent blocks", () => {
  it("marks subagent header text as copyable without making selection clicks expand it", () => {
    const card = renderTranscriptWithSubagent();
    const toggle = within(card).getAllByRole("button")[0];
    const description = within(toggle).getByText("probe");
    const textNode = description.firstChild;
    if (!textNode) throw new Error("Expected subagent description text node");
    const selection = mockTextSelectionWithin(textNode);

    expect(within(toggle).getByText("Agent")).toHaveAttribute(
      "data-copyable-control-text",
      "true",
    );
    expect(description).toHaveAttribute("data-copyable-control-text", "true");

    fireEvent.click(toggle);

    expect(toggle).toHaveAttribute("aria-expanded", "false");
    selection.mockRestore();
  });

  it("renders Agent tool as SubagentInvocationCard, hides child blocks from top level", () => {
    const card = renderTranscriptWithSubagent();
    // 头部是一行：Agent · probe + general-purpose chip + 完成态的次级信息
    // 「N 步 · token · 耗时」(成功态不再挂状态胶囊)。R8/R9 把带标签的完整
    // 工具数/tokens/耗时下沉到展开区 meta 行。
    expect(within(card).getByText("Agent")).toBeInTheDocument();
    expect(within(card).getByText("probe")).toBeInTheDocument();
    expect(within(card).getByText("general-purpose")).toBeInTheDocument();
    expect(within(card).queryByText(/last:/)).toBeNull();
    expect(within(card).queryByTestId("agent-spawn-status-pill")).toBeNull();
    expect(within(card).getByTestId("agent-spawn-outcome")).toHaveTextContent(
      "7.8s",
    );
    // 详情区(展开区 meta 行)折叠时仍常驻 DOM,只查全卡片文案测不出「头部
    // 不再显示数字」——把查询限定在头部按钮本身。
    const header = within(card).getByRole("button", { expanded: false });
    expect(within(header).queryByText(/tools/i)).toBeNull();
    expect(within(header).queryByTestId("agent-spawn-progress")).toBeNull();

    // 子 Bash 不应出现在与 Agent 同级的位置 —— 没有独立的 Bash 工具卡。
    expect(screen.queryByRole("region", { name: "Tool call Bash" })).toBeNull();

    // 完整的工具数没有从卡片里彻底丢失 —— 只是挪进了展开区 meta 行(R8)。
    fireEvent.click(within(card).getAllByRole("button")[0]);
    expect(within(card).getByTestId("agent-spawn-meta-tools").textContent).toBe(
      "1",
    );
  });

  it("expanded card lists subagent inner Bash step + final summary", () => {
    const card = renderTranscriptWithSubagent();
    fireEvent.click(within(card).getAllByRole("button")[0]);

    expect(within(card).getByText("TASK PROMPT")).toBeInTheDocument();
    expect(within(card).getByText("STEPS")).toBeInTheDocument();
    expect(within(card).getByText("SUMMARY")).toBeInTheDocument();
    expect(
      within(card).getByText("Run echo hello and return."),
    ).toBeInTheDocument();
    // Bash 子步骤出现在 STEPS 区 —— 现在是活动块里的一行(同转录的活动行),
    // 不再是各自带边框与状态胶囊的 step 卡。
    expect(within(card).getByTestId("activity-name")).toHaveTextContent("Bash");
    expect(within(card).getByText("echo hello")).toBeInTheDocument();
    // SUMMARY 区有最终文本
    expect(within(card).getByText(/Raw output:/)).toBeInTheDocument();
  });

  // Plan C: AgentSpawnCard 不读 toolInput.model — canonical.AgentSpawn 没有 model 字段。
  // 旧 SubagentInvocationCard 渲染 model chip 的逻辑已废除;前端从 canonical 取数据,
  // model 不在 wire 里就不显示。
});

describe("ChatTranscript permission + tool merge", () => {
  it("marks standalone permission summary text as copyable while keeping action buttons plain", () => {
    render(
      <ChatTranscript
        agentColor="agent-1"
        agentName="CEO 助手"
        messages={[
          chatMessage([
            {
              canonical: {
                kind: "tool.permission",
                toolPermission: {
                  requestId: "req-copy",
                  toolName: "Bash",
                  toolInput: { command: "printf hi" },
                  resolved: false,
                },
              },
              toolPermission: {
                requestId: "req-copy",
                toolName: "Bash",
                toolInput: { command: "printf hi" },
                resolved: false,
              },
              type: "tool_permission_request",
            } as unknown as ChatBlockData,
          ]),
        ]}
      />,
    );

    expect(screen.getByText("Bash")).toHaveAttribute(
      "data-copyable-control-text",
      "true",
    );
    expect(screen.getByText("printf hi")).toHaveAttribute(
      "data-copyable-control-text",
      "true",
    );
    expect(screen.getByText("Allow Once")).not.toHaveAttribute(
      "data-copyable-control-text",
    );
  });

  // Plan C: "merges resolved+allowed permission into the next matching tool_use card" +
  // "uses 'Allowed · session' badge when alwaysAllow=true" 两条特性化 ToolInvocationCard
  // header 上 Allowed badge 的测试已删除 —— 新 canonical-tool/raw/card.tsx 简化为
  // 只显示 toolName + 摘要 + 可选 overlay,不再挂 inline badge(审批信息保留在 toolBlock.
  // toolPermission sidecar,后续 RawToolCard 自行决定如何展示)。

  it("keeps denied permissions as a standalone card with no following tool_use", () => {
    render(
      <ChatTranscript
        agentColor="agent-1"
        agentName="CEO 助手"
        messages={[
          chatMessage([
            {
              canonical: {
                kind: "tool.permission",
                toolPermission: {
                  requestId: "req-3",
                  toolName: "Bash",
                  toolInput: { command: "rm -rf /" },
                  resolved: true,
                  allowed: false,
                  alwaysAllow: false,
                },
              },
              toolPermission: {
                requestId: "req-3",
                toolName: "Bash",
                toolInput: { command: "rm -rf /" },
                resolved: true,
                allowed: false,
                alwaysAllow: false,
              },
              type: "tool_permission_request",
            } as unknown as ChatBlockData,
          ]),
        ]}
      />,
    );

    // ToolPermissionCard 仍渲染 (only header 显示 toolName 和 Denied pill)
    expect(screen.getByText("Denied")).toBeInTheDocument();
    // 没有 tool_use 卡
    expect(screen.queryByRole("region", { name: "Tool call Bash" })).toBeNull();
  });

  it("keeps pending (unresolved) permissions as a standalone card", () => {
    render(
      <ChatTranscript
        agentColor="agent-1"
        agentName="CEO 助手"
        messages={[
          chatMessage([
            {
              canonical: {
                kind: "tool.permission",
                toolPermission: {
                  requestId: "req-4",
                  toolName: "Bash",
                  toolInput: { command: "ls" },
                  resolved: false,
                },
              },
              toolPermission: {
                requestId: "req-4",
                toolName: "Bash",
                toolInput: { command: "ls" },
                resolved: false,
              },
              type: "tool_permission_request",
            } as unknown as ChatBlockData,
          ]),
        ]}
      />,
    );

    // 待审批态留三个操作按钮,confirm 卡片确实出现。
    expect(screen.getByText("Allow Once")).toBeInTheDocument();
    expect(screen.getByText("Always Allow This Session")).toBeInTheDocument();
    expect(screen.getByText("Reject")).toBeInTheDocument();
  });
});

describe("ChatTranscript hides AskUserQuestion tool_use", () => {
  it("does not render a tool card for AskUserQuestion's tool_use / tool_result", () => {
    render(
      <ChatTranscript
        agentColor="agent-1"
        agentName="CEO 助手"
        messages={[
          chatMessage([
            {
              askUserQuestion: {
                requestId: "ask-1",
                questions: [
                  {
                    question: "选哪个?",
                    multiSelect: false,
                    options: [
                      { label: "A", description: "" },
                      { label: "B", description: "" },
                    ],
                  },
                ],
                answered: true,
                answers: [{ questionIndex: 0, labels: ["A"], otherText: "" }],
              },
              // Plan C: backend live + replay 路径都填 canonical.UserAsk;前端走
              // CanonicalToolRouter → UserAskCard 渲染。
              canonical: {
                kind: "user.ask",
                userAsk: {
                  requestId: "ask-1",
                  questions: [
                    {
                      question: "选哪个?",
                      multiSelect: false,
                      options: [
                        { label: "A", description: "" },
                        { label: "B", description: "" },
                      ],
                    },
                  ],
                  answered: true,
                  answers: [{ questionIndex: 0, labels: ["A"], otherText: "" }],
                },
              },
              type: "ask_user_question",
            } as ChatBlockData,
            {
              toolInput: { questions: [] },
              toolName: "AskUserQuestion",
              toolUseId: "auq-1",
              type: "tool_use",
            } as ChatBlockData,
            {
              text: "ok",
              toolUseId: "auq-1",
              type: "tool_result",
            } as ChatBlockData,
          ]),
        ]}
      />,
    );

    // UserAskCard 渲染(canonical-tool/user-ask;header 显示 user_ask)
    expect(screen.getByTestId("user-ask-card")).toBeInTheDocument();
    expect(screen.getByText("user_ask")).toBeInTheDocument();
    // 不存在 AskUserQuestion 的独立 tool_use 卡片
    expect(
      screen.queryByRole("region", { name: /Tool call AskUserQuestion/ }),
    ).toBeNull();
  });
});

// ExitPlanMode 的 PlanApproveCard 已经承担了"批准执行计划"的完整渲染,后续 CLI
// 真正调用 ExitPlanMode 时同样会冒出一条 tool_use(及配对 tool_result),如果按
// 通用 tool_use 路径渲染会得到一张"裸 ExitPlanMode"卡夹在 PlanApproveCard 旁边,
// 视觉重复。这里参照 AskUserQuestion 的做法,在 consumeBlock 里直接 skip。
describe("ChatTranscript hides ExitPlanMode tool_use", () => {
  it("renders only PlanApproveCard, no separate tool_use card for ExitPlanMode", () => {
    render(
      <ChatTranscript
        agentColor="agent-1"
        agentName="CEO 助手"
        messages={[
          chatMessage([
            {
              canonical: {
                kind: "plan.approve_request",
                planApprove: {
                  requestId: "perm-plan-1",
                  planText: "## Plan\n- step a\n- step b",
                  resolved: true,
                  allowed: true,
                },
              },
              toolPermission: {
                requestId: "perm-plan-1",
                toolName: "ExitPlanMode",
                toolInput: { plan: "## Plan\n- step a\n- step b" },
                resolved: true,
                allowed: true,
              },
              type: "tool_permission_request",
            } as unknown as ChatBlockData,
            {
              toolInput: { plan: "## Plan\n- step a\n- step b" },
              toolName: "ExitPlanMode",
              toolUseId: "epm-1",
              type: "tool_use",
            } as ChatBlockData,
            {
              text: "",
              toolUseId: "epm-1",
              type: "tool_result",
            } as ChatBlockData,
          ]),
        ]}
      />,
    );

    expect(screen.getByTestId("plan-card")).toBeInTheDocument();
    // ExitPlanMode 没有独立 tool_use 卡
    expect(
      screen.queryByRole("region", { name: /Tool call ExitPlanMode/ }),
    ).toBeNull();
    // 也不应出现 RawToolCard 把 toolName="ExitPlanMode" 暴露出来。
    expect(screen.queryByText("ExitPlanMode")).toBeNull();
  });
});

describe("ChatTranscript plan.update rendering", () => {
  it("does not render synthetic type=plan blocks in the transcript", () => {
    render(
      <ChatTranscript
        agentColor="agent-1"
        agentName="CEO 助手"
        messages={[
          chatMessage([
            {
              text: "# Plan\n\n1. Inspect\n2. Test",
              type: "plan",
            } as ChatBlockData,
          ]),
        ]}
      />,
    );

    expect(screen.queryByTestId("plan-card")).toBeNull();
    expect(screen.queryByText("Inspect")).toBeNull();
  });

  it("renders Codex plan-mode type=plan blocks with actions as a plan card", () => {
    render(
      <ChatTranscript
        agentColor="agent-1"
        agentName="CEO 助手"
        sessionId={7}
        messages={[
          chatMessage([
            {
              toolInput: { command: "echo before plan" },
              toolName: "command_execution",
              toolUseId: "bash-1",
              type: "tool_use",
            } as ChatBlockData,
            {
              text: "ok",
              toolUseId: "bash-1",
              type: "tool_result",
            } as ChatBlockData,
            {
              canonical: {
                kind: "plan.update",
                planUpdate: {
                  text: "# Plan\n\n1. Inspect\n2. Test",
                  actions: [
                    { id: "plan.execute", kind: "approve" },
                    {
                      id: "plan.refine",
                      kind: "refine",
                      requiresFeedback: true,
                    },
                  ],
                  steps: [],
                },
              },
              text: "# Plan\n\n1. Inspect\n2. Test",
              type: "plan",
            } as unknown as ChatBlockData,
          ]),
        ]}
      />,
    );

    expect(screen.getByTestId("plan-card")).toBeInTheDocument();
    expect(screen.getByText("Execute Plan")).toBeInTheDocument();
    expect(screen.getByText("Refine Plan")).toBeInTheDocument();
  });

  it("renders plan.update tool_use as an ordinary activity step", () => {
    render(
      <ChatTranscript
        agentColor="agent-1"
        agentName="CEO 助手"
        messages={[
          chatMessage([
            {
              canonical: {
                kind: "plan.update",
                planUpdate: {
                  steps: [
                    { step: "Inspect", status: "completed" },
                    { step: "Test", status: "inProgress" },
                  ],
                },
              },
              toolInput: { plan: "- [x] Inspect\n- [ ] Test" },
              toolName: "update_plan",
              toolUseId: "plan-1",
              type: "tool_use",
            } as unknown as ChatBlockData,
            {
              text: "ok",
              toolUseId: "plan-1",
              type: "tool_result",
            } as ChatBlockData,
          ]),
        ]}
      />,
    );

    const row = screen.getByTestId("activity-row");
    expect(screen.queryByTestId("plan-card")).toBeNull();
    expect(within(row).getByTestId("activity-name")).toHaveTextContent(
      "update_plan",
    );
    expect(screen.queryByText("plan.update")).toBeNull();
    // 成功态没有状态标记 ——「没有标记 = 成功」(spec 决策 10)。
    expect(screen.queryByText("DONE")).toBeNull();
  });
});

describe("ChatTranscript compact_boundary fold", () => {
  function makeMessage(
    id: number,
    role: "user" | "assistant",
    blocks: ChatBlockData[],
  ): chat_svc.ChatMessage {
    return {
      blocks,
      cachedTokens: 0,
      cacheCreationTokens: 0,
      completionTokens: 0,
      createtime: new Date("2026-05-27T10:00:00Z").getTime(),
      durationMs: 0,
      errorText: "",
      id,
      model: "",
      promptTokens: 0,
      reasoningTokens: 0,
      role,
      seq: id,
      sessionId: 1,
      totalInputTokens: 0,
    } as unknown as chat_svc.ChatMessage;
  }

  it("折叠 boundary 之前的消息,显示展开按钮 + 边界 divider", () => {
    render(
      <ChatTranscript
        agentColor="agent-1"
        agentName="A"
        messages={[
          makeMessage(1, "user", [{ type: "text", text: "old-question" }]),
          makeMessage(2, "assistant", [{ type: "text", text: "old-answer" }]),
          makeMessage(3, "assistant", [
            {
              type: "compact_boundary",
              compact: { preTokens: 12345, trigger: "auto", at: 0 },
            } as unknown as ChatBlockData,
            { type: "text", text: "fresh-answer" },
          ]),
        ]}
      />,
    );

    expect(screen.queryByText("old-question")).toBeNull();
    expect(screen.queryByText("old-answer")).toBeNull();
    expect(screen.getByText("fresh-answer")).toBeInTheDocument();
    expect(screen.getByText("Context compacted")).toBeInTheDocument();
    // 折叠条:文案"查看压缩前的 2 条消息"
    const expandBtn = screen.getByRole("button", {
      name: /View 2 messages before compaction/,
    });
    expect(expandBtn).toBeInTheDocument();
  });

  it("点击展开按钮后旧消息全部可见", () => {
    render(
      <ChatTranscript
        agentColor="agent-1"
        agentName="A"
        messages={[
          makeMessage(1, "user", [{ type: "text", text: "old-question" }]),
          makeMessage(2, "assistant", [
            {
              type: "compact_boundary",
              compact: { trigger: "manual", at: 0 },
            } as unknown as ChatBlockData,
            { type: "text", text: "fresh-answer" },
          ]),
        ]}
      />,
    );

    expect(screen.queryByText("old-question")).toBeNull();
    fireEvent.click(
      screen.getByRole("button", { name: /View 1 messages before compaction/ }),
    );
    expect(screen.getByText("old-question")).toBeInTheDocument();
    expect(screen.getByText("fresh-answer")).toBeInTheDocument();
  });

  it("没有 compact_boundary 时不折叠 / 不显示按钮", () => {
    render(
      <ChatTranscript
        agentColor="agent-1"
        agentName="A"
        messages={[
          makeMessage(1, "user", [{ type: "text", text: "q" }]),
          makeMessage(2, "assistant", [{ type: "text", text: "a" }]),
        ]}
      />,
    );

    expect(screen.getByText("q")).toBeInTheDocument();
    expect(screen.getByText("a")).toBeInTheDocument();
    expect(screen.queryByText("Context compacted")).toBeNull();
    expect(
      screen.queryByRole("button", { name: /View .* before compaction/ }),
    ).toBeNull();
  });
});

describe("ChatTranscript read-only mode (no onRerun / no onEdit)", () => {
  // 验收：只读调用方（如 ConversationPanel）不传 onRerun/onEdit 时，
  // 「重新生成」和「编辑」按钮不渲染；传入后仍正常工作（回归保护）。
  function assistantMsg(): chat_svc.ChatMessage {
    return {
      blocks: [{ type: "text", text: "resp" } as ChatBlockData],
      cacheCreationTokens: 0,
      cachedTokens: 0,
      completionTokens: 10,
      createtime: new Date("2026-06-01T10:00:00Z").getTime(),
      durationMs: 500,
      errorText: "",
      id: 20,
      model: "claude-sonnet-4-6",
      promptTokens: 5,
      reasoningTokens: 0,
      role: "assistant",
      seq: 1,
      sessionId: 1,
      totalInputTokens: 0,
    } as unknown as chat_svc.ChatMessage;
  }

  function userMsg(): chat_svc.ChatMessage {
    return {
      blocks: [{ type: "text", text: "question" } as ChatBlockData],
      cacheCreationTokens: 0,
      cachedTokens: 0,
      completionTokens: 0,
      createtime: new Date("2026-06-01T10:00:00Z").getTime(),
      durationMs: 0,
      errorText: "",
      id: 19,
      model: "",
      promptTokens: 0,
      reasoningTokens: 0,
      role: "user",
      seq: 1,
      sessionId: 1,
      totalInputTokens: 0,
    } as unknown as chat_svc.ChatMessage;
  }

  it("只读模式：不传 onRerun → 不渲染「重新生成」按钮", () => {
    // onRerun 未传；之前因稳定代理恒 truthy 错误渲染该按钮。
    render(
      <ChatTranscript
        agentColor="agent-1"
        agentName="TestAgent"
        messages={[userMsg(), assistantMsg()]}
      />,
    );

    expect(
      screen.queryByRole("button", { name: /Regenerate/ }),
    ).not.toBeInTheDocument();
  });

  it("只读模式：不传 onEdit → 不渲染「编辑」按钮", () => {
    // onEdit 未传；UserMessageActions 的 onEdit 箭头函数在无 ctx.onEdit 时不渲染。
    render(
      <ChatTranscript
        agentColor="agent-1"
        agentName="TestAgent"
        messages={[userMsg(), assistantMsg()]}
      />,
    );

    expect(
      screen.queryByRole("button", { name: /edit/i }),
    ).not.toBeInTheDocument();
  });

  it("有 onRerun → 「重新生成」按钮正常渲染并回调（回归保护）", () => {
    const calls: number[] = [];
    render(
      <ChatTranscript
        agentColor="agent-1"
        agentName="TestAgent"
        messages={[userMsg(), assistantMsg()]}
        onRerun={(id) => calls.push(id)}
      />,
    );

    fireEvent.click(screen.getByRole("button", { name: /Regenerate/ }));
    expect(calls).toEqual([20]);
  });

  it("有 onEdit → 「编辑」按钮正常渲染并回调（回归保护）", () => {
    const calls: number[] = [];
    render(
      <ChatTranscript
        agentColor="agent-1"
        agentName="TestAgent"
        messages={[userMsg(), assistantMsg()]}
        onEdit={(id) => calls.push(id)}
      />,
    );

    fireEvent.click(screen.getByRole("button", { name: /edit/i }));
    expect(calls).toEqual([19]);
  });
});
