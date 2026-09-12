import { describe, expect, it } from "vitest";

import { buildMachineRoster, LOCAL_DEVICE_ID } from "./machine-roster";

const local = "本机";

describe("buildMachineRoster", () => {
  it("本机恒在，且排最前 —— 绝大多数会话都落在它上面", () => {
    const roster = buildMachineRoster([], local);

    expect(roster).toEqual([
      { deviceId: LOCAL_DEVICE_ID, name: local, online: true },
    ]);
  });

  it("一台都没配对时机器轴仍然成立：只有本机一组", () => {
    expect(buildMachineRoster(undefined, local)).toHaveLength(1);
  });

  it("配对的 daemon 跟在本机之后，在线的排前面，同段内按名字", () => {
    const roster = buildMachineRoster(
      [
        { id: 3, name: "zeta", online: false },
        { id: 1, name: "beta", online: true },
        { id: 2, name: "alpha", online: false },
        { id: 4, name: "gamma", online: true },
      ],
      local,
    );

    expect(roster.map((m) => m.name)).toEqual([
      local,
      "beta",
      "gamma",
      "alpha",
      "zeta",
    ]);
  });

  it("本机用 deviceId 0 —— 与 chat_entity.Session.ExecDeviceID 的约定同源", () => {
    // 0 不是「没有机器」：机器轴按 exec_device_id 取数，本机那一组发的就是 0。
    expect(LOCAL_DEVICE_ID).toBe(0);
  });

  it("没有名字的机器不占用别人的名字，如实留空", () => {
    const roster = buildMachineRoster(
      [{ id: 5, name: "", online: true }],
      local,
    );

    expect(roster[1]).toEqual({ deviceId: 5, name: "", online: true });
  });
});
