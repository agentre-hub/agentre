import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";

vi.mock("../../../../../wailsjs/go/app/App", () => ({
  PeerAttach: vi.fn(),
  PeerPull: vi.fn(),
  PeerSteer: vi.fn(),
  PeerSubmitAnswer: vi.fn(),
  PeerSubmitToolPermission: vi.fn(),
  PeerDetach: vi.fn().mockResolvedValue(undefined),
  RemoteDeviceFingerprint: vi.fn().mockResolvedValue(""),
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
  PeerSubmitToolPermission,
  PeerDetach,
} from "../../../../../wailsjs/go/app/App";
import { PeerPanel } from "../peer-panel";
import {
  peerKeyOf,
  usePeerSessionsStore,
} from "../../../../stores/peer-session-store";
import { createPeerTranscript, reducePeerEvent } from "../peer-transcript";

const mockPerm = PeerSubmitToolPermission as unknown as ReturnType<
  typeof vi.fn
>;
const mockDetach = PeerDetach as unknown as ReturnType<typeof vi.fn>;

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
        sessionId={7}
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
          sessionId: 7,
        }),
      );
    });
    unmount();
    expect(mockDetach).toHaveBeenCalledWith("sha256:peer-desktop", 7);
  });

  it("renders a pending tool-permission decision and shows the already-handled notice on submit", async () => {
    mockPerm.mockResolvedValue({ alreadyHandled: true });
    const key = peerKeyOf("sha256:peer-desktop", 7);
    usePeerSessionsStore.setState({
      sessions: {
        [key]: {
          key,
          fingerprint: "sha256:peer-desktop",
          sessionId: 7,
          title: "t",
          deviceName: "MacBook Pro",
          status: "ready",
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
        sessionId={7}
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
        sessionId: 7,
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
    const key = peerKeyOf("sha256:peer-desktop", 7);
    usePeerSessionsStore.setState({
      sessions: {
        [key]: {
          key,
          fingerprint: "sha256:peer-desktop",
          sessionId: 7,
          title: "t",
          deviceName: "MacBook Pro",
          status: "ready",
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
        sessionId={7}
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
    const key = peerKeyOf("sha256:peer-desktop", 7);
    let transcript = createPeerTranscript();
    transcript = reducePeerEvent(transcript, {
      fingerprint: "sha256:peer-desktop",
      sessionId: 7,
      seq: 1,
      event: { kind: "compact_boundary", preTokens: 120000, trigger: "auto" },
    });
    transcript = reducePeerEvent(transcript, {
      fingerprint: "sha256:peer-desktop",
      sessionId: 7,
      seq: 2,
      event: { kind: "kind_from_a_newer_peer", detail: "payload-kept" },
    } as never);
    usePeerSessionsStore.setState({
      sessions: {
        [key]: {
          key,
          fingerprint: "sha256:peer-desktop",
          sessionId: 7,
          title: "t",
          deviceName: "MacBook Pro",
          status: "ready",
          highWater: 0,
          sending: false,
          transcript,
        },
      },
    });

    render(
      <PeerPanel
        fingerprint="sha256:peer-desktop"
        sessionId={7}
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
});
