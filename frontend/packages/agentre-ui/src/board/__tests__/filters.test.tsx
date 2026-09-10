import { act, fireEvent, render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, describe, expect, it, vi } from "vitest";

import { BoardFilterBar } from "../board-filter-bar";
import {
  activeConditionCount,
  buildFilterChips,
  dropChip,
} from "../query-conditions";
import { BOARD_SEARCH_DEBOUNCE_MS } from "../use-board-query";
import {
  EMPTY_BOARD_QUERY,
  type BoardQuery,
  type LabelUsageView,
} from "../query-types";

const LABELS: LabelUsageView[] = [
  { id: 1, name: "bug", tone: "red", usageCount: 3 },
  { id: 2, name: "docs", tone: "gray", usageCount: 0 },
  { id: 3, name: "feature", tone: "green", usageCount: 5 },
];

function query(over: Partial<BoardQuery> = {}): BoardQuery {
  return { ...EMPTY_BOARD_QUERY, ...over };
}

function renderBar(
  over: Partial<React.ComponentProps<typeof BoardFilterBar>> = {},
) {
  const onQueryChange = vi.fn();
  const view = render(
    <BoardFilterBar
      query={query()}
      labels={LABELS}
      ports={{ onQueryChange }}
      {...over}
    />,
  );
  return { onQueryChange, view };
}

afterEach(() => {
  vi.useRealTimers();
});

describe("生效条件的计数口径", () => {
  it("Given three labels and a keyword, When conditions are counted, Then it is two conditions, not four labels", () => {
    expect(
      activeConditionCount(query({ labelIds: [1, 2, 3], keyword: "x" })),
    ).toBe(2);
  });

  it("Given every condition at its default, When counted, Then nothing is in force", () => {
    expect(activeConditionCount(query())).toBe(0);
    // 「已完成保留多久」的默认档是 30 天，只有另外两档才算一条生效的条件。
    expect(activeConditionCount(query({ doneRetention: "all" }))).toBe(1);
  });
});

describe("chip 行", () => {
  it("Given several labels in one condition, When chips are built, Then each label is its own removable chip", () => {
    const q = query({ labelIds: [1, 3], keyword: "x", doneRetention: "all" });
    const chips = buildFilterChips(q);

    expect(chips.map((chip) => chip.key)).toEqual([
      "keyword",
      "label:1",
      "label:3",
      "doneRetention",
    ]);
    // 单独摘掉一枚标签，另一枚留在原地 —— 条件是一条，chip 不是一枚。
    expect(dropChip(q, chips[1]).labelIds).toEqual([3]);
  });

  it("Given a chip for each other condition, When it is dropped, Then that condition returns to its default", () => {
    const q = query({
      scope: { kind: "project", projectId: 7 },
      noLabelOnly: true,
      updated: { preset: "today" },
      created: { preset: "7d" },
      doneRetention: "all",
    });
    const byKey = Object.fromEntries(
      buildFilterChips(q).map((chip) => [chip.key, chip]),
    );

    expect(dropChip(q, byKey.scope).scope).toEqual({ kind: "all" });
    expect(dropChip(q, byKey.noLabel).noLabelOnly).toBe(false);
    expect(dropChip(q, byKey.updated).updated).toEqual({ preset: "any" });
    expect(dropChip(q, byKey.created).created).toEqual({ preset: "any" });
    expect(dropChip(q, byKey.doneRetention).doneRetention).toBe("30d");
  });

  it("Given more chips than fit, When the row renders, Then the row scrolls on its own instead of squeezing the search box", () => {
    renderBar({ query: query({ labelIds: [1, 2, 3] }) });

    const row = screen.getByTestId("filter-chips");
    expect(row.className).toContain("overflow-x-auto");
    expect(screen.getByTestId("board-search").className).toContain("shrink-0");
  });

  it("Given a rendered chip, When its remove button is used, Then only that condition leaves the query", async () => {
    const user = userEvent.setup();
    const { onQueryChange } = renderBar({
      query: query({ labelIds: [1, 3], updated: { preset: "today" } }),
    });

    await user.click(screen.getByTestId("filter-chip-remove-label:1"));

    expect(onQueryChange).toHaveBeenCalledWith(
      expect.objectContaining({
        labelIds: [3],
        updated: { preset: "today" },
      }),
    );
  });

  it("Given a time condition, When its chip renders, Then it carries a prefix saying which time it is about", () => {
    renderBar({
      query: query({ updated: { preset: "today" }, doneRetention: "all" }),
    });

    expect(screen.getByTestId("filter-chip-updated")).toHaveTextContent(
      "Updated Today",
    );
    expect(screen.getByTestId("filter-chip-doneRetention")).toHaveTextContent(
      "Done All",
    );
  });
});

describe("搜索框", () => {
  it("Given typing, When 200ms have not passed, Then no query goes out and the spinner stands in", async () => {
    // shouldAdvanceTime：userEvent 自己也等真实时钟，纯假时钟会把 type() 挂死。
    vi.useFakeTimers({ shouldAdvanceTime: true });
    const user = userEvent.setup({ advanceTimers: vi.advanceTimersByTime });
    const { onQueryChange } = renderBar();

    await user.type(screen.getByRole("searchbox"), "log");
    expect(onQueryChange).not.toHaveBeenCalled();
    expect(screen.getByTestId("board-search-spinner")).toBeInTheDocument();

    // act 包住定时器：假时钟下 React 的调度不会自己被推进，DOM 会停在旧那一帧。
    await act(async () => {
      await vi.advanceTimersByTimeAsync(BOARD_SEARCH_DEBOUNCE_MS);
    });

    expect(onQueryChange).toHaveBeenCalledTimes(1);
    expect(onQueryChange).toHaveBeenCalledWith(
      expect.objectContaining({ keyword: "log" }),
    );
    expect(screen.queryByTestId("board-search-spinner")).toBeNull();
  });

  it("Given a hit count, When the search box renders, Then it sits right next to the input", () => {
    renderBar({ matchedCount: 4 });

    expect(
      within(screen.getByTestId("board-search")).getByTestId(
        "board-search-count",
      ),
    ).toHaveTextContent("4");
  });
});

