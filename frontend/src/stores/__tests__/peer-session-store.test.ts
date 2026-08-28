import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";

const mocks = vi.hoisted(() => ({
  PeerAttach: vi.fn(),
  PeerPull: vi.fn(),
  PeerSteer: vi.fn(),
  PeerSubmitAnswer: vi.fn(),
  PeerSubmitToolPermission: vi.fn(),
  PeerDetach: vi.fn().mockResolvedValue(undefined),
}));
vi.mock("../../../wailsjs/go/app/App", () => mocks);

type EventsOnHandler = (payload: unknown) => void;
const eventsOn = vi.hoisted(() => {
  const holder: { handler: EventsOnHandler | null } = { handler: null };
  const fn = vi.fn((_name: string, h: EventsOnHandler) => {
    holder.handler = h;
    return vi.fn();
  });
  return { holder, fn };
});
vi.mock("../../../wailsjs/runtime/runtime", () => ({
  EventsOn: eventsOn.fn,
}));

import {
  PeerAttach,
  PeerPull,
  PeerSteer,
  PeerSubmitAnswer,
  PeerSubmitToolPermission,
  PeerDetach,
} from "../../../wailsjs/go/app/App";
import { usePeerSessionsStore } from "../peer-session-store";

const frame = (
  seq: number,
  kind: string,
  extra: Record<string, unknown> = {},
) => ({
  fingerprint: "sha256:peer-desktop",
  sessionId: 7,
  seq,
  event: { kind, ...extra },
});

beforeEach(() => {
  vi.clearAllMocks();
  usePeerSessionsStore.setState({ sessions: {} });
});

afterEach(() => {
  usePeerSessionsStore.setState({ sessions: {} });
});

