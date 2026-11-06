// 渲染计数：侧栏一个会话的状态变化只重渲染该会话所在的组，且同组里没变的行
// 保持同一个对象引用（行 memo 的前提）。
import { act, render } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";

import "@/i18n";

type SpyRow = { id: string; status?: string };
const renders = new Map<string, number>();
const lastRows = new Map<string, Map<string, SpyRow>>();

vi.mock("@agentre-hub/agentre-ui", async (importOriginal) => {
  const orig = await importOriginal<Record<string, unknown>>();
  return {
    ...orig,
    ProjectSessionGroup: (props: {
      persistenceKey?: string;
      sessions: SpyRow[];
    }) => {
      const key = props.persistenceKey ?? "";
      renders.set(key, (renders.get(key) ?? 0) + 1);
      lastRows.set(key, new Map(props.sessions.map((s) => [s.id, s])));
      return null;
    },
  };
});

import { useSessionMetaStore } from "@/stores/session-meta-store";
import { useSessionStatusStore } from "@/stores/session-status-store";

import { IndexGroupRow, type IndexGroupRowProps } from "./index-group-row";
import type { IndexGroup } from "./index-projection";

const handlers = {
  onSessionSelect: vi.fn(),
  onOpenInNewTab: vi.fn(),
  onRenameSession: vi.fn(),
  onDeleteSession: vi.fn(),
  onNewSession: vi.fn(),
  onOpenNotChattable: vi.fn(),
  onTogglePin: vi.fn(),
  onOpenSettings: vi.fn(),
  onAddSubProject: vi.fn(),
  onOpenTerminal: vi.fn(),
  onMergeInto: vi.fn(),
  onDeleteProject: vi.fn(),
};
const agentInfoOf = () => ({ name: "A", color: "agent-1" });
const projectInfoOf = () => null;

function group(key: string, ids: number[]): IndexGroup {
  return {
    key,
    kind: "free",
    refID: 0,
    depth: 0,
    sessionIDs: ids,
    total: ids.length,
  };
}
const GA = group("ga", [1, 2, 3]);
const GB = group("gb", [4, 5]);

function rowProps(g: IndexGroup): IndexGroupRowProps {
  return {
    group: g,
    axis: "time",
    selectedSessionID: 0,
    visibleSessionIDs: null,
    subtreeSessionIDs: g.sessionIDs,
    allLocalPathsMissing: false,
    projectInfoOf,
    agentInfoOf,
    handlers,
  };
}

function mount() {
  return render(
    <>
      <IndexGroupRow {...rowProps(GA)} />
      <IndexGroupRow {...rowProps(GB)} />
    </>,
  );
}

describe("IndexGroupRow render isolation", () => {
  beforeEach(() => {
    renders.clear();
    lastRows.clear();
    useSessionMetaStore.getState().__reset();
    useSessionStatusStore.getState().__reset();
    useSessionMetaStore.getState().bulkUpsert(
      [1, 2, 3, 4, 5].map((id) => [
        id,
        {
          agentId: 1,
          agentName: "A",
          agentColor: "agent-1",
          title: `s${id}`,
          lastMessageAt: 100 + id,
          lastReadAt: 1000,
        },
      ]),
    );
  });

  it("Given two groups, When a session in one goes running, Then the other group does not re-render", () => {
    mount();
    const gbBefore = renders.get("gb");
    const gaBefore = renders.get("ga") ?? 0;

    act(() => {
      useSessionStatusStore
        .getState()
        .upsert(2, { agentStatus: "running", needsAttention: false });
    });

    expect(renders.get("gb")).toBe(gbBefore);
    expect(renders.get("ga")).toBeGreaterThan(gaBefore);
    // 变化的那一行确实反映了新状态（不能因为少渲染而丢更新）。
    expect(lastRows.get("ga")?.get("2")?.status).toBe("running");
  });

  it("Given a group, When one session changes, Then untouched rows keep the same object", () => {
    mount();
    const before = new Map(lastRows.get("ga"));

    act(() => {
      useSessionStatusStore
        .getState()
        .upsert(2, { agentStatus: "running", needsAttention: false });
    });

    const after = lastRows.get("ga")!;
    expect(after.get("1")).toBe(before.get("1"));
    expect(after.get("3")).toBe(before.get("3"));
    expect(after.get("2")).not.toBe(before.get("2"));
  });

  it("Given a group, When a session's title changes, Then that row updates", () => {
    mount();
    act(() => {
      useSessionMetaStore.getState().bulkUpsert([[3, { title: "renamed" }]]);
    });
    expect((lastRows.get("ga")?.get("3") as { title?: string }).title).toBe(
      "renamed",
    );
  });
});
