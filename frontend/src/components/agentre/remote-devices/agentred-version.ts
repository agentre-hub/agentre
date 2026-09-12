// frontend/src/components/agentre/remote-devices/agentred-version.ts
//
// 「这台 agentred 跑的是哪一版，要不要劝它升级」这件事的全部判定。
// 呈现层只读这里的结论，不自己比版本号。

/** 一个可比较的发布版本号解析结果。 */
type ParsedVersion = {
  parts: [number, number, number];
  /** 预发布后缀（`0.6.0-beta.1` 的 `beta.1`）；空串表示正式发布。 */
  prerelease: string;
};

// 预发布段与构建元数据分开捕获：按 semver，只有前者参与排序，后者（`+` 之后那一段）
// 不参与。两者都塞进「预发布」的话，一台跑 0.6.0+abc 的机器会被永久判成旧于 0.6.0，
// 一直劝升，而点下去的每一次一键升级都只会拿回 ALREADY_LATEST。
const RELEASE_VERSION_RE = /^v?(\d+)\.(\d+)\.(\d+)(?:-([^+]+))?(?:\+[^+]*)?$/;

function parseVersion(v: string): ParsedVersion | null {
  const m = RELEASE_VERSION_RE.exec(v.trim());
  if (!m) return null;
  return {
    parts: [Number(m[1]), Number(m[2]), Number(m[3])],
    prerelease: m[4] ?? "",
  };
}

/**
 * 比较两个发布版本号：a 旧于 b 返回负数，相等返回 0，新于返回正数。
 *
 * 任一侧不是可比较的发布版本号（`dev`、`nightly-…`、空串）时返回 null ——
 * 不可比就是不可比，硬排会把一台机器判成过期然后一直劝升。
 */
export function compareVersions(a: string, b: string): number | null {
  const pa = parseVersion(a);
  const pb = parseVersion(b);
  if (!pa || !pb) return null;
  for (let i = 0; i < 3; i++) {
    if (pa.parts[i] !== pb.parts[i]) return pa.parts[i] - pb.parts[i];
  }
  return comparePrerelease(pa.prerelease, pb.prerelease);
}

/**
 * 按 semver 比较两个预发布段（空串 = 正式版）。
 *
 * 逐节（`.` 分隔）比，纯数字节按**数值**比 —— 字面比法会把 `beta.10` 排在 `beta.2`
 * 之前，于是一台跑 beta.2 的机器在 beta.10 已经发布之后被判成「已是最新」。
 */
function comparePrerelease(a: string, b: string): number {
  if (a === b) return 0;
  // 预发布排在同号正式版之前：0.6.0-beta.1 旧于 0.6.0。
  if (a === "") return 1;
  if (b === "") return -1;
  const left = a.split(".");
  const right = b.split(".");
  for (let i = 0; i < Math.max(left.length, right.length); i++) {
    const x = left[i];
    const y = right[i];
    // 前缀相同时，标识符少的那个更旧（0.6.0-beta 旧于 0.6.0-beta.1）。
    if (x === undefined) return -1;
    if (y === undefined) return 1;
    if (x === y) continue;
    const xNum = /^\d+$/.test(x);
    const yNum = /^\d+$/.test(y);
    if (xNum && yNum) return Number(x) - Number(y);
    // 纯数字节低于带字母的节（semver 的既定顺序）。
    if (xNum !== yNum) return xNum ? -1 : 1;
    return x < y ? -1 : 1;
  }
  return 0;
}

/** 桌面端自身的构建标识（`app.AppInfo` 的 version / commit）。 */
export type DesktopBuild = { version: string; commit: string };

