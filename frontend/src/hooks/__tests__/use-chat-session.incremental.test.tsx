import { act, renderHook, waitFor } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";

vi.mock("../../../wailsjs/go/app/App", () => ({
  LoadChatSession: vi.fn(),
  LoadChatMessageBlocks: vi.fn(),
  LoadChatSessionBlocksByType: vi.fn(),
}));

import {
  LoadChatMessageBlocks,
  LoadChatSession,
  LoadChatSessionBlocksByType,
} from "../../../wailsjs/go/app/App";
import { deriveBackgroundTasks } from "@/components/agentre/background-tasks/derive";
import { useTurnSettledCleanup } from "@/components/agentre/chat-panel/use-turn-settled-cleanup";
import { deriveSessionChanges } from "@/components/agentre/chat-context-sidebar/derive";
import { deriveTaskProgress } from "@/components/agentre/task-progress/derive";
import { useChatStreamsStore } from "@/stores/chat-streams-store";
import { useSessionConnStore } from "@/stores/session-conn-store";
import { useSessionMetaStore } from "@/stores/session-meta-store";
import { useSessionStatusStore } from "@/stores/session-status-store";
import { useChatSession } from "../use-chat-session";
import type { ChatMessage } from "../use-chat-session";

const loadChatSession = LoadChatSession as ReturnType<typeof vi.fn>;
const loadBlocksByType = LoadChatSessionBlocksByType as ReturnType<
  typeof vi.fn
>;
const loadMessageBlocks = LoadChatMessageBlocks as ReturnType<typeof vi.fn>;

// ─── 假后端 ──────────────────────────────────────────────────────────────────
//
// 与 chat_svc 读路径同一口径(spec 2026-11-05 决策 4):每条消息带一个版本,任何
// 一次写入把它推到全局计数器的下一个值;窗口内(最后 WINDOW 条)带完整正文,窗口外
// 只有元数据;按类型点查只交回点名类型的块。cursor=false 模拟不懂游标的后端 ——
// 响应不带 version / messageIds、无视 sinceVersion,前端于是一直走全量,作为「全量
// 重读」的参照答案(消息自身的 version 两边都带,逐字比较才有意义)。

const WINDOW = 2;

type Row = { id: number; seq: number; role: string; blocks: unknown[] };
type StoredRow = Row & { version: number };

class FakeDb {
  rows: StoredRow[] = [];
  clock = 0;
  constructor(
    readonly sessionId: number,
    readonly cursor: boolean,
    rows: Row[],
  ) {
    for (const r of rows) this.put(r);
  }

  put(row: Row): void {
    const next = { ...row, version: ++this.clock };
    const at = this.rows.findIndex((r) => r.id === row.id);
    if (at >= 0) this.rows[at] = next;
    else this.rows.push(next);
    this.rows.sort((a, b) => a.seq - b.seq);
  }

  patch(id: number, blocks: unknown[]): void {
    const row = this.rows.find((r) => r.id === id)!;
    this.put({ ...row, blocks });
  }

  remove(id: number): void {
    this.rows = this.rows.filter((r) => r.id !== id);
  }

  // restore 把一条消息搬回会话而不推进版本(恢复流程) —— 增量读看不见它。
  restore(row: Row, version: number): void {
    this.rows.push({ ...row, version });
    this.rows.sort((a, b) => a.seq - b.seq);
  }

  private project(row: StoredRow, types?: string[]): ChatMessage {
    const inWindow = this.rows.indexOf(row) >= this.rows.length - WINDOW;
    const blocks = types
      ? (row.blocks as Array<{ type: string }>).filter((b) =>
          types.includes(b.type),
        )
      : inWindow
        ? row.blocks
        : [];
    return {
      id: row.id,
      sessionId: this.sessionId,
      role: row.role,
      seq: row.seq,
      createtime: row.seq * 1000,
      blocks: structuredClone(blocks),
      blocksLoaded: !types && inWindow,
      version: row.version,
    } as unknown as ChatMessage;
  }

