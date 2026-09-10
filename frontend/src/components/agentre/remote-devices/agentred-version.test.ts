import { describe, expect, it } from "vitest";

import {
  agentredVersionState,
  compareVersions,
  latestKnownVersion,
} from "./agentred-version";

describe("compareVersions", () => {
  it("orders release versions numerically, not lexically", () => {
    expect(compareVersions("0.5.2", "0.10.0")).toBeLessThan(0);
    expect(compareVersions("1.0.0", "0.9.9")).toBeGreaterThan(0);
    expect(compareVersions("v0.6.0", "0.6.0")).toBe(0);
  });

  it("puts a pre-release before the release it leads to", () => {
    expect(compareVersions("0.6.0-beta.1", "0.6.0")).toBeLessThan(0);
    expect(compareVersions("0.6.0", "0.6.0-beta.1")).toBeGreaterThan(0);
  });

  // 预发布段按 `.` 分节比，数字节按数值比：字面比法会把 beta.10 排在 beta.2 之前，
  // 于是一台跑 beta.2 的机器在 beta.10 已经发布之后被判成「已是最新」——正是决策 19
  // 不许发生的那种「借一个结论冒充另一个」。
  it("orders pre-release counters numerically", () => {
    expect(compareVersions("0.6.0-beta.2", "0.6.0-beta.10")).toBeLessThan(0);
    expect(compareVersions("0.6.0-beta.10", "0.6.0-beta.2")).toBeGreaterThan(0);
    // 标识符少的更旧（semver：前缀相同时短的排前面）。
    expect(compareVersions("0.6.0-beta", "0.6.0-beta.1")).toBeLessThan(0);
  });

  // 构建元数据（`+` 之后那一段）按 semver 不参与排序。把它当成预发布段的话，
  // 一台跑 0.6.0+abc123 的机器会被永久判成旧于 0.6.0，一直劝升，而点下去的每一次
  // 一键升级都只会拿回 ALREADY_LATEST。
  it("ignores build metadata", () => {
    expect(compareVersions("0.6.0+abc123", "0.6.0")).toBe(0);
    expect(compareVersions("0.6.0-beta.1+abc", "0.6.0-beta.1")).toBe(0);
    expect(compareVersions("0.6.0+abc", "0.6.1")).toBeLessThan(0);
  });

  // 不可比就是不可比：nightly 串、空串、"dev" 都不能拿去和正式版排序，
  // 硬排会把一台机器判成过期然后一直劝升。
  it("returns null for anything that is not a comparable version", () => {
    expect(compareVersions("dev", "0.6.0")).toBeNull();
    expect(compareVersions("nightly-20260101-abc", "0.6.0")).toBeNull();
    expect(compareVersions("", "0.6.0")).toBeNull();
  });
});

describe("agentredVersionState", () => {
  const release = { version: "0.5.2", commit: "a1b2c3d", lastError: "" };

  it("nudges a release build that is older than the latest known version", () => {
    expect(agentredVersionState({ ...release, latest: "0.6.0" })).toEqual({
      kind: "upgradable",
      version: "0.5.2",
      latest: "0.6.0",
    });
  });

  it("says nothing about a release build that is already at the latest", () => {
    expect(agentredVersionState({ ...release, latest: "0.5.2" })).toEqual({
      kind: "current",
      version: "0.5.2",
    });
  });

  // 决策 19：拿不到最新版信息与「已是最新」必须分得开，两者都不出徽标，
  // 但前者不许借「没有徽标」冒充已是最新。
  it("keeps 'latest unknown' apart from 'already current'", () => {
    expect(agentredVersionState({ ...release, latest: "" })).toEqual({
      kind: "latest-unknown",
      version: "0.5.2",
    });
  });

  // 决策 5：未注入版本的构建自称 1.0.0，比任何 0.x 正式版都「新」——
  // 短 commit 为空是唯一判据，缺了它就会把本地构建判成最新、把正式版判成过期。
  it("treats an empty short commit as a development build and never nudges it", () => {
    expect(
      agentredVersionState({
        version: "1.0.0",
        commit: "",
        lastError: "",
        latest: "0.6.0",
      }),
    ).toEqual({ kind: "dev-build" });
  });

  it("says nothing at all before the daemon has reported a version", () => {
    expect(
      agentredVersionState({
        version: "",
        commit: "",
        lastError: "",
        latest: "0.6.0",
      }),
    ).toEqual({ kind: "unknown" });
  });

  // 握手被拒是强提示：一键升级够不着（握手都没过），出口只能是可复制的命令。
  it("turns a protocol-version rejection into the strong state", () => {
    expect(
      agentredVersionState({
        ...release,
        lastError: "protocol_mismatch",
        latest: "0.6.0",
      }),
    ).toEqual({ kind: "protocol-mismatch", version: "0.5.2" });
  });

  it("still reports the strong state when no version was ever read", () => {
    expect(
      agentredVersionState({
        version: "",
        commit: "",
        lastError: "protocol_mismatch",
        latest: "",
      }),
    ).toEqual({ kind: "protocol-mismatch", version: "" });
  });

  // remote_device_svc 落的另一个协议层拒绝：这台 daemon 老到根本不认这套 protobuf
  // 协议（refresh.go / watcher.go 的 protocol_unsupported）。它比版本对不上更旧，
  // 处置完全一样（只能重装那台 agentred），因此必须落进同一个强提示 —— 否则最该
  // 拿到命令卡的那些机器恰恰一张都拿不到。
  it("treats a daemon that speaks no protobuf protocol as the same strong state", () => {
    expect(
      agentredVersionState({
        ...release,
        lastError: "protocol_unsupported",
        latest: "0.6.0",
      }),
    ).toEqual({ kind: "protocol-mismatch", version: "0.5.2" });
  });

  it("leaves ordinary connection errors alone", () => {
    expect(
      agentredVersionState({
        ...release,
        lastError: "dial_failed:boom",
        latest: "",
      }),
    ).toEqual({ kind: "latest-unknown", version: "0.5.2" });
  });
});

describe("latestKnownVersion", () => {
  it("takes the version the update check found", () => {
    expect(
      latestKnownVersion("0.6.0", { version: "0.5.2", commit: "a1b2c3d" }),
    ).toBe("0.6.0");
  });

  // 检查说「已是最新」时不再回传版本号，但桌面端自己就跑在一个已发布的版本上：
  // 拿它劝升说的仍然是真话，不是编出来的版本。
  it("falls back to the desktop's own release version", () => {
    expect(
      latestKnownVersion("", { version: "0.6.0", commit: "9f8e7d6" }),
    ).toBe("0.6.0");
  });

  it("keeps whichever of the two is newer", () => {
    expect(
      latestKnownVersion("0.6.0", { version: "0.7.0", commit: "9f8e7d6" }),
    ).toBe("0.7.0");
  });

  // 桌面端自己不是发布构建时它的版本号不可比（决策 5 的同一道闸）：
  // 没有检查结果就如实回空串，界面据此什么都不说（决策 19）。
  it("reports nothing when neither source is usable", () => {
    expect(latestKnownVersion("", { version: "dev", commit: "" })).toBe("");
    expect(latestKnownVersion("", { version: "1.0.0", commit: "" })).toBe("");
  });
});
