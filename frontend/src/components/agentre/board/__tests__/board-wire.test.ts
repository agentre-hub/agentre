import { describe, expect, it } from "vitest";

import {
  ALL_PROJECTS_SCOPE,
  EMPTY_BOARD_QUERY,
  type BoardQuery,
} from "@agentre-hub/agentre-ui";

import {
  isFiltering,
  matchedTotal,
  projectCountOf,
  scopeShowsGlyphs,
  toBoardColumns,
  toIssueListRequest,
  toTaskFormValue,
} from "../board-wire";

const NOW = Date.UTC(2026, 7, 28, 9, 30, 0);

function query(patch: Partial<BoardQuery> = {}): BoardQuery {
  return { ...EMPTY_BOARD_QUERY, ...patch };
}

describe("toIssueListRequest", () => {
  it("Given the empty query, When it is translated, Then it asks for every project sorted by column position", () => {
    expect(toIssueListRequest(query(), NOW)).toEqual({
      scope: "all",
      projectID: 0,
      keyword: "",
      labelIDs: [],
      labelMatchAll: false,
      noLabel: false,
      updatedFrom: 0,
      updatedTo: 0,
      createdFrom: 0,
      createdTo: 0,
      doneWithinDays: 30,
      sort: "position",
    });
  });

  it("Given a project scope, When it is translated, Then the project id rides along with the scope name", () => {
    const request = toIssueListRequest(
      query({ scope: { kind: "project", projectId: 7 } }),
      NOW,
    );

    expect(request.scope).toBe("project");
    expect(request.projectID).toBe(7);
  });

  it("Given the unassigned scope, When it is translated, Then no project id is sent", () => {
    const request = toIssueListRequest(
      query({ scope: { kind: "unassigned" } }),
      NOW,
    );

    expect(request.scope).toBe("unassigned");
    expect(request.projectID).toBe(0);
  });

  it("Given label match-all with the no-label switch, When it is translated, Then both flags travel", () => {
    const request = toIssueListRequest(
      query({ labelIds: [2, 5], labelMatch: "all", noLabelOnly: true }),
      NOW,
    );

    expect(request.labelIDs).toEqual([2, 5]);
    expect(request.labelMatchAll).toBe(true);
    expect(request.noLabel).toBe(true);
  });

  it("Given the 7-day preset, When it is translated, Then the window opens seven days before now and stays open-ended", () => {
    const request = toIssueListRequest(
      query({ updated: { preset: "7d" } }),
      NOW,
    );

    expect(request.updatedFrom).toBe(NOW - 7 * 86_400_000);
    expect(request.updatedTo).toBe(0);
  });

  it("Given the today preset, When it is translated, Then the window starts at local midnight", () => {
    const request = toIssueListRequest(
      query({ created: { preset: "today" } }),
      NOW,
    );

    const midnight = new Date(NOW);
    midnight.setHours(0, 0, 0, 0);
    expect(request.createdFrom).toBe(midnight.getTime());
  });

  it("Given a custom range with only an end, When it is translated, Then the missing end stays unbounded", () => {
    const request = toIssueListRequest(
      query({ created: { preset: "custom", to: 1_700_000_000_000 } }),
      NOW,
    );

    expect(request.createdFrom).toBe(0);
    expect(request.createdTo).toBe(1_700_000_000_000);
  });

  it("Given each done retention, When it is translated, Then all means no day window at all", () => {
    expect(
      toIssueListRequest(query({ doneRetention: "90d" }), NOW).doneWithinDays,
    ).toBe(90);
    expect(
      toIssueListRequest(query({ doneRetention: "all" }), NOW).doneWithinDays,
    ).toBe(0);
  });
});

describe("isFiltering", () => {
  it("Given only a project scope, When the board asks whether it is filtering, Then it is not — the totals are already scoped", () => {
    expect(
      isFiltering(query({ scope: { kind: "project", projectId: 3 } })),
    ).toBe(false);
  });

  it("Given a keyword, When the board asks whether it is filtering, Then it is", () => {
    expect(isFiltering(query({ keyword: "oauth" }))).toBe(true);
  });

  it("Given a non-default done retention, When the board asks whether it is filtering, Then it is", () => {
    expect(isFiltering(query({ doneRetention: "all" }))).toBe(true);
  });

  it("Given the empty query, When the board asks whether it is filtering, Then it is not", () => {
    expect(isFiltering(query({ scope: ALL_PROJECTS_SCOPE }))).toBe(false);
  });
});

