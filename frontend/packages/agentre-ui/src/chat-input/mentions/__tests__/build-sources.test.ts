import { describe, expect, it } from "vitest";

import { buildMentionSources } from "../build-sources";

describe("buildMentionSources", () => {
  it("maps agents and projects into mention items", () => {
    const out = buildMentionSources(
      [{ id: 12, name: "Reviewer", avatarColor: "agent-3" }],
      [{ id: 3, name: "Web", path: "/w", color: "agent-5", depth: 2 }],
    );
    expect(out).toEqual({
      agents: [
        { kind: "agent", refId: 12, label: "Reviewer", color: "agent-3" },
      ],
      projects: [
        {
          kind: "project",
          refId: 3,
          label: "Web",
          path: "/w",
          color: "agent-5",
          depth: 2,
        },
      ],
    });
  });

  it("tolerates missing color/path", () => {
    const out = buildMentionSources(
      [{ id: 1, name: "A" }],
      [{ id: 2, name: "B" }],
    );
    expect(out.agents[0]).toMatchObject({
      kind: "agent",
      refId: 1,
      label: "A",
    });
    expect(out.projects[0]).toMatchObject({
      kind: "project",
      refId: 2,
      label: "B",
      path: "",
      depth: 0,
    });
  });
});