  private select(since: number | undefined): StoredRow[] {
    const cut = this.cursor ? (since ?? 0) : 0;
    return this.rows.filter((r) => r.version > cut);
  }

  private envelope() {
    return this.cursor
      ? { version: this.clock, messageIds: this.rows.map((r) => r.id) }
      : {};
  }

  loadSession(req: { sessionId: number; sinceVersion?: number }) {
    return {
      session: {
        id: this.sessionId,
        agentId: 1,
        agentName: "Eng",
        title: "x",
        agentStatus: "idle",
        lastMessageAt: 0,
        createtime: 0,
      },
      messages: this.select(req.sinceVersion).map((r) => this.project(r)),
      ...this.envelope(),
    };
  }

  loadByType(req: {
    sessionId: number;
    types: string[];
    sinceVersion?: number;
  }) {
    return {
      messages: this.select(req.sinceVersion).map((r) =>
        this.project(r, req.types),
      ),
      ...this.envelope(),
    };
  }
}

function serve(...dbs: FakeDb[]): void {
  const of = (sid: number) => dbs.find((d) => d.sessionId === sid)!;
  loadChatSession.mockImplementation(
    async (req: { sessionId: number; sinceVersion?: number }) =>
      of(req.sessionId).loadSession(req),
  );
  loadBlocksByType.mockImplementation(
    async (req: {
      sessionId: number;
      types: string[];
      sinceVersion?: number;
    }) => of(req.sessionId).loadByType(req),
  );
}

// ─── 夹具:一条跨窗口的会话 ─────────────────────────────────────────────────

const ROOT = "/repo";

function bgAgent(status: string) {
  return {
    type: "tool_use",
    toolName: "Agent",
    toolUseId: "tu-bg",
    subagent: { kind: "local_agent", status, taskDescription: "probe" },
  };
}

function edit(toolUseId: string, path: string, plus: number, minus: number) {
  return {
    type: "tool_use",
    toolName: "Edit",
    toolUseId,
    toolInput: { file_path: path },
    canonical: {
      kind: "file.edit",
      fileEdit: { files: [{ kind: "modified", path, plus, minus, hunks: [] }] },
    },
  };
}

function write(toolUseId: string, path: string, lines: number) {
  return {
    type: "tool_use",
    toolName: "Write",
    toolUseId,
    toolInput: { file_path: path },
    canonical: {
      kind: "file.write",
      fileWrite: { path, content: "", lines, bytes: 0 },
    },
  };
}

function plan(toolUseId: string, status: string) {
  return {
    type: "tool_use",
    toolName: "TodoWrite",
    toolUseId,
    canonical: {
      kind: "plan.update",
      planUpdate: { steps: [{ id: "s1", step: "ship it", status }] },
    },
  };
}

function text(t: string) {
  return { type: "text", text: t };
}

function result(toolUseId: string) {
  return { type: "tool_result", toolUseId, content: "ok" };
}

function seedRows(): Row[] {
  return [
    { id: 1, seq: 1, role: "user", blocks: [text("start a background probe")] },
    {
      id: 2,
      seq: 2,
      role: "assistant",
      blocks: [
        bgAgent("running"),
        edit("tu-e1", "/repo/a.ts", 3, 1),
        result("tu-e1"),
      ],
    },
    { id: 3, seq: 3, role: "user", blocks: [text("plan it")] },
    {
      id: 4,
      seq: 4,
      role: "assistant",
      blocks: [text("planned"), plan("tu-p1", "in_progress")],
    },
  ];
}

// 一轮收尾之后的数据库:窗口外的旧消息被改写(后台任务翻 completed)、追加了新的一轮。
function finishTurn(db: FakeDb): void {
  db.patch(2, [
    bgAgent("completed"),
    edit("tu-e1", "/repo/a.ts", 3, 1),
    result("tu-e1"),
  ]);
  db.put({ id: 5, seq: 5, role: "user", blocks: [text("write b")] });
  db.put({
    id: 6,
    seq: 6,
    role: "assistant",
    blocks: [write("tu-w1", "/repo/b.ts", 7), plan("tu-p2", "completed")],
  });
}

