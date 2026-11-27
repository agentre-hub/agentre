import { act, render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import * as React from "react";
import { beforeEach, describe, expect, it, vi } from "vitest";

// 复刻 PTY 事件通道:`EventsOn` 的取消函数只摘自己;测试用 emit 模拟终端退出。
type Handler = (payload: never) => void;
const listeners = new Map<string, Set<Handler>>();
vi.mock("@/../wailsjs/runtime/runtime", () => ({
  EventsOn: vi.fn((event: string, handler: Handler) => {
    const bucket = listeners.get(event) ?? new Set<Handler>();
    listeners.set(event, bucket);
    bucket.add(handler);
    return () => bucket.delete(handler);
  }),
  EventsOff: vi.fn(),
}));

vi.mock("@/../wailsjs/go/app/App", () => ({
  TerminalOpen: vi.fn().mockResolvedValue(undefined),
  TerminalOpenForSession: vi.fn().mockResolvedValue(undefined),
  TerminalWrite: vi.fn().mockResolvedValue(undefined),
  TerminalResize: vi.fn().mockResolvedValue(undefined),
  TerminalClose: vi.fn().mockResolvedValue(undefined),
}));
// 删除会话走相对路径的绑定（与会话列表、对话页同一处导入）。
const deleteApi = vi.hoisted(() => ({ DeleteChatSession: vi.fn() }));
vi.mock("../../../../../wailsjs/go/app/App", () => deleteApi);

// 真 TerminalPanel 的 xterm 在 happy-dom 里跑不起来:换成一个按同样方式使用传输的替身
// (挂载时 open、订阅退出,卸载时 close),并记下挂载 / 卸载次数。
const panelLife = vi.hoisted(() => ({
  mounts: 0,
  unmounts: 0,
  passThroughKey: null as ((ev: KeyboardEvent) => boolean) | null,
}));
vi.mock("@agentre-hub/agentre-ui/terminal", async () => {
  const ui = await vi.importActual<typeof import("@agentre-hub/agentre-ui")>(
    "@agentre-hub/agentre-ui",
  );
  const R = await vi.importActual<typeof import("react")>("react");
  function TerminalPanel(props: {
    terminalID: string;
    projectId: number;
    deviceId: string;
    active?: boolean;
    attach?: boolean;
    onClose: () => void;
    onNewTerminal?: () => void;
    passThroughKey?: (ev: KeyboardEvent) => boolean;
    surface?: string;
  }) {
    const transport = ui.useTerminalTransport();
    R.useEffect(() => {
      panelLife.passThroughKey = props.passThroughKey ?? null;
    });
    R.useEffect(() => {
      panelLife.mounts += 1;
      if (props.attach) {
        return () => {
          panelLife.unmounts += 1;
        };
      }
      const off = transport.subscribe(props.terminalID, {
        onData: () => {},
        onExit: () => {},
      });
      void transport.open({
        terminalId: props.terminalID,
        projectId: props.projectId,
        deviceId: props.deviceId,
        cols: 80,
        rows: 24,
      });
      return () => {
        panelLife.unmounts += 1;
        off();
        void transport.close(props.terminalID);
      };
    }, [
      transport,
      props.terminalID,
      props.attach,
      props.projectId,
      props.deviceId,
    ]);
    return (
      <div
        data-testid="terminal-panel"
        data-terminal={props.terminalID}
        data-active={String(props.active)}
        data-surface={props.surface}
      >
        <button type="button" onClick={props.onNewTerminal}>
          panel-new-terminal
        </button>
        <button type="button" onClick={props.onClose}>
          panel-close
        </button>
      </div>
    );
  }
  return { TerminalPanel };
});

import * as App from "@/../wailsjs/go/app/App";
import { TooltipProvider } from "@agentre-hub/agentre-ui";
import { useChatTabsStore } from "@/stores/chat-tabs-store";
import {
  useSessionRootStore,
  type WorkRoot,
} from "@/stores/session-root-store";
import { useSessionTerminalsStore } from "@/stores/session-terminals-store";

import { useToolWindowLayout } from "@/stores/tool-window-layout-store";

import { deleteChatSession } from "../../session-index/delete-chat-session";
import { SessionTerminalsHost } from "../session-terminals-host";
import { TerminalToolWindow } from "../terminal-tool-window";
import { ToolWindowFrame } from "../tool-window-frame";

const CWD = "/work/demo-app";
const WT = "/work/demo-app-dark-mode";

function root(path: string, name: string, isPrimary: boolean): WorkRoot {
  return { path, name, isPrimary, isWorktree: !isPrimary, aiHere: false };
}

function twoRoots(sessionId = 7) {
  act(() =>
    useSessionRootStore
      .getState()
      .applyRoots(sessionId, CWD, [
        root(CWD, "demo-app", true),
        root(WT, "dark-mode", false),
      ]),
  );
}

function oneRoot(sessionId = 7) {
  act(() =>
    useSessionRootStore
      .getState()
      .applyRoots(sessionId, CWD, [root(CWD, "demo-app", true)]),
  );
}

/** 与真实页面同构:Host 常驻(在 ChatPanelHost 里),窗口内容随显示起落。 */
function Harness({
  sessionId = 7,
  shown = true,
}: {
  sessionId?: number;
  shown?: boolean;
}) {
  return (
    <TooltipProvider>
      <SessionTerminalsHost />
      {shown ? <TerminalToolWindow sessionId={sessionId} /> : null}
    </TooltipProvider>
  );
}

/**
 * 窗口还没有终端时,空态里的按钮就是「新建终端」;已有终端时是标签栏末尾的 +。
 * 面板是懒加载的,等它出现(此时终端已 open)再往下断言。
 */
async function newTerminal() {
  await userEvent
    .setup()
    .click(screen.getByRole("button", { name: "New terminal" }));
  await screen.findByTestId("terminal-panel");
}

const openCalls = () => vi.mocked(App.TerminalOpenForSession).mock.calls;

beforeEach(() => {
  vi.clearAllMocks();
  listeners.clear();
  panelLife.mounts = 0;
  panelLife.unmounts = 0;
  panelLife.passThroughKey = null;
  useSessionTerminalsStore.setState({ bySession: {}, viewing: {} });
  useSessionRootStore.setState({ bySession: {} });
  useChatTabsStore.setState({ tabs: [], activeTabId: null });
});

/** 与真实页面同构、再套上工具窗外壳：标签进标题栏、外壳的按键处理都在。 */
function FramedHarness({ sessionId = 7 }: { sessionId?: number }) {
  return (
    <TooltipProvider>
      <SessionTerminalsHost />
      <ToolWindowFrame
        window={{
          id: "terminal",
          label: "Terminal",
          icon: (() => null) as never,
          content: <TerminalToolWindow sessionId={sessionId} />,
        }}
      />
    </TooltipProvider>
  );
}

describe("TerminalToolWindow · 在工具窗外壳里", () => {
  beforeEach(() => {
    act(() =>
      useToolWindowLayout.setState({
        shown: {
          rightTop: null,
          rightBottom: null,
          bottomLeft: "terminal",
          bottomRight: null,
        },
        maximized: null,
      }),
    );
  });

  // Given 窗口里有终端标签, Then 标签在标题栏里（spec「终端与网页预览的标题栏同时承载各自的标签」）。
  it("puts the terminal tabs in the window's title bar", async () => {
    oneRoot();
    render(<FramedHarness />);
    await newTerminal();
    const region = screen.getByRole("region", { name: "Terminal" });
    const titleBar = region.querySelector("[data-tool-window-title-bar]");
    expect(titleBar).not.toBeNull();
    expect(within(titleBar as HTMLElement).getByRole("tablist")).toBeTruthy();
    expect(
      within(titleBar as HTMLElement).getByRole("tab", { name: /Terminal 1/ }),
    ).toBeTruthy();
  });

  // Given 终端放在工具窗里, When 面板挂载, Then 它取窗口表面(--sheet)作底色。
  it("mounts the terminal on the window surface", async () => {
    oneRoot();
    render(<FramedHarness />);
    await newTerminal();
    expect(screen.getByTestId("terminal-panel").dataset.surface).toBe("sheet");
  });

  // Given 焦点在终端里, When 按 ⇧Esc, Then 隐藏终端工具窗（终端面板挂在常驻宿主里，按键要照样落到外壳）。
  it("hides the window on shift+Esc pressed inside the terminal", async () => {
    oneRoot();
    render(<FramedHarness />);
    await newTerminal();
    const inside = within(screen.getByTestId("terminal-panel")).getByRole(
      "button",
      { name: "panel-close" },
    );
    act(() => {
      inside.dispatchEvent(
        new KeyboardEvent("keydown", {
          key: "Escape",
          code: "Escape",
          shiftKey: true,
          bubbles: true,
          cancelable: true,
        }),
      );
    });
    expect(useToolWindowLayout.getState().shown.bottomLeft).toBeNull();
  });
});

describe("TerminalToolWindow · 焦点在终端里时的工具窗快捷键", () => {
  // Given 焦点在终端里, When 按 ⇧Esc / ⌃`（工具窗快捷键）, Then 终端不吞它们、交给应用；
  // 单按 Esc 仍归终端（vim 等全屏程序要用它）。
  it("lets tool-window shortcuts through to the app but keeps plain Esc in the terminal", async () => {
    oneRoot();
    render(<Harness />);
    await newTerminal();
    const pass = panelLife.passThroughKey;
    expect(pass).not.toBeNull();
    const key = (init: KeyboardEventInit) => new KeyboardEvent("keydown", init);
    expect(pass!(key({ key: "Escape", code: "Escape", shiftKey: true }))).toBe(
      true,
    );
    expect(pass!(key({ key: "`", code: "Backquote", ctrlKey: true }))).toBe(
      true,
    );
    expect(pass!(key({ key: "Escape", code: "Escape" }))).toBe(false);
    expect(pass!(key({ key: "c", code: "KeyC", ctrlKey: true }))).toBe(false);
  });
});

describe("TerminalToolWindow · 新建终端在当前工作根", () => {
  // Given 当前工作根是 worktree, When 显示终端窗, Then 第一个终端在 worktree 起
  // (会话 7、root=worktree 路径),而不是项目目录。
  it("starts the first terminal in the session's current root", async () => {
    twoRoots();
    act(() => useSessionRootStore.getState().select(7, WT));
    render(<Harness />);
    await newTerminal();
    expect(openCalls()).toEqual([[expect.any(String), 7, WT, 80, 24]]);
  });

  // Given 当前是主根, Then 传给后端的 root 是空串(主根的规范标识)。
  it("opens on the primary root with an empty root argument", async () => {
    oneRoot();
    render(<Harness />);
    await newTerminal();
    expect(openCalls()).toEqual([[expect.any(String), 7, "", 80, 24]]);
  });

  // Given 已开一个终端后工作根变了, When 点「新建终端」, Then 新终端在新的当前根起;
  // 已开的终端不会被重开 / cd(没有第二次对它的 open)。
  it("a new terminal follows the current root while the open one is left alone", async () => {
    twoRoots();
    const user = userEvent.setup();
    render(<Harness />);
    await newTerminal();
    expect(openCalls()).toHaveLength(1);
    expect(openCalls()[0][2]).toBe("");

    act(() => useSessionRootStore.getState().select(7, WT));
    expect(openCalls()).toHaveLength(1);

    await user.click(screen.getByRole("button", { name: "New terminal" }));
    expect(openCalls()).toHaveLength(2);
    expect(openCalls()[1][2]).toBe(WT);
    expect(openCalls()[1][0]).not.toBe(openCalls()[0][0]);
  });

  // Given 终端断线, When 点红条上的「新开终端」, Then 在本窗里多一个标签、在当前根起。
  it("a disconnect 'new terminal' opens another tab in this window", async () => {
    twoRoots();
    const user = userEvent.setup();
    render(<Harness />);
    await newTerminal();
    act(() => useSessionRootStore.getState().select(7, WT));
    await user.click(
      within(screen.getByTestId("terminal-panel")).getByRole("button", {
        name: "panel-new-terminal",
      }),
    );
    expect(useSessionTerminalsStore.getState().bySession[7].tabs).toHaveLength(
      2,
    );
    expect(openCalls()).toHaveLength(2);
    expect(openCalls()[1][2]).toBe(WT);
    expect(screen.getAllByRole("tab")).toHaveLength(2);
  });
});

describe("TerminalToolWindow · 会话被删除", () => {
  // Given 会话有在跑的终端, When 删除这个会话, Then 它的终端进程随之结束、标签消失；
  // 别的会话的终端不受影响。
  it("closes the deleted session's terminals and leaves other sessions alone", async () => {
    oneRoot();
    oneRoot(8);
    const view = render(<Harness />);
    await newTerminal();
    view.rerender(<Harness sessionId={8} />);
    await newTerminal();
    vi.mocked(App.TerminalClose).mockClear();
    deleteApi.DeleteChatSession.mockResolvedValueOnce(undefined);

    await act(() => deleteChatSession(7));

    expect(deleteApi.DeleteChatSession).toHaveBeenCalledWith({ sessionId: 7 });
    expect(App.TerminalClose).toHaveBeenCalledTimes(1);
    expect(useSessionTerminalsStore.getState().bySession[7]).toBeUndefined();
    expect(useSessionTerminalsStore.getState().bySession[8]?.tabs).toHaveLength(
      1,
    );
  });

  // Given 删除失败, Then 终端照留（会话还在）。
  it("keeps the terminals when the delete fails", async () => {
    oneRoot();
    render(<Harness />);
    await newTerminal();
    deleteApi.DeleteChatSession.mockRejectedValueOnce(new Error("nope"));

    await expect(act(() => deleteChatSession(7))).rejects.toThrow("nope");

    expect(useSessionTerminalsStore.getState().bySession[7]?.tabs).toHaveLength(
      1,
    );
  });
});

describe("TerminalToolWindow · 标签名", () => {
  // Given 会话有两个工作根, Then 标签名带当前终端起始根的根名。
  it("carries the root name once the session has two or more roots", async () => {
    twoRoots();
    act(() => useSessionRootStore.getState().select(7, WT));
    render(<Harness />);
    await newTerminal();
    expect(
      screen.getByRole("tab", { name: /Terminal 1 · dark-mode/ }),
    ).toBeTruthy();
  });

  // Given 只有一个根, Then 标签名不带根名。
  it("omits the root name with a single root", async () => {
    oneRoot();
    render(<Harness />);
    await newTerminal();
    const tab = screen.getByRole("tab", { name: /Terminal 1/ });
    expect(tab.textContent).not.toContain("demo-app");
  });
});

describe("TerminalToolWindow · 隐藏不结束进程", () => {
  // Given 一个在跑的终端, When 隐藏终端窗再显示, Then 没有 close、没有重新 open,
  // 面板一直挂着,显示后还是同一个终端。
  it("keeps the process alive across hide and show", async () => {
    oneRoot();
    const { rerender } = render(<Harness />);
    await newTerminal();
    const id = openCalls()[0][0];
    expect(panelLife.mounts).toBe(1);

    rerender(<Harness shown={false} />);
    expect(App.TerminalClose).not.toHaveBeenCalled();
    expect(panelLife.unmounts).toBe(0);
    expect(screen.queryByTestId("terminal-panel")).toBeNull();

    rerender(<Harness shown />);
    expect(App.TerminalClose).not.toHaveBeenCalled();
    expect(openCalls()).toHaveLength(1);
    expect(screen.getByTestId("terminal-panel").dataset.terminal).toBe(id);
    // 窗口重新显示时不再自动新开第二个终端。
    expect(useSessionTerminalsStore.getState().bySession[7].tabs).toHaveLength(
      1,
    );
  });

  // 隐藏期间面板不算「在看」,显示时才标成可见。
  it("marks the panel inactive while the window is hidden", async () => {
    oneRoot();
    const { rerender } = render(<Harness />);
    await newTerminal();
    expect(screen.getByTestId("terminal-panel").dataset.active).toBe("true");
    rerender(<Harness shown={false} />);
    expect(useSessionTerminalsStore.getState().viewing[7]).toBe(false);
  });
});

describe("TerminalToolWindow · 关闭标签与今天一致", () => {
  // When 点标签上的关闭, Then 这个终端的 PTY 被关掉、标签消失(与今天关终端标签页同)。
  it("closing a tab closes its terminal process", async () => {
    oneRoot();
    const user = userEvent.setup();
    render(<Harness />);
    await newTerminal();
    const id = openCalls()[0][0];
    await user.click(screen.getByRole("button", { name: /^Close Terminal 1/ }));
    expect(App.TerminalClose).toHaveBeenCalledWith(id);
    expect(useSessionTerminalsStore.getState().bySession[7].tabs).toEqual([]);
  });

  // 进程自己退出(面板调 onClose)与手动关一样把标签收掉。
  it("a natural exit closes the tab", async () => {
    oneRoot();
    const user = userEvent.setup();
    render(<Harness />);
    await newTerminal();
    await user.click(screen.getByRole("button", { name: "panel-close" }));
    expect(useSessionTerminalsStore.getState().bySession[7].tabs).toEqual([]);
    expect(App.TerminalClose).toHaveBeenCalledTimes(1);
  });
});

describe("TerminalToolWindow · 按会话分开", () => {
  // Given 两个会话各有终端, When 切到另一个会话, Then 只显示那个会话自己的标签。
  it("shows only the terminals of the session being viewed", () => {
    oneRoot(1);
    oneRoot(2);
    act(() => {
      useSessionTerminalsStore.getState().openInCurrentRoot(1);
      useSessionTerminalsStore.getState().openInCurrentRoot(2);
      useSessionTerminalsStore.getState().openInCurrentRoot(2);
    });
    const { rerender } = render(<Harness sessionId={1} />);
    expect(screen.getAllByRole("tab")).toHaveLength(1);
    rerender(<Harness sessionId={2} />);
    expect(screen.getAllByRole("tab")).toHaveLength(2);
    // 切走的会话的终端仍活着。
    expect(App.TerminalClose).not.toHaveBeenCalled();
  });
});

describe("TerminalToolWindow · 接入转录卡片的进程", () => {
  // Given 卡片上一个在跑的进程, When 接入, Then 本窗多一个标签、显示它。
  it("shows an attached process as an extra tab", async () => {
    oneRoot();
    render(<Harness />);
    await newTerminal();
    act(() =>
      useSessionTerminalsStore
        .getState()
        .attach(7, { terminalId: "cmd-1", command: "npm test" }),
    );
    expect(screen.getAllByRole("tab")).toHaveLength(2);
    expect(screen.getByRole("tab", { name: /Command: npm test/ })).toBeTruthy();
    expect(screen.getByTestId("terminal-panel").dataset.terminal).toBe("cmd-1");
  });
});

describe("会话终端的退出旁听", () => {
  // Given 终端断线, When 进程退出事件到达, Then 标签留着但不再算存活。
  it("marks the tab exited when the process exits", async () => {
    oneRoot();
    render(<Harness />);
    await newTerminal();
    const id = openCalls()[0][0];
    act(() => {
      for (const h of listeners.get(`terminal:${id}:exit`) ?? []) {
        h({ code: 0, reason: "connection_lost" } as never);
      }
    });
    const tab = useSessionTerminalsStore.getState().bySession[7].tabs[0];
    expect(tab.exited).toBe(true);
  });
});

describe("项目终端仍是独立的对话标签页", () => {
  // Given 从项目列表打开项目终端, Then 它是 chat-tabs 里的 terminal 标签,
  // 会话终端 store 不收它。
  it("opens as a chat tab and does not enter any session's terminal window", () => {
    useChatTabsStore.getState().openTerminal(5, "", undefined);
    const tabs = useChatTabsStore.getState().tabs;
    expect(tabs).toHaveLength(1);
    expect(tabs[0].meta.kind).toBe("terminal");
    expect(useSessionTerminalsStore.getState().bySession).toEqual({});
  });
});

// 防止 React 未使用告警:Harness 用到了 JSX。
void React;
