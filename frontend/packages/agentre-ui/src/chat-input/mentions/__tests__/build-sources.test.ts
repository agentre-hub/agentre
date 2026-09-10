import { describe, expect, it } from "vitest";

import { buildMentionSources } from "../build-sources";

describe("buildMentionSources", () => {
  it("maps agents and projects into mention items", () => {
    const out = buildMentionSources(
      [{ id: 12, name: "Reviewer", avatarColor: "agent-3" }],
      [{ id: 3, name: "Web", path: "/w", color: "agent-5", depth: 2 }],
      [],
    );
    expect(out).toEqual({
      devices: [],
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

  it("maps devices onto their fingerprint, with no numeric id", () => {
    const out = buildMentionSources(
      [],
      [],
      [{ fp: "sha256:ab12", name: "工作站", online: true }],
    );
    expect(out.devices).toEqual([
      {
        kind: "device",
        refId: 0,
        label: "工作站",
        fp: "sha256:ab12",
        online: true,
      },
    ]);
  });

  // 离线设备照样可 @(它是合法的讨论对象),所以 online 必须如实带出来而不是被过滤掉。
  it("keeps an offline device and reports it as offline", () => {
    const out = buildMentionSources([], [], [{ fp: "sha256:cd", name: "NAS" }]);
    expect(out.devices).toEqual([
      {
        kind: "device",
        refId: 0,
        label: "NAS",
        fp: "sha256:cd",
        online: false,
      },
    ]);
  });

  it("tolerates missing color/path", () => {
    const out = buildMentionSources(
      [{ id: 1, name: "A" }],
      [{ id: 2, name: "B" }],
      [],
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