function derivedViews(messages: ChatMessage[]) {
  return {
    changes: deriveSessionChanges(messages, ROOT),
    tasks: deriveBackgroundTasks(messages, []),
    progress: deriveTaskProgress(messages, []),
  };
}

type Scenario = (db: FakeDb, reload: () => Promise<void>) => Promise<void>;

// runScenario 在一个全新的 hook 上:首次打开 → 执行剧本 → 交回最终消息表。
async function runScenario(
  cursor: boolean,
  scenario: Scenario,
  rows: Row[] = seedRows(),
): Promise<ChatMessage[]> {
  const db = new FakeDb(9, cursor, rows);
  serve(db);
  const { result, unmount } = renderHook(() => useChatSession(9));
  await waitFor(() => expect(result.current.session).not.toBeNull());
  await waitFor(() => expect(result.current.loading).toBe(false));
  await scenario(db, () => result.current.reload());
  const messages = result.current.messages;
  unmount();
  return messages;
}

function sinceVersions(
  fn: ReturnType<typeof vi.fn>,
): Array<number | undefined> {
  return fn.mock.calls.map(
    (c) => (c[0] as { sinceVersion?: number }).sinceVersion,
  );
}

function resetAll(): void {
  loadChatSession.mockReset();
  loadBlocksByType.mockReset();
  loadMessageBlocks.mockReset();
  useChatStreamsStore.setState({ streams: new Map() });
  useSessionConnStore.getState().__reset();
  useSessionMetaStore.getState().__reset();
  useSessionStatusStore.getState().__reset();
}

// incrementalMatchesFull 跑同一剧本两遍:一遍对着懂游标的后端(增量合并),一遍对着
// 不懂游标的后端(每次全量重读),两边的最终消息表必须逐字一致。
async function incrementalMatchesFull(scenario: Scenario) {
  resetAll();
  const full = await runScenario(false, scenario);
  resetAll();
  const merged = await runScenario(true, scenario);
  return { merged, full };
}