describe("peer-session-store", () => {
  it("attach then pull rebuilds history and enters ready", async () => {
    (PeerAttach as unknown as ReturnType<typeof vi.fn>).mockResolvedValue({
      latestSeq: 2,
      lifecycleState: "idle",
    });
    (PeerPull as unknown as ReturnType<typeof vi.fn>).mockResolvedValue({
      notifications: [
        {
          seq: 1,
          params: { sessionId: 7, event: { kind: "user_message", text: "hi" } },
        },
        {
          seq: 2,
          params: {
            sessionId: 7,
            event: { kind: "text_delta", text: "hello" },
          },
        },
      ],
      cursor: 2,
      hasMore: false,
      oldestSeq: 1,
    });

    await usePeerSessionsStore.getState().attach({
      fingerprint: "sha256:peer-desktop",
      sessionId: 7,
      title: "t",
      deviceName: "d",
    });

    const s = usePeerSessionsStore.getState().sessions["sha256:peer-desktop:7"];
    expect(s.status).toBe("ready");
    expect(s.transcript.messages).toHaveLength(2);
    expect(s.transcript.messages[0]).toMatchObject({
      role: "user",
      blocks: [{ type: "text", text: "hi" }],
    });
    expect(s.transcript.messages[1].blocks[0]).toMatchObject({
      type: "text",
      text: "hello",
    });
  });

  it("live frames after attach are reduced; frames covered by pull are dropped", async () => {
    (PeerAttach as unknown as ReturnType<typeof vi.fn>).mockResolvedValue({
      latestSeq: 2,
      lifecycleState: "idle",
    });
    (PeerPull as unknown as ReturnType<typeof vi.fn>).mockResolvedValue({
      notifications: [],
      cursor: 2,
      hasMore: false,
      oldestSeq: 1,
    });
    await usePeerSessionsStore.getState().attach({
      fingerprint: "sha256:peer-desktop",
      sessionId: 7,
      title: "t",
      deviceName: "d",
    });

    // 高水位(2)之后的实时帧正常归约
    eventsOn.holder.handler?.([frame(3, "text_delta", { text: "live" })]);
    let s = usePeerSessionsStore.getState().sessions["sha256:peer-desktop:7"];
    expect(s.transcript.messages[0].blocks[0]).toMatchObject({
      type: "text",
      text: "live",
    });

    // 重复帧（≤ 游标）被去重丢弃
    eventsOn.holder.handler?.([frame(3, "text_delta", { text: "live" })]);
    eventsOn.holder.handler?.([frame(2, "text_delta", { text: "dup" })]);
    s = usePeerSessionsStore.getState().sessions["sha256:peer-desktop:7"];
    expect(s.transcript.messages).toHaveLength(1);
    expect(s.transcript.messages[0].blocks[0]).toMatchObject({
      type: "text",
      text: "live",
    });
  });

  it("一批多帧只触发一次 store 更新,且逐帧按序归约", async () => {
    // 合帧的意义在这里:一批 N 帧过去只是 N 次 setState + N 次 Wails 广播。
    // 帧本身不合并(各自带 seq),所以归约结果必须与逐帧送达完全一致。
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
    await usePeerSessionsStore.getState().attach({
      fingerprint: "sha256:peer-desktop",
      sessionId: 7,
      title: "t",
      deviceName: "d",
    });

    let updates = 0;
    const unsubscribe = usePeerSessionsStore.subscribe(() => {
      updates += 1;
    });

    eventsOn.holder.handler?.([
      frame(1, "text_delta", { text: "he" }),
      frame(2, "text_delta", { text: "ll" }),
      frame(3, "text_delta", { text: "o" }),
    ]);
    unsubscribe();

    expect(updates).toBe(1);
    const s = usePeerSessionsStore.getState().sessions["sha256:peer-desktop:7"];
    expect(s.transcript.messages[0].blocks[0]).toMatchObject({
      type: "text",
      text: "hello",
    });
  });

  it("一批里混着多条会话的帧,各自落到自己那条", async () => {
    // batcher 是全局的,一批里可以同时装着不同对端 / 不同会话的帧。
    for (const sessionId of [7, 9]) {
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
      await usePeerSessionsStore.getState().attach({
        fingerprint: "sha256:peer-desktop",
        sessionId,
        title: "t",
        deviceName: "d",
      });
    }

    eventsOn.holder.handler?.([
      { ...frame(1, "text_delta", { text: "seven" }), sessionId: 7 },
      { ...frame(1, "text_delta", { text: "nine" }), sessionId: 9 },
    ]);

    const sessions = usePeerSessionsStore.getState().sessions;
    expect(
      sessions["sha256:peer-desktop:7"].transcript.messages[0].blocks[0],
    ).toMatchObject({ type: "text", text: "seven" });
    expect(
      sessions["sha256:peer-desktop:9"].transcript.messages[0].blocks[0],
    ).toMatchObject({ type: "text", text: "nine" });
  });

  it("steer sends through the peer binding", async () => {
    (PeerAttach as unknown as ReturnType<typeof vi.fn>).mockResolvedValue({
      latestSeq: 0,
    });
    (PeerPull as unknown as ReturnType<typeof vi.fn>).mockResolvedValue({
      notifications: [],
      cursor: 0,
      hasMore: false,
    });
    await usePeerSessionsStore.getState().attach({
      fingerprint: "sha256:peer-desktop",
      sessionId: 7,
      title: "t",
      deviceName: "d",
    });

    const ok = await usePeerSessionsStore
      .getState()
      .steer("sha256:peer-desktop", 7, "接着干");
    expect(ok).toBe(true);
    expect(PeerSteer).toHaveBeenCalledWith(
      expect.objectContaining({
        fingerprint: "sha256:peer-desktop",
        sessionId: 7,
        text: "接着干",
      }),
    );
  });

  it("submitAnswer surfaces alreadyHandled (R10)", async () => {
    (PeerAttach as unknown as ReturnType<typeof vi.fn>).mockResolvedValue({
      latestSeq: 0,
    });
    (PeerPull as unknown as ReturnType<typeof vi.fn>).mockResolvedValue({
      notifications: [],
      cursor: 0,
      hasMore: false,
    });
    (PeerSubmitAnswer as unknown as ReturnType<typeof vi.fn>).mockResolvedValue(
      { alreadyHandled: true },
    );
    await usePeerSessionsStore.getState().attach({
      fingerprint: "sha256:peer-desktop",
      sessionId: 7,
      title: "t",
      deviceName: "d",
    });

    const res = await usePeerSessionsStore.getState().submitAnswer({
      fingerprint: "sha256:peer-desktop",
      sessionId: 7,
      requestId: "req-1",
      answers: [],
    });
    expect(res).toEqual({ alreadyHandled: true });
  });

  it("submitToolPermission surfaces alreadyHandled (R10)", async () => {
    (PeerAttach as unknown as ReturnType<typeof vi.fn>).mockResolvedValue({
      latestSeq: 0,
    });
    (PeerPull as unknown as ReturnType<typeof vi.fn>).mockResolvedValue({
      notifications: [],
      cursor: 0,
      hasMore: false,
    });
    (
      PeerSubmitToolPermission as unknown as ReturnType<typeof vi.fn>
    ).mockResolvedValue({ alreadyHandled: true });
    await usePeerSessionsStore.getState().attach({
      fingerprint: "sha256:peer-desktop",
      sessionId: 7,
      title: "t",
      deviceName: "d",
    });

    const res = await usePeerSessionsStore.getState().submitToolPermission({
      fingerprint: "sha256:peer-desktop",
      sessionId: 7,
      requestId: "p-1",
      allow: true,
    });
    expect(res).toEqual({ alreadyHandled: true });
  });

  it("detach removes the local session and calls PeerDetach — does not delete the remote session", async () => {
    (PeerAttach as unknown as ReturnType<typeof vi.fn>).mockResolvedValue({
      latestSeq: 0,
    });
    (PeerPull as unknown as ReturnType<typeof vi.fn>).mockResolvedValue({
      notifications: [],
      cursor: 0,
      hasMore: false,
    });
    await usePeerSessionsStore.getState().attach({
      fingerprint: "sha256:peer-desktop",
      sessionId: 7,
      title: "t",
      deviceName: "d",
    });

    usePeerSessionsStore.getState().detach("sha256:peer-desktop", 7);
    expect(
      usePeerSessionsStore.getState().sessions["sha256:peer-desktop:7"],
    ).toBeUndefined();
    expect(PeerDetach).toHaveBeenCalledWith("sha256:peer-desktop", 7);
  });

  it("attach failure marks the session error instead of leaving it half-open", async () => {
    (PeerAttach as unknown as ReturnType<typeof vi.fn>).mockRejectedValue(
      new Error("Agentre is not running on that computer"),
    );
    await usePeerSessionsStore.getState().attach({
      fingerprint: "sha256:peer-desktop",
      sessionId: 7,
      title: "t",
      deviceName: "d",
    });
    const s = usePeerSessionsStore.getState().sessions["sha256:peer-desktop:7"];
    expect(s.status).toBe("error");
    expect(s.error).toContain("not running");
  });
});
