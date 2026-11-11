import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter } from "react-router-dom";

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
  PeerSubmitAnswer,
  PeerSubmitToolPermission,
  PeerDetach,
} from "../../../../../wailsjs/go/app/App";
import { PeerPanel } from "../peer-panel";
import {
  peerKeyOf,
  usePeerSessionsStore,
} from "../../../../stores/peer-session-store";
import {
  createPeerTranscript,
  reducePeerEvent,
  type PeerTranscriptState,
} from "../peer-transcript";

// conv 是这些用例里那条对话的身份（uuid 字符串）——Peer Tab 按它寻址。
const conv = (n: number) =>
  `0198f4c1-a000-7c0d-8b21-${String(n).padStart(12, "0")}`;

const mockPerm = PeerSubmitToolPermission as unknown as ReturnType<
  typeof vi.fn
>;
const mockAnswer = PeerSubmitAnswer as unknown as ReturnType<typeof vi.fn>;
const mockAttach = PeerAttach as unknown as ReturnType<typeof vi.fn>;
const mockPull = PeerPull as unknown as ReturnType<typeof vi.fn>;
const mockDetach = PeerDetach as unknown as ReturnType<typeof vi.fn>;
const mockRun = PeerRun as unknown as ReturnType<typeof vi.fn>;
const mockSteer = PeerSteer as unknown as ReturnType<typeof vi.fn>;

// readySession 造一条「已接入」的 Peer Tab 会话,lifecycleState 由用例点名 ——
// 它正是「这条会话此刻在不在跑」,也正是发送走 run 还是 steer 的判据。
function readySession(
  lifecycleState: string,
  transcript: PeerTranscriptState = createPeerTranscript(),
) {
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
        transcript,
      },
    },
  });
  return key;
}

// 转录一律由**真的帧**归约出来 —— 面板画什么取决于归约器给的块,手写一份块等于
// 绕开被测的那条路。
function transcriptOf(
  events: Array<{ kind: string } & Record<string, unknown>>,
): PeerTranscriptState {
  let transcript = createPeerTranscript();
  events.forEach((event, i) => {
    transcript = reducePeerEvent(transcript, {
      fingerprint: "sha256:peer-desktop",
      conversationId: conv(7),
      seq: i + 1,
      event,
    } as never);
  });
  return transcript;
}

const withAsk = () =>
  transcriptOf([
    { kind: "text_delta", text: "need a decision" },
    {
      kind: "ask_user_question",
      requestId: "q-1",
      questions: [
        {
          question: "Which database?",
          header: "DB",
          options: [
            { label: "MySQL", description: "the one prod runs" },
            { label: "Postgres", description: "the other one" },
          ],
        },
      ],
    },
  ]);

const withPermission = () =>
  transcriptOf([
    { kind: "text_delta", text: "let me look" },
    {
      kind: "tool_permission_request",
      requestId: "p-1",
      toolName: "Bash",
      input: { command: "ls -la /srv" },
    },
  ]);

const withReply = () =>
  transcriptOf([{ kind: "text_delta", text: "done with the task" }]);

