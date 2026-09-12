import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";

vi.mock("../../../../../wailsjs/go/app/App", () => ({
  PeerAttach: vi.fn(),
  PeerPull: vi.fn(),
  PeerSteer: vi.fn(),
  PeerRun: vi.fn(),
  PeerSubmitAnswer: vi.fn(),
  PeerSubmitToolPermission: vi.fn(),
  PeerDetach: vi.fn().mockResolvedValue(undefined),
  RemoteDeviceFingerprint: vi.fn().mockResolvedValue(""),
  // 输入框的 @ 菜单要一份设备清单(device-list-store)。
  RemoteDeviceList: vi.fn().mockResolvedValue([]),
  ServerListDevices: vi.fn().mockRejectedValue(new Error("not logged in")),
}));

vi.mock("../../../../../wailsjs/runtime/runtime", () => ({
  EventsOn: vi.fn(() => vi.fn()),
  EventsOff: vi.fn(),
  OnFileDrop: vi.fn(),
  OnFileDropOff: vi.fn(),
}));

import {
  PeerAttach,
  PeerPull,
  PeerRun,
  PeerSteer,
  PeerSubmitToolPermission,
  PeerDetach,
} from "../../../../../wailsjs/go/app/App";
import { PeerPanel } from "../peer-panel";
import {
  peerKeyOf,
  usePeerSessionsStore,
} from "../../../../stores/peer-session-store";
import { createPeerTranscript, reducePeerEvent } from "../peer-transcript";

// conv 是这些用例里那条对话的身份（uuid 字符串）——Peer Tab 按它寻址。
const conv = (n: number) =>
  `0198f4c1-a000-7c0d-8b21-${String(n).padStart(12, "0")}`;

const mockPerm = PeerSubmitToolPermission as unknown as ReturnType<
  typeof vi.fn
>;
const mockDetach = PeerDetach as unknown as ReturnType<typeof vi.fn>;
const mockRun = PeerRun as unknown as ReturnType<typeof vi.fn>;
const mockSteer = PeerSteer as unknown as ReturnType<typeof vi.fn>;

// readySession 造一条「已接入」的 Peer Tab 会话,lifecycleState 由用例点名 ——
// 它正是「这条会话此刻在不在跑」,也正是发送走 run 还是 steer 的判据。
function readySession(lifecycleState: string) {
  const key = peerKeyOf("sha256:peer-desktop", conv(7));
  usePeerSessionsStore.setState({
    sessions: {
      [key]: {
        key,
        fingerprint: "sha256:peer-desktop",
        conversationId: conv(7),
        title: "t",
        deviceName: "MacBook Pro",
        status: "ready",
        lifecycleState,
        highWater: 0,
        sending: false,
        transcript: createPeerTranscript(),
      },
    },
  });
  return key;
}

function renderPanel() {
  return render(
    <PeerPanel
      fingerprint="sha256:peer-desktop"
      conversationId={conv(7)}
      title="t"
      deviceName="MacBook Pro"
      active
      onClose={() => {}}
    />,
  );
}

// send 走生产路径:在输入框里打字,再按发送键。
//
// 打完字要等发送键**真的可按**再按:ProseMirror 的 view 在 effect 里挂,草稿空不空
// 又是 React 侧的状态(空草稿时发送键 disabled)。机器忙的时候这两跳都会落在 type
// 之后 —— 按在一颗还禁用着的键上什么也不会发生,用例会以「没调到绑定」的面目失败,
// 而缺陷根本不在被测代码里。
//
// delay: null 关掉逐键之间的排队:整条转录 + 输入框渲染一次本来就不便宜,并发跑
// 时逐键 await 能把一个用例拖过默认超时(拖过之后它还没打完的键会溅到下一个用例上)。
async function send(text: string) {
  const editor = await screen.findByRole("textbox");
  await userEvent.click(editor);
  await userEvent.type(editor, text, { delay: null });
  const sendButton = screen.getByRole("button", { name: "Send" });
  await waitFor(() => expect(sendButton).toBeEnabled(), { timeout: 5000 });
  await userEvent.click(sendButton);
}

// 这几条要渲染整块面板再走一遍输入框,忙机器上默认 5s 不够 —— 超时会以「没调到
// 绑定」的面目出现,和真缺陷长得一模一样。
const SEND_CASE_TIMEOUT = 20000;

