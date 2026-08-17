import { describe, expect, it } from "vitest";

import { rankMentionItems } from "../rank-items";
import type { MentionSources } from "../types";

const chineseSources: MentionSources = {
  agents: [
    { kind: "agent", refId: 1, label: "年度回顾" },
    { kind: "agent", refId: 2, label: "年度助手" },
  ],
  projects: [],
};

describe("rankMentionItems", () => {
  it("Given Chinese agent names, when queried by full pinyin or initials, then it returns the matching agent", () => {
    expect(rankMentionItems(chineseSources, "nianduzhu")).toEqual([
      chineseSources.agents[1],
    ]);
    expect(rankMentionItems(chineseSources, "ndzs")).toEqual([
      chineseSources.agents[1],
    ]);
  });

  it("Given project paths and an agent path-shaped value, when queried by path, then only direct project subtitle containment matches", () => {
    const sources: MentionSources = {
      agents: [
        {
          kind: "agent",
          refId: 1,
          label: "Builder",
          path: "/workspace/frontend",
        },
      ],
      projects: [
        {
          kind: "project",
          refId: 2,
          label: "Desktop",
          path: "/workspace/frontend",
        },
        {
          kind: "project",
          refId: 3,
          label: "Console",
          path: "/nian/du",
        },
        {
          kind: "project",
          refId: 4,
          label: "Terminal",
          path: "/项目/年度",
        },
      ],
    };

    expect(rankMentionItems(sources, "frontend")).toEqual([
      sources.projects[0],
    ]);
    expect(rankMentionItems(sources, "ndu")).toEqual([]);
    expect(rankMentionItems(sources, "niandu")).toEqual([]);
  });

  it("Given both groups, when relevance differs and scores tie, then Agents stay before Projects and each group is stably ranked", () => {
    const sources: MentionSources = {
      agents: [
        { kind: "agent", refId: 1, label: "Beta Agent" },
        { kind: "agent", refId: 2, label: "Agent Exact" },
        { kind: "agent", refId: 3, label: "Gamma Agent" },
      ],
      projects: [
        { kind: "project", refId: 4, label: "Agent" },
        {
          kind: "project",
          refId: 5,
          label: "Workspace",
          path: "/agent",
        },
        { kind: "project", refId: 6, label: "Agent" },
      ],
    };

    expect(
      rankMentionItems(sources, "agent").map((item) => item.refId),
    ).toEqual([2, 1, 3, 4, 6, 5]);
  });

  it("Given an empty query or a group with no matches, then source order is stable and empty groups disappear", () => {
    const sources: MentionSources = {
      agents: [
        { kind: "agent", refId: 1, label: "Reviewer" },
        { kind: "agent", refId: 2, label: "Builder" },
      ],
      projects: [
        { kind: "project", refId: 3, label: "Web", path: "/workspace/web" },
        { kind: "project", refId: 4, label: "API" },
      ],
    };

    expect(rankMentionItems(sources, " ").map((item) => item.refId)).toEqual([
      1, 2, 3, 4,
    ]);
    expect(rankMentionItems(sources, "workspace")).toEqual([
      sources.projects[0],
    ]);
    expect(rankMentionItems(sources, "api")).toEqual([sources.projects[1]]);
    expect(rankMentionItems({ agents: [], projects: [] }, "anything")).toEqual(
      [],
    );
  });
});