// MemoryRouter:面板把共享卡的动作端口改接到对端,其余端口沿用桌面端那一份 ——
// 而那一份里「点 @提及去哪」要 navigate。
function renderPanel() {
  return render(
    <MemoryRouter>
      <PeerPanel
        fingerprint="sha256:peer-desktop"
        conversationId={conv(7)}
        title="t"
        deviceName="MacBook Pro"
        active
        onClose={() => {}}
      />
    </MemoryRouter>,
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
    const { unmount } = renderPanel();
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

  // ── 未决提问 / 授权:与本机对话同一张卡,提交的是用户真实的选择 ─────────────
  //
  // 此前面板自绘一条决策条:「回答」提交的是 skipped:true + 空答案,选项根本不可见;
  // 授权条不显示工具输入,也没有「本会话始终允许」。

  it("Given 对端有未决提问, When 选了选项再提交, Then 提交的是用户选的答案", async () => {
    mockAnswer.mockResolvedValue({ alreadyHandled: false });
    readySession("running", withAsk());
    renderPanel();

    expect(await screen.findByTestId("user-ask-card")).toBeTruthy();
    // 题目与全部选项可见。
    expect(screen.getByText("Which database?")).toBeTruthy();
    expect(screen.getByText("MySQL")).toBeTruthy();
    await userEvent.click(screen.getByText("Postgres"));
    await userEvent.click(screen.getByRole("button", { name: "Submit Reply" }));

    await waitFor(() => expect(mockAnswer).toHaveBeenCalledTimes(1));
    const sent = mockAnswer.mock.calls[0][0];
    expect(sent).toMatchObject({
      fingerprint: "sha256:peer-desktop",
      conversationId: conv(7),
      requestId: "q-1",
    });
    expect(sent.skipped).toBeFalsy();
    expect(sent.answers).toEqual([
      expect.objectContaining({ questionIndex: 0, labels: ["Postgres"] }),
    ]);
  });

  it("Given 对端有未决提问, When 点「跳过」, Then 提交 skipped 而不带答案", async () => {
    mockAnswer.mockResolvedValue({ alreadyHandled: false });
    readySession("running", withAsk());
    renderPanel();

    await userEvent.click(await screen.findByRole("button", { name: "Skip" }));

    await waitFor(() => expect(mockAnswer).toHaveBeenCalledTimes(1));
    expect(mockAnswer.mock.calls[0][0]).toMatchObject({
      requestId: "q-1",
      skipped: true,
      answers: [],
    });
  });

  it("Given 提交答复被对端拒绝, Then 卡不锁、选择还在, 提示条说本地化事实句且原话只在折叠详情里, 可关闭", async () => {
    mockAnswer.mockRejectedValue(new Error("relay: upstream timed out"));
    readySession("running", withAsk());
    renderPanel();

    await userEvent.click(await screen.findByText("Postgres"));
    await userEvent.click(screen.getByRole("button", { name: "Submit Reply" }));

    const notice = await screen.findByTestId("peer-notice");
    expect(notice.textContent).toContain("didn't reach");
    expect(notice.getAttribute("data-kind")).toBe("error");
    // 原话是证据:只在默认折叠的详情里。
    const raw = within(notice).getByText("relay: upstream timed out");
    expect(raw.closest("details")).not.toBeNull();
    expect(raw.closest("details")!.hasAttribute("open")).toBe(false);
    // 卡没被锁死:还能再提交一次。
    const submit = screen.getByRole("button", { name: "Submit Reply" });
    expect(submit).toBeEnabled();
    await userEvent.click(submit);
    await waitFor(() => expect(mockAnswer).toHaveBeenCalledTimes(2));

    await userEvent.click(
      within(screen.getByTestId("peer-notice")).getByRole("button", {
        name: "Close",
      }),
    );
    expect(screen.queryByTestId("peer-notice")).toBeNull();
  });

  it("Given 对端说那条提问已经不在等了, Then 提示条按「已被处理」说(中性底), 不当成没送到", async () => {
    mockAnswer.mockRejectedValue(
      new Error("claudecode: no waiting request for requestID q-1"),
    );
    readySession("running", withAsk());
    renderPanel();

    await userEvent.click(await screen.findByRole("button", { name: "Skip" }));

    const notice = await screen.findByTestId("peer-notice");
    expect(notice.getAttribute("data-kind")).toBe("info");
    expect(notice.textContent).toContain("already handled");
    expect(notice.textContent).not.toContain("no waiting request");
  });

  it("Given 对端有未决授权, Then 显示工具输入, 「本会话始终允许」提交 alwaysAllowSession", async () => {
    mockPerm.mockResolvedValue({ alreadyHandled: false });
    readySession("running", withPermission());
    renderPanel();

    const card = await screen.findByTestId("tool-permission-card");
    // 工具输入的可读摘要:要运行的那条命令看得见。
    expect(within(card).getAllByText("ls -la /srv").length).toBeGreaterThan(0);
    expect(within(card).getByRole("button", { name: /^Allow/ })).toBeTruthy();
    expect(
      within(card).getByRole("button", { name: /Deny|Reject/ }),
    ).toBeTruthy();
    await userEvent.click(
      within(card).getByRole("button", { name: /Always Allow This Session/ }),
    );

    await waitFor(() => expect(mockPerm).toHaveBeenCalledTimes(1));
    expect(mockPerm.mock.calls[0][0]).toMatchObject({
      fingerprint: "sha256:peer-desktop",
      conversationId: conv(7),
      requestId: "p-1",
      allow: true,
      alwaysAllowSession: true,
    });
  });

  it("Given 授权已被别处处理, When 提交, Then 提示条用中性底说「已被处理」", async () => {
    mockPerm.mockResolvedValue({ alreadyHandled: true });
    readySession("running", withPermission());
    renderPanel();

    const card = await screen.findByTestId("tool-permission-card");
    await userEvent.click(within(card).getByRole("button", { name: /^Allow/ }));

    expect(mockPerm).toHaveBeenCalledWith(
      expect.objectContaining({
        fingerprint: "sha256:peer-desktop",
        conversationId: conv(7),
        requestId: "p-1",
        allow: true,
      }),
    );
    const notice = await screen.findByTestId("peer-notice");
    expect(notice.textContent).toContain("already handled");
    // 「已被处理」不是待办也不是警告。
    expect(notice.getAttribute("data-kind")).toBe("info");
  });

  it("renders a resolved permission as handled instead of buttons", async () => {
    let transcript = withPermission();
    transcript = reducePeerEvent(transcript, {
      fingerprint: "sha256:peer-desktop",
      conversationId: conv(7),
      seq: 3,
      event: {
        kind: "tool_permission_resolved",
        requestId: "p-1",
        allowed: true,
      },
    });
    readySession("idle", transcript);
    renderPanel();

    const card = await screen.findByTestId("tool-permission-card");
    expect(within(card).getByText("Allowed")).toBeTruthy();
    expect(screen.queryByRole("button", { name: /^Allow/ })).toBeNull();
  });

  // ── 生成中指示:只在对端那一轮真的在跑时出现 ─────────────────────────────────
  //
  // 此前判据是「已接入且不在等我」—— 对端闲着的时候它恒为真,转录末尾常驻三点。

  it("Given 对端那一轮已经结束, Then 转录末尾没有生成中指示", async () => {
    readySession("idle", withReply());
    renderPanel();

    expect(await screen.findByText("done with the task")).toBeTruthy();
    expect(screen.queryByLabelText("Generating")).toBeNull();
  });

  it("Given 对端那一轮正在跑, Then 转录末尾有生成中指示", async () => {
    readySession("running", withReply());
    renderPanel();

    expect(await screen.findByLabelText("Generating")).toBeTruthy();
  });

  // ── 接入失败页 ─────────────────────────────────────────────────────────────

  it("Given 接入失败, Then 有「重试」重新接入, 原始错误只在折叠详情里", async () => {
    const key = readySession("idle");
    usePeerSessionsStore.setState((state) => ({
      sessions: {
        [key]: {
          ...state.sessions[key],
          status: "error",
          error: "dial tcp 10.0.0.2:7456: connection refused",
        },
      },
    }));
    mockAttach.mockResolvedValue({ latestSeq: 0, lifecycleState: "idle" });
    mockPull.mockResolvedValue({
      notifications: [],
      cursor: 0,
      hasMore: false,
      oldestSeq: 0,
    });
    renderPanel();

    const raw = screen.getByText("dial tcp 10.0.0.2:7456: connection refused");
    expect(raw.closest("details")).not.toBeNull();
    expect(raw.closest("details")!.hasAttribute("open")).toBe(false);
    expect(mockAttach).not.toHaveBeenCalled();

    await userEvent.click(screen.getByRole("button", { name: "Retry" }));

    await waitFor(() => expect(mockAttach).toHaveBeenCalledTimes(1));
    await waitFor(() =>
      expect(usePeerSessionsStore.getState().sessions[key]?.status).toBe(
        "ready",
      ),
    );
    expect(screen.queryByRole("button", { name: "Retry" })).toBeNull();
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

    renderPanel();

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