beforeEach(() => {
  vi.clearAllMocks();
  usePeerSessionsStore.setState({ sessions: {} });
});

describe("PeerPanel", () => {
  it("attaches on mount and detaches on unmount (close-detaches only)", async () => {
    (PeerAttach as unknown as ReturnType<typeof vi.fn>).mockResolvedValue({
      latestSeq: 0,
      lifecycleState: "idle",
    });
    (PeerPull as unknown as ReturnType<typeof vi.fn>).mockResolvedValue({
      notifications: [],
      cursor: 0,
      hasMore: false,
      oldestSeq: 0,
    });
    const { unmount } = render(
      <PeerPanel
        fingerprint="sha256:peer-desktop"
        conversationId={conv(7)}
        title="t"
        deviceName="MacBook Pro"
        active
        onClose={() => {}}
      />,
    );
    await waitFor(() => {
      expect(PeerAttach).toHaveBeenCalledWith(
        expect.objectContaining({
          fingerprint: "sha256:peer-desktop",
          conversationId: conv(7),
        }),
      );
    });
    unmount();
    expect(mockDetach).toHaveBeenCalledWith("sha256:peer-desktop", conv(7));
  });

  it("renders a pending tool-permission decision and shows the already-handled notice on submit", async () => {
    mockPerm.mockResolvedValue({ alreadyHandled: true });
    const key = peerKeyOf("sha256:peer-desktop", conv(7));
    usePeerSessionsStore.setState({
      sessions: {
        [key]: {
          key,
          fingerprint: "sha256:peer-desktop",
          conversationId: conv(7),
          title: "t",
          deviceName: "MacBook Pro",
          status: "ready",
          lifecycleState: "idle",
          highWater: 0,
          sending: false,
          transcript: {
            ...createPeerTranscript(),
            decisions: [
              {
                kind: "permission",
                requestId: "p-1",
                toolName: "Bash",
              },
            ],
          },
        },
      },
    });

    render(
      <PeerPanel
        fingerprint="sha256:peer-desktop"
        conversationId={conv(7)}
        title="t"
        deviceName="MacBook Pro"
        active
        onClose={() => {}}
      />,
    );

    expect(screen.getByTestId("peer-permission-card")).toBeTruthy();
    await userEvent.click(screen.getByText("Allow"));
    expect(mockPerm).toHaveBeenCalledWith(
      expect.objectContaining({
        fingerprint: "sha256:peer-desktop",
        conversationId: conv(7),
        requestId: "p-1",
        allow: true,
      }),
    );
    await waitFor(() => {
      expect(screen.getByTestId("peer-notice").textContent).toContain(
        "already handled",
      );
    });
  });

  it("renders a resolved permission as handled instead of buttons", async () => {
    const key = peerKeyOf("sha256:peer-desktop", conv(7));
    usePeerSessionsStore.setState({
      sessions: {
        [key]: {
          key,
          fingerprint: "sha256:peer-desktop",
          conversationId: conv(7),
          title: "t",
          deviceName: "MacBook Pro",
          status: "ready",
          lifecycleState: "idle",
          highWater: 0,
          sending: false,
          transcript: {
            ...createPeerTranscript(),
            decisions: [
              {
                kind: "permission",
                requestId: "p-1",
                toolName: "Bash",
                resolved: true,
                allowed: true,
              },
            ],
          },
        },
      },
    });

    render(
      <PeerPanel
        fingerprint="sha256:peer-desktop"
        conversationId={conv(7)}
        title="t"
        deviceName="MacBook Pro"
        active
        onClose={() => {}}
      />,
    );

    expect(screen.getByTestId("peer-permission-handled")).toBeTruthy();
    expect(screen.queryByText("Allow")).toBeNull();
  });

  // Given 归约器现在产出 plan / notice 这些 Peer Tab 从前一律落 raw 的块;When 面板把它们
  // 喂给 ChatTranscript;Then 转录**真的画得出来**。此前那些块渲染成一行
  // `(debug) unimplemented block type: raw`,载荷不可见 —— 光看归约结果看不出这件事,
  // 所以这条断言落在渲染出来的文字上。
  it("renders the block kinds the peer tab previously downgraded to raw", async () => {
    const key = peerKeyOf("sha256:peer-desktop", conv(7));
    let transcript = createPeerTranscript();
    transcript = reducePeerEvent(transcript, {
      fingerprint: "sha256:peer-desktop",
      conversationId: conv(7),
      seq: 1,
      event: { kind: "compact_boundary", preTokens: 120000, trigger: "auto" },
    });
    transcript = reducePeerEvent(transcript, {
      fingerprint: "sha256:peer-desktop",
      conversationId: conv(7),
      seq: 2,
      event: { kind: "kind_from_a_newer_peer", detail: "payload-kept" },
    } as never);
    usePeerSessionsStore.setState({
      sessions: {
        [key]: {
          key,
          fingerprint: "sha256:peer-desktop",
          conversationId: conv(7),
          title: "t",
          deviceName: "MacBook Pro",
          status: "ready",
          lifecycleState: "idle",
          highWater: 0,
          sending: false,
          transcript,
        },
      },
    });

    render(
      <PeerPanel
        fingerprint="sha256:peer-desktop"
        conversationId={conv(7)}
        title="t"
        deviceName="MacBook Pro"
        active
        onClose={() => {}}
      />,
    );

    // 压缩边界从前是 raw,现在走包的 CompactBoundaryDivider。
    expect(
      await screen.findByLabelText("Context compaction boundary"),
    ).toBeTruthy();
    // 未知帧的载荷要**看得见**,这正是 raw 那条路藏起来的东西。
    expect(screen.getByTestId("transcript-notice").textContent).toContain(
      "payload-kept",
    );
  });

  // ── 发送:空闲会话也要发得出去 ─────────────────────────────────────────────
  //
  // 面板此前无条件只调 steer,而 steer 只能给**正在进行的轮次**插话:对着一条闲着
  // 的会话怎么发都是 `agentruntime: no active turn for session`,界面却只说一句
  // 「发送失败,请重试」—— 重试一百次也不会变。用户不该关心这条会话此刻在不在跑。

  it(
    "会话空闲时发送:在同一条对话上起新一轮(run),不走插话",
    async () => {
      mockRun.mockResolvedValue({ conversationId: conv(7) });
      readySession("idle");
      renderPanel();

      await send("空闲时也要发得出去");

      await waitFor(() => {
        expect(mockRun).toHaveBeenCalledWith(
          expect.objectContaining({
            fingerprint: "sha256:peer-desktop",
            conversationId: conv(7),
            text: "空闲时也要发得出去",
          }),
        );
      });
      expect(mockSteer).not.toHaveBeenCalled();
    },
    SEND_CASE_TIMEOUT,
  );

  it(
    "会话正在跑时发送:插进当前这一轮(steer),不另起一轮",
    async () => {
      mockSteer.mockResolvedValue(undefined);
      readySession("running");
      renderPanel();

      await send("插一句");

      await waitFor(() => {
        expect(mockSteer).toHaveBeenCalledWith(
          expect.objectContaining({
            conversationId: conv(7),
            text: "插一句",
          }),
        );
      });
      expect(mockRun).not.toHaveBeenCalled();
    },
    SEND_CASE_TIMEOUT,
  );

  it(
    "发送失败时说出对端给的真实原因,并保留草稿",
    async () => {
      mockRun.mockRejectedValue(
        new Error("relay: Agentre App is not running on the target desktop"),
      );
      readySession("idle");
      renderPanel();

      await send("发不出去");

      const notice = await screen.findByTestId("peer-notice");
      expect(notice.textContent).toContain(
        "Agentre is not running on that computer",
      );
      // 「请重试」是句废话:这条失败重试多少次都一样。
      expect(notice.textContent).not.toContain("please retry");
      // 草稿不能跟着丢。
      expect(screen.getByRole("textbox")).toHaveTextContent("发不出去");
    },
    SEND_CASE_TIMEOUT,
  );

  it(
    "对端说不出原因时,界面照样给出一句事实,而不是「请重试」",
    async () => {
      mockRun.mockRejectedValue(new Error(""));
      readySession("idle");
      renderPanel();

      await send("发不出去");

      const notice = await screen.findByTestId("peer-notice");
      expect(notice.textContent).not.toContain("please retry");
      expect(notice.textContent!.trim().length).toBeGreaterThan(0);
    },
    SEND_CASE_TIMEOUT,
  );
});
