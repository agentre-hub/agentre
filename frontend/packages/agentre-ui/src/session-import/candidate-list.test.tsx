import { fireEvent, render, screen, within } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";

import { CandidateList } from "./candidate-list";
import { buildCandidateGroups } from "./candidate-groups";
import type { ImportCandidateView } from "./ports";

/**
 * 左栏的三条验收（规格「UI 与状态」）：按时间分组、已导入的不可选**且原因读得出来**、
 * 键盘上下移动并选中。
 */
const NOW = new Date("2026-08-26T15:00:00").getTime();
const HOUR = 60 * 60 * 1000;
const DAY = 24 * HOUR;

function candidate(over: Partial<ImportCandidateView>): ImportCandidateView {
  return {
    backend: "claudecode",
    providerSessionId: "s1",
    title: "Refactor wire protocol",
    cwd: "/Code/agentre",
    startedAt: NOW - HOUR,
    endedAt: NOW - HOUR,
    turns: 42,
    origin: "terminal",
    locator: "loc-1",
    imported: false,
    importedSessionId: "",
    ...over,
  };
}

describe("buildCandidateGroups", () => {
  it("Given 今天 / 昨天 / 更早各一条, When 分组, Then 三段各自成组、段内按末次活动倒序、空段不出现", () => {
    const groups = buildCandidateGroups(
      [
        candidate({ providerSessionId: "old", endedAt: NOW - 9 * DAY }),
        candidate({
          providerSessionId: "today-early",
          endedAt: NOW - 6 * HOUR,
        }),
        candidate({ providerSessionId: "yesterday", endedAt: NOW - 26 * HOUR }),
        candidate({ providerSessionId: "today-late", endedAt: NOW - HOUR }),
      ],
      NOW,
    );

    expect(groups.map((g) => g.bucket)).toEqual([
      "today",
      "yesterday",
      "earlier",
    ]);
    expect(groups[0].items.map((c) => c.providerSessionId)).toEqual([
      "today-late",
      "today-early",
    ]);
  });

  it("Given 全部都是今天的, When 分组, Then 只出一段（空段不摆一个什么都没有的标题）", () => {
    const groups = buildCandidateGroups([candidate({})], NOW);
    expect(groups).toHaveLength(1);
  });

  /**
   * 分组口径是「哪一天跑的」，不是「离现在几小时」—— 夏令时那两天这两种口径会分家：
   * 春季那一天只有 23 小时，「今天零点往回退 24 小时」落在**前天** 23:00 上，于是
   * 前天深夜那条会被标成「昨天」。用户按标题找的是「昨天那条」，翻到的却是前天的。
   */
  it("Given 春季夏令时切换那一天, When 分组, Then 前天深夜那条仍归「更早」", () => {
    const tz = process.env.TZ;
    process.env.TZ = "America/New_York";
    try {
      // 2026-03-08 是美东往前拨的那一天（那天只有 23 小时）。
      const now = new Date("2026-03-09T15:00:00-04:00").getTime();
      const groups = buildCandidateGroups(
        [
          candidate({
            providerSessionId: "day-before",
            // 前天（3-07）23:30 —— 与「昨天」隔着一整天。
            endedAt: new Date("2026-03-07T23:30:00-05:00").getTime(),
          }),
        ],
        now,
      );
      expect(groups.map((g) => g.bucket)).toEqual(["earlier"]);
    } finally {
      process.env.TZ = tz;
    }
  });
});

describe("候选列表", () => {
  function renderList(
    candidates: ImportCandidateView[],
    activeLocator: string | null = null,
  ) {
    const onActivate = vi.fn();
    const onOpenImported = vi.fn();
    render(
      <CandidateList
        candidates={candidates}
        activeLocator={activeLocator}
        onActivate={onActivate}
        onOpenImported={onOpenImported}
        now={NOW}
      />,
    );
    return { onActivate, onOpenImported };
  }

  it("Given 一条已导入过的候选, When 列表渲染, Then 它照常在列、被标成不可选，且不可选的原因读得出来（不是只变浅），并给「打开」的去处", () => {
    const { onOpenImported } = renderList([
      candidate({
        providerSessionId: "done",
        locator: "loc-done",
        imported: true,
        importedSessionId: "77",
      }),
    ]);

    const row = screen.getByTestId("import-candidate-done");
    expect(row.getAttribute("aria-disabled")).toBe("true");
    const reasonId = row.getAttribute("aria-describedby");
    expect(reasonId).toBeTruthy();
    expect(document.getElementById(reasonId!)?.textContent).toContain(
      "Already in agentre",
    );

    fireEvent.click(within(row).getByTestId("import-open-done"));
    expect(onOpenImported).toHaveBeenCalledWith("77");
  });

  it("Given 三条候选, When 在列表上按方向键, Then 选中沿摊平后的顺序移动，且 aria-activedescendant 跟着变（辅助技术感知得到）", () => {
    const rows = [
      candidate({ providerSessionId: "a", locator: "a", endedAt: NOW - HOUR }),
      candidate({
        providerSessionId: "b",
        locator: "b",
        endedAt: NOW - 2 * HOUR,
      }),
      candidate({
        providerSessionId: "c",
        locator: "c",
        endedAt: NOW - 3 * DAY,
      }),
    ];
    const { onActivate } = renderList(rows, "a");
    const list = screen.getByTestId("import-candidate-list");
    expect(list.getAttribute("aria-activedescendant")).toBe(
      "import-candidate-a",
    );

    fireEvent.keyDown(list, { key: "ArrowDown" });
    expect(onActivate).toHaveBeenLastCalledWith(
      expect.objectContaining({ locator: "b" }),
    );
  });

  it("Given 高亮停在「今天」那一段的最后一条, When 再按一次向下, Then 跨过分组头落到「更早」的第一条（分组头不占一格）", () => {
    const { onActivate } = renderList(
      [
        candidate({
          providerSessionId: "a",
          locator: "a",
          endedAt: NOW - HOUR,
        }),
        candidate({
          providerSessionId: "b",
          locator: "b",
          endedAt: NOW - 2 * HOUR,
        }),
        candidate({
          providerSessionId: "c",
          locator: "c",
          endedAt: NOW - 3 * DAY,
        }),
      ],
      "b",
    );

    fireEvent.keyDown(screen.getByTestId("import-candidate-list"), {
      key: "ArrowDown",
    });
    expect(onActivate).toHaveBeenLastCalledWith(
      expect.objectContaining({ locator: "c" }),
    );
  });

  it("Given 已导入那条排在中间, When 方向键走过去, Then 它照样被走到（跳过去等于把那条会话变没了，原因就再也读不到）", () => {
    const { onActivate } = renderList(
      [
        candidate({
          providerSessionId: "a",
          locator: "a",
          endedAt: NOW - HOUR,
        }),
        candidate({
          providerSessionId: "b",
          locator: "b",
          endedAt: NOW - 2 * HOUR,
          imported: true,
          importedSessionId: "9",
        }),
      ],
      "a",
    );

    fireEvent.keyDown(screen.getByTestId("import-candidate-list"), {
      key: "ArrowDown",
    });
    expect(onActivate).toHaveBeenLastCalledWith(
      expect.objectContaining({ locator: "b" }),
    );
  });

  it("Given 轮数未知（元信息里拿不到）, When 那一行渲染, Then 说的是「未知」而不是「0 轮」", () => {
    renderList([candidate({ providerSessionId: "u", turns: 0 })]);
    expect(screen.getByTestId("import-candidate-u").textContent).toContain(
      "Turn count unknown",
    );
  });
});