/**
 * 「桌面端已知的最新发布版本」。agentred 与桌面端同一条发布流，所以这里不另开一条
 * 发布信息来源：
 *
 * - `checked` 是更新检查（update_svc）查到的版本号，有就以它为准；
 * - 没有（还没查 / 已是最新时结果不再回传 / 查失败）时，桌面端自己跑着的那个发布
 *   版本是「确实存在且可能更新」的证据，拿它劝升说的仍是真话；
 * - 桌面端自己不是发布构建（短 commit 为空）时它的版本号不可比（与决策 5 同一道闸），
 *   此时若也没有检查结果就如实回空串 —— 拿不到就是拿不到，不编（决策 19）。
 */
export function latestKnownVersion(
  checked: string,
  desktop: DesktopBuild,
): string {
  const own = desktop.commit ? desktop.version : "";
  if (!parseVersion(checked)) return parseVersion(own) ? own : "";
  if (!parseVersion(own)) return checked;
  return (compareVersions(own, checked) ?? 0) > 0 ? own : checked;
}

/**
 * 一台远端 agentred 在版本这件事上的状态。九个界面状态里属于本轮的那几个：
 * 已是最新 / 可升级 / 开发构建 / 协议不匹配 / 拿不到最新版信息。
 */
export type AgentredVersionState =
  /** 还没读到版本（没连上过 / 旧 daemon 不自报）：什么都不说。 */
  | { kind: "unknown" }
  /** 短 commit 为空 = 非发布构建：如实显示，永不劝升（决策 5）。 */
  | { kind: "dev-build" }
  /** 已是最新。 */
  | { kind: "current"; version: string }
  /** 拿不到最新版信息：显示版本，不下判断（决策 19，与「已是最新」分得开）。 */
  | { kind: "latest-unknown"; version: string }
  /** 发布构建且旧于最新版：弱徽标 + 升级出口。 */
  | { kind: "upgradable"; version: string; latest: string }
  /** 握手被拒于协议版本：强提示，出口只有可复制的命令。 */
  | { kind: "protocol-mismatch"; version: string };

export type AgentredVersionInput = {
  /** daemon 自报的版本号（health.ping）。 */
  version: string;
  /** daemon 自报的短 commit；空串 = 非发布构建。 */
  commit: string;
  /** 配对行上的 lastError，用来认出握手被拒。 */
  lastError: string;
  /** 桌面端已知的最新发布版本；空串 = 不知道。 */
  latest: string;
};

/**
 * 握手被拒于协议这一层的判据，与 remote_device_svc / remote_device_watcher_svc 落的
 * lastError 取值一致。
 *
 * 两个取值都要认：`protocol_mismatch` 是「版本对不上」，`protocol_unsupported` 是
 * 「这台 daemon 老到根本不认这套 protobuf 协议」——后者更旧，而处置完全一样（只能
 * 重装那台 agentred）。只认前一个的话，最该拿到命令卡的那些机器一张都拿不到，反而会
 * 被当成普通的可升级机器劝去点一个握手都过不去的按钮。
 */
const PROTOCOL_REFUSALS = ["protocol_mismatch", "protocol_unsupported"];

/** 这条 lastError 是不是「握手在协议这一层就被拒了」。 */
export function isProtocolRefusal(lastError: string): boolean {
  return PROTOCOL_REFUSALS.includes(lastError);
}

export function agentredVersionState(
  input: AgentredVersionInput,
): AgentredVersionState {
  // 强提示优先：这台机器已经用不了了，比「有没有新版本」更要紧，而且此时多半
  // 连版本号都没读到（握手都没过）。
  if (isProtocolRefusal(input.lastError)) {
    return { kind: "protocol-mismatch", version: input.version };
  }
  if (!input.version) return { kind: "unknown" };
  if (!input.commit) return { kind: "dev-build" };
  if (!input.latest) return { kind: "latest-unknown", version: input.version };
  const order = compareVersions(input.version, input.latest);
  if (order === null) return { kind: "latest-unknown", version: input.version };
  return order < 0
    ? { kind: "upgradable", version: input.version, latest: input.latest }
    : { kind: "current", version: input.version };
}