describe("筛选面板", () => {
  async function openPanel(over = {}) {
    const user = userEvent.setup();
    const handles = renderBar(over);
    await user.click(screen.getByTestId("filter-trigger"));
    return { user, ...handles };
  }

  it("Given conditions in force, When the 筛选 button renders, Then its number counts conditions rather than labels", () => {
    renderBar({ query: query({ labelIds: [1, 2, 3], keyword: "x" }) });

    expect(screen.getByTestId("filter-count")).toHaveTextContent("2");
  });

  it("Given the panel, When each of the five panel conditions is set, Then the query carries it out", async () => {
    const { user, onQueryChange } = await openPanel();

    await user.click(screen.getByTestId("filter-label-1"));
    expect(onQueryChange).toHaveBeenLastCalledWith(
      expect.objectContaining({ labelIds: [1] }),
    );

    await user.click(screen.getByTestId("filter-match-all"));
    expect(onQueryChange).toHaveBeenLastCalledWith(
      expect.objectContaining({ labelMatch: "all" }),
    );

    await user.click(screen.getByTestId("filter-no-label"));
    expect(onQueryChange).toHaveBeenLastCalledWith(
      expect.objectContaining({ noLabelOnly: true, labelIds: [] }),
    );

    await user.click(screen.getByTestId("filter-updated-today"));
    expect(onQueryChange).toHaveBeenLastCalledWith(
      expect.objectContaining({ updated: { preset: "today" } }),
    );

    await user.click(screen.getByTestId("filter-created-30d"));
    expect(onQueryChange).toHaveBeenLastCalledWith(
      expect.objectContaining({ created: { preset: "30d" } }),
    );

    await user.click(screen.getByTestId("filter-done-all"));
    expect(onQueryChange).toHaveBeenLastCalledWith(
      expect.objectContaining({ doneRetention: "all" }),
    );
  });

  it("Given the custom time preset, When both ends are given, Then the range travels with the query", async () => {
    const { user, onQueryChange } = await openPanel();

    await user.click(screen.getByTestId("filter-updated-custom"));
    expect(onQueryChange).toHaveBeenLastCalledWith(
      expect.objectContaining({ updated: { preset: "custom" } }),
    );
  });

  // 下界是那一天的零点，原样进查询。
  it("Given a custom lower bound, When it is picked, Then it lands at that day's midnight", async () => {
    const { onQueryChange } = await openPanel({
      query: query({ updated: { preset: "custom" } }),
    });

    fireEvent.change(screen.getByTestId("filter-updated-from"), {
      target: { value: "2026-08-01" },
    });

    expect(onQueryChange).toHaveBeenLastCalledWith(
      expect.objectContaining({
        updated: {
          preset: "custom",
          from: new Date("2026-08-01T00:00:00").getTime(),
        },
      }),
    );
  });

  // 区间是闭的（后端拼的是 `updatetime <= to`），所以上界照零点发出去的话，
  // 「8 月 1 日到 8 月 27 日」会把 27 日当天改过的每一张卡都排除在外——最常选的
  // 那一天（今天）会一张都看不见。
  it("Given a custom upper bound, When it is picked, Then it lands at the end of that day", async () => {
    const { onQueryChange } = await openPanel({
      query: query({
        updated: {
          preset: "custom",
          from: new Date("2026-08-01T00:00:00").getTime(),
        },
      }),
    });

    fireEvent.change(screen.getByTestId("filter-updated-to"), {
      target: { value: "2026-08-27" },
    });

    const to = onQueryChange.mock.calls.at(-1)?.[0].updated.to as number;
    expect(to).toBe(new Date("2026-08-28T00:00:00").getTime() - 1);
    expect(new Date(to).getDate()).toBe(27);
  });

  // 回填时上界要折回它自己那一天：直接按毫秒格式化的话，输入框会显示前一天。
  it("Given a stored upper bound, When the panel reopens, Then the date input shows that same day", async () => {
    await openPanel({
      query: query({
        updated: {
          preset: "custom",
          to: new Date("2026-08-28T00:00:00").getTime() - 1,
        },
      }),
    });

    expect(screen.getByTestId("filter-updated-to")).toHaveValue("2026-08-27");
  });

  it("Given label management, When it is opened from the panel, Then the host is told", async () => {
    const onManageLabels = vi.fn();
    const { user } = await openPanel({ onManageLabels });

    await user.click(screen.getByTestId("filter-manage-labels"));

    expect(onManageLabels).toHaveBeenCalled();
  });
});

describe("窄到最小窗口宽度", () => {
  it("Given the minimum window width, When the filter button renders, Then it is icon-only and still says its name", () => {
    render(
      <BoardFilterBar
        query={EMPTY_BOARD_QUERY}
        labels={[]}
        projects={[]}
        ports={{ onQueryChange: vi.fn() }}
      />,
    );

    const trigger = screen.getByTestId("filter-trigger");
    // 860px 是最小窗口宽度：那点横向空间留给搜索框与 chip 行。
    expect(within(trigger).getByText("Filter").className).toContain(
      "max-[860px]:hidden",
    );
    // 文本被 `display:none` 拿掉后名字得另有出处。
    expect(trigger).toHaveAttribute("aria-label", "Filter");
  });
});
