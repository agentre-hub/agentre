// 渲染计数：一行的数据变化只重渲染这一行（SessionRow memo + SessionGroup 给的稳定 handler）。
// 用 StatusDot 当探针：它在 SessionRow 内部，SessionRow 每渲染一次它就被重新创建一次。
import { render } from "@testing-library/react";
import * as React from "react";
import { beforeEach, describe, expect, it, vi } from "vitest";

const dotRenders = new Map<string, number>();

vi.mock("../ui/status-dot", () => ({
  StatusDot: ({ status }: { status: string }) => {
    dotRenders.set(status, (dotRenders.get(status) ?? 0) + 1);
    return null;
  },
}));

import { SessionGroup } from "./session-group";
import type { SessionRowModel } from "./types";

const rows = (title: string): SessionRowModel[] => [
  { id: "1", status: "idle", title },
  { id: "2", status: "running", title: "two" },
  { id: "3", status: "error", title: "three" },
];

function Host({
  sessions,
  tick,
}: {
  sessions: SessionRowModel[];
  tick: number;
}) {
  // 宿主每次渲染都新建 handler（桌面端 rowHandlers 就是这样）。
  return (
    <SessionGroup
      data-tick={tick}
      renderHeader={() => null}
      defaultExpanded
      sessions={sessions}
      onSessionSelect={() => undefined}
      onOpenInNewTab={() => undefined}
      onRenameSession={() => undefined}
      onDeleteSession={() => undefined}
    />
  );
}

describe("SessionGroup row render isolation", () => {
  beforeEach(() => dotRenders.clear());

  it("Given three rows, When one row's model changes, Then only that row re-renders", () => {
    const base = rows("one");
    const { rerender } = render(<Host sessions={base} tick={0} />);
    expect(dotRenders.get("running")).toBe(1);

    // 只换第 1 行的对象；第 2、3 行沿用同一个引用。
    const next = [{ ...base[0], title: "one!" }, base[1], base[2]];
    rerender(<Host sessions={next} tick={1} />);

    expect(dotRenders.get("idle")).toBe(2);
    expect(dotRenders.get("running")).toBe(1);
    expect(dotRenders.get("error")).toBe(1);
  });

  it("Given a row whose selected flag flips, When group re-renders, Then it re-renders and others do not", () => {
    const base = rows("one");
    const { rerender } = render(
      <SessionGroup
        renderHeader={() => null}
        defaultExpanded
        sessions={base}
        selectedSessionId="1"
      />,
    );
    rerender(
      <SessionGroup
        renderHeader={() => null}
        defaultExpanded
        sessions={base}
        selectedSessionId="2"
      />,
    );
    expect(dotRenders.get("idle")).toBe(2);
    expect(dotRenders.get("running")).toBe(2);
    expect(dotRenders.get("error")).toBe(1);
    void React;
  });
});