describe("useChatSession incremental reload (spec 2026-11-05 decision 4)", () => {
  beforeEach(resetAll);

  it("Given a loaded session, When a turn finishes, Then the turn-end reload sends the last version as its cursor", async () => {
    const db = new FakeDb(9, true, seedRows());
    serve(db);
    const { result } = renderHook(() => {
      const s = useChatSession(9);
      useTurnSettledCleanup({
        sessionId: 9,
        setMessages: s.setMessages,
        reloadSession: s.reload,
      });
      return s;
    });
    await waitFor(() => expect(result.current.loading).toBe(false));
    const opened = db.clock;
    // 首次打开是全量:不带游标。
    expect(sinceVersions(loadChatSession)).toEqual([undefined]);

    finishTurn(db);
    act(() => {
      useSessionStatusStore.getState().bumpDone(9, { kind: "done" });
    });

    await waitFor(() => expect(loadChatSession).toHaveBeenCalledTimes(2));
    expect(sinceVersions(loadChatSession)[1]).toBe(opened);
    await waitFor(() =>
      expect(result.current.messages.map((m) => m.id)).toEqual([
        1, 2, 3, 4, 5, 6,
      ]),
    );
  });

  it("Given messages appended and an old message rewritten, When the reload merges, Then the list equals a full reload", async () => {
    const { merged, full } = await incrementalMatchesFull(
      async (db, reload) => {
        finishTurn(db);
        await act(async () => {
          await reload();
        });
      },
    );
    // 增量那一遍确实只取了游标之后的那几条。
    const lastCall = loadChatSession.mock.results.at(-1)!;
    const lastResp = (await lastCall.value) as { messages: ChatMessage[] };
    expect(lastResp.messages.map((m) => m.id)).toEqual([2, 5, 6]);
    expect(sinceVersions(loadChatSession)).toEqual([undefined, 4]);

    expect(merged.map((m) => m.id)).toEqual([1, 2, 3, 4, 5, 6]);
    expect(merged).toEqual(full);
  });

  it("Given a message the backend deleted, When the reload merges, Then it is dropped like a full reload drops it", async () => {
    const { merged, full } = await incrementalMatchesFull(
      async (db, reload) => {
        db.remove(3);
        db.put({ id: 7, seq: 7, role: "user", blocks: [text("after delete")] });
        await act(async () => {
          await reload();
        });
      },
    );
    expect(merged.map((m) => m.id)).toEqual([1, 2, 4, 7]);
    expect(merged).toEqual(full);
  });

  it("Given an id in messageIds that is neither held nor returned, When the reload lands, Then it falls back to a full reload", async () => {
    const { merged, full } = await incrementalMatchesFull(
      async (db, reload) => {
        db.restore(
          { id: 50, seq: 3.5, role: "assistant", blocks: [text("restored")] },
          1,
        );
        await act(async () => {
          await reload();
        });
      },
    );
    // 增量读看不见那条被搬回来的消息 → 以游标 0 重读一次。
    expect(sinceVersions(loadChatSession)).toEqual([undefined, 4, undefined]);
    expect(merged.map((m) => m.id)).toEqual([1, 2, 3, 50, 4]);
    expect(merged).toEqual(full);
  });

  it("Given a different session, When the hook switches to it, Then its first load is full and its cursor is its own", async () => {
    const a = new FakeDb(9, true, seedRows());
    const b = new FakeDb(10, true, [
      { id: 100, seq: 1, role: "user", blocks: [text("other")] },
    ]);
    serve(a, b);
    const { result, rerender } = renderHook(({ sid }) => useChatSession(sid), {
      initialProps: { sid: 9 },
    });
    await waitFor(() => expect(result.current.session?.id).toBe(9));

    rerender({ sid: 10 });
    await waitFor(() =>
      expect(result.current.messages.map((m) => m.id)).toEqual([100]),
    );
    b.put({ id: 101, seq: 2, role: "assistant", blocks: [text("reply")] });
    await act(async () => {
      await result.current.reload();
    });
    expect(result.current.messages.map((m) => m.id)).toEqual([100, 101]);

    rerender({ sid: 9 });
    await waitFor(() =>
      expect(result.current.messages.map((m) => m.id)).toEqual([1, 2, 3, 4]),
    );

    const calls = loadChatSession.mock.calls.map(
      (c) => c[0] as { sessionId: number; sinceVersion?: number },
    );
    expect(calls).toEqual([
      { sessionId: 9 },
      { sessionId: 10 },
      { sessionId: 10, sinceVersion: 1 },
      { sessionId: 9 },
    ]);
  });

  it("Given derived views over the whole session, When the turn-end reload merges, Then changes, background tasks and task progress equal a full reload", async () => {
    const { merged, full } = await incrementalMatchesFull(
      async (db, reload) => {
        finishTurn(db);
        await act(async () => {
          await reload();
        });
      },
    );
    const got = derivedViews(merged);
    expect(got).toEqual(derivedViews(full));
    // 参照答案本身不是空的:三个视图都看到了窗口外的旧消息与新追加的一轮。
    expect(got.changes.map((c) => c.path).sort()).toEqual(["a.ts", "b.ts"]);
    expect(got.tasks.map((t) => [t.toolUseId, t.status])).toEqual([
      ["tu-bg", "completed"],
    ]);
    expect(got.progress.tasks.map((t) => t.status)).toEqual(["completed"]);
  });

  it("Given an incremental reload, When the derived-view blocks are fetched, Then the per-type query carries the same cursor", async () => {
    await runScenario(true, async (db, reload) => {
      finishTurn(db);
      await act(async () => {
        await reload();
      });
    });
    expect(loadBlocksByType.mock.calls.map((c) => c[0])).toEqual([
      { sessionId: 9, types: ["text", "tool_use"] },
      { sessionId: 9, types: ["text", "tool_use"], sinceVersion: 4 },
    ]);
  });

  // 点查失败时这次交回的窗口外消息没补上派生块;游标若照常推进,之后的增量读再也
  // 不交回它们,后台任务面板就一直缺着那张卡,直到下一次全量读。
  it("Given the per-type query fails during an incremental reload, When the next reload runs, Then it reuses the old cursor and the derived views catch up", async () => {
    const db = new FakeDb(9, true, seedRows());
    serve(db);
    const { result } = renderHook(() => useChatSession(9));
    await waitFor(() => expect(result.current.loading).toBe(false));

    finishTurn(db);
    loadBlocksByType.mockRejectedValueOnce(new Error("bridge hiccup"));
    await act(async () => {
      await result.current.reload();
    });
    await act(async () => {
      await result.current.reload();
    });

    expect(sinceVersions(loadChatSession)).toEqual([undefined, 4, 4]);
    expect(
      deriveBackgroundTasks(result.current.messages, []).map((t) => t.status),
    ).toEqual(["completed"]);
  });

  // 轮内 reload 时在流的那条消息正文被冻在开流那一刻(retainLiveMessageBlocks):
  // 手上那一份比它的版本旧。游标若越过它,流收尾后的增量读再也取不回它的最终正文。
  it("Given a message frozen under a live stream, When the stream ends without a final message, Then the next reload still fetches its persisted blocks", async () => {
    const rows: Row[] = [
      { id: 40, seq: 1, role: "user", blocks: [text("go")] },
      { id: 42, seq: 2, role: "assistant", blocks: [text("draft")] },
    ];
    const scenario: Scenario = async (db, reload) => {
      act(() => {
        useChatStreamsStore.getState().openStream({
          name: "chat:event:9:42",
          sessionId: 9,
          assistantMessageId: 42,
          streamStartedAt: 1,
        });
      });
      db.patch(42, [text("draft"), text("final")]);
      await act(async () => {
        await reload();
      });
      act(() => {
        useChatStreamsStore.getState().finishStream(9, 42, { kind: "error" });
      });
      await act(async () => {
        await reload();
      });
    };
    resetAll();
    const full = await runScenario(false, scenario, rows);
    resetAll();
    const merged = await runScenario(true, scenario, rows);
    expect(merged.find((m) => m.id === 42)?.blocks).toEqual([
      text("draft"),
      text("final"),
    ]);
    expect(merged).toEqual(full);
  });

  // 恢复后没点开过的 tab(DormantSessionTab)也接自主续轮:它在 assistant 行建出的那一刻
  // 开流,之后全局宿主把这一轮的块攒进 liveBlocks,后端同时在轮内 checkpoint 落库。tab
  // 首次被点开时面板手上一条消息都没有 —— 库里那份正文与 liveBlocks 是同一段,两路各画
  // 一遍就是重复的工具卡(sess-3396 的撞 key)。开流时消息还是空的,正文应冻在空上。
  it("Given an autonomous turn streamed into a never-opened tab, When the tab's first load lands mid-turn, Then its checkpointed blocks are not drawn a second time", async () => {
    const db = new FakeDb(9, true, [
      { id: 40, seq: 1, role: "user", blocks: [text("go")] },
      { id: 42, seq: 2, role: "assistant", blocks: [] },
    ]);
    serve(db);
    act(() => {
      useChatStreamsStore.getState().openStream({
        name: "chat:event:9:42",
        sessionId: 9,
        assistantMessageId: 42,
        streamStartedAt: 1,
        startsEmpty: true,
      });
      useChatStreamsStore.getState().appendLiveToolUse(9, 42, {
        toolName: "Bash",
        toolUseId: "tu-live",
      });
    });
    db.patch(42, [
      { type: "tool_use", toolName: "Bash", toolUseId: "tu-live" },
    ]);

    const { result } = renderHook(() => useChatSession(9));
    await waitFor(() => expect(result.current.loading).toBe(false));

    const persisted = result.current.messages.find((m) => m.id === 42);
    const live =
      useChatStreamsStore.getState().streams.get(9)?.get(42)?.liveBlocks ?? [];
    const drawn = [...(persisted?.blocks ?? []), ...live].filter(
      (b) => b.toolUseId === "tu-live",
    );
    expect(drawn).toHaveLength(1);
  });

  // 往上取正文的请求在途时一轮收尾:增量 reload 先落、把游标推过了窗口外那条旧消息的新
  // 版本,随后才回来的取正文响应带的是那条消息改写**之前**的正文。游标若不退回到这份
  // 旧正文的版本,之后的增量读再也不交回它,后台任务面板就永远停在旧状态(改动前每次
  // reload 都全量点查,会把它修回来)。
  it("Given an earlier-blocks fetch that lands after an incremental reload already passed a rewritten message, When the next reload runs, Then the stale copy is refreshed", async () => {
    const db = new FakeDb(9, true, seedRows());
    serve(db);
    const { result } = renderHook(() => useChatSession(9));
    await waitFor(() => expect(result.current.loading).toBe(false));

    // 取正文请求发出那一刻的快照:消息 1、2 改写之前的完整正文。
    const staleEarlier = {
      messages: db.rows
        .filter((r) => r.seq < 3)
        .map((r) => ({
          id: r.id,
          sessionId: 9,
          role: r.role,
          seq: r.seq,
          createtime: r.seq * 1000,
          blocks: structuredClone(r.blocks),
          blocksLoaded: true,
          version: r.version,
        })),
      hasMore: false,
    };
    let resolveEarlier!: (v: unknown) => void;
    loadMessageBlocks.mockReturnValueOnce(
      new Promise((r) => (resolveEarlier = r)),
    );
    let earlier!: Promise<void>;
    act(() => {
      earlier = result.current.loadEarlierBlocks();
    });

    finishTurn(db);
    await act(async () => {
      await result.current.reload();
    });
    await act(async () => {
      resolveEarlier(staleEarlier);
      await earlier;
    });
    await act(async () => {
      await result.current.reload();
    });

    expect(
      deriveBackgroundTasks(result.current.messages, []).map((t) => t.status),
    ).toEqual(["completed"]);
  });

  // 两次 reload 在途、先发的那次后回来:它带着更旧的快照,不能盖掉后发那次已经落下的
  // 结果,也不能把游标推进到与手上数据不符的位置。
  it("Given two reloads in flight, When the earlier one resolves last, Then its older snapshot is discarded", async () => {
    const db = new FakeDb(9, true, seedRows());
    serve(db);
    const { result } = renderHook(() => useChatSession(9));
    await waitFor(() => expect(result.current.loading).toBe(false));

    const stale = db.loadSession({ sessionId: 9, sinceVersion: 4 });
    finishTurn(db);
    const fresh = db.loadSession({ sessionId: 9, sinceVersion: 4 });
    let resolveStale!: (v: unknown) => void;
    let resolveFresh!: (v: unknown) => void;
    loadChatSession
      .mockReturnValueOnce(new Promise((r) => (resolveStale = r)))
      .mockReturnValueOnce(new Promise((r) => (resolveFresh = r)));

    let first!: Promise<void>;
    let second!: Promise<void>;
    act(() => {
      first = result.current.reload();
      second = result.current.reload();
    });
    await act(async () => {
      resolveFresh(fresh);
      await second;
    });
    await act(async () => {
      resolveStale(stale);
      await first;
    });

    expect(result.current.messages.map((m) => m.id)).toEqual([
      1, 2, 3, 4, 5, 6,
    ]);
    serve(db);
    await act(async () => {
      await result.current.reload();
    });
    expect(sinceVersions(loadChatSession).at(-1)).toBe(db.clock);
  });
});