describe("toBoardColumns", () => {
  const issues = [
    {
      id: 3,
      projectID: 0,
      title: "later",
      body: "",
      stage: "todo",
      position: 20,
      updatetime: 100,
      labels: [],
    },
    {
      id: 1,
      projectID: 4,
      title: "first",
      body: "why",
      stage: "todo",
      position: 10,
      updatetime: 200,
      labels: [{ id: 9, name: "bug", tone: "red", usageCount: 3 }],
    },
    {
      id: 7,
      projectID: 4,
      title: "shipped",
      body: "",
      stage: "done",
      position: 5,
      updatetime: 300,
      labels: [],
    },
  ] as never[];

  it("Given issues out of order, When columns are built, Then each column is sorted by position and the missing ones are empty", () => {
    const columns = toBoardColumns(
      { issues, stageCounts: {}, stageTotals: {} } as never,
      () => null,
    );

    expect(columns.todo?.cards.map((card) => card.id)).toEqual([1, 3]);
    expect(columns.done?.cards.map((card) => card.id)).toEqual([7]);
    expect(columns.doing?.cards).toEqual([]);
    expect(columns.review?.cards).toEqual([]);
  });

  it("Given stage totals and counts, When columns are built, Then the header denominator does not shrink with the filter", () => {
    const columns = toBoardColumns(
      {
        issues,
        stageCounts: { todo: 2, done: 1 },
        stageTotals: { todo: 9, done: 4 },
      } as never,
      () => null,
    );

    expect(columns.todo?.total).toBe(9);
    expect(columns.todo?.matched).toBe(2);
  });

  it("Given a card body, When columns are built, Then the description marker rides on the card and the project resolver decides the glyph", () => {
    const columns = toBoardColumns(
      { issues, stageCounts: {}, stageTotals: {} } as never,
      (projectID) =>
        projectID === 4 ? { name: "Agentre", color: "agent-1" } : null,
    );

    const [first] = columns.todo?.cards ?? [];
    expect(first.hasDescription).toBe(true);
    expect(first.project?.name).toBe("Agentre");
    expect(first.labels).toEqual([{ id: 9, name: "bug", tone: "red" }]);
    expect(columns.todo?.cards[1].project).toBeNull();
  });
});

describe("matchedTotal and projectCountOf", () => {
  it("Given stage counts, When the search box asks for a hit count, Then every column is added up", () => {
    expect(matchedTotal({ todo: 2, doing: 0, review: 1, done: 4 })).toBe(7);
  });

  it("Given the per-project counts, When one is looked up, Then a project with no row counts as zero", () => {
    const counts = [
      { projectID: 0, count: 2 },
      { projectID: 5, count: 8 },
    ] as never[];

    expect(projectCountOf(counts, 5)).toBe(8);
    expect(projectCountOf(counts, 0)).toBe(2);
    expect(projectCountOf(counts, 99)).toBe(0);
  });
});

describe("toTaskFormValue", () => {
  it("Given an issue, When the form opens on it, Then every field it can edit comes back — execution included", () => {
    const value = toTaskFormValue({
      id: 12,
      projectID: 4,
      title: "fix OAuth",
      body: "the callback drops it",
      stage: "review",
      updatetime: 900,
      labels: [{ id: 1 }, { id: 2 }],
      assigneeAgentID: 7,
      agentBackendID: 9,
      llmProviderKey: "prov",
      llmModelKey: "model",
    } as never);

    expect(value).toEqual({
      id: 12,
      title: "fix OAuth",
      description: "the callback drops it",
      stage: "review",
      projectId: 4,
      labelIds: [1, 2],
      assigneeAgentId: 7,
      agentBackendId: 9,
      llmProviderKey: "prov",
      llmModelKey: "model",
      updatedAt: 900,
    });
  });

  it("Given an unassigned issue with no execution plan, When the form opens on it, Then the empty foreign keys read as null", () => {
    const value = toTaskFormValue({
      id: 3,
      projectID: 0,
      title: "t",
      body: "",
      stage: "todo",
      updatetime: 0,
      labels: [],
      assigneeAgentID: 0,
      agentBackendID: 0,
      llmProviderKey: "",
      llmModelKey: "",
    } as never);

    expect(value.projectId).toBeNull();
    expect(value.assigneeAgentId).toBeNull();
    expect(value.agentBackendId).toBeNull();
  });
});

describe("scopeShowsGlyphs", () => {
  const flat = [
    { id: 1, depth: 0 },
    { id: 2, depth: 1 },
    { id: 3, depth: 0 },
  ];

  it("Given the all-projects scope, When cards render, Then the project glyph is shown", () => {
    expect(scopeShowsGlyphs({ kind: "all" }, flat)).toBe(true);
  });

  it("Given the unassigned scope, When cards render, Then there is no project to show", () => {
    expect(scopeShowsGlyphs({ kind: "unassigned" }, flat)).toBe(false);
  });

  it("Given a parent project, When cards render, Then its children make the range more than one project", () => {
    expect(scopeShowsGlyphs({ kind: "project", projectId: 1 }, flat)).toBe(
      true,
    );
  });

  it("Given a leaf project, When cards render, Then the glyph says nothing new", () => {
    expect(scopeShowsGlyphs({ kind: "project", projectId: 3 }, flat)).toBe(
      false,
    );
  });
});
