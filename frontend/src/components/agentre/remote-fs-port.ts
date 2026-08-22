/**
 * 桌面端这一侧的 `ProjectFsPort`（规格 2026-08-22 D 段，决策 7 / 11）。
 *
 * 包里的目录选择器不认识 wails，也不认识中继——它只认 `ProjectFsPort`。这份 adapter
 * 就是把 wails 那两个绑定裹成那个契约，宿主专属的东西一件都不往包里漏。
 *
 * **错误分类靠码，不靠文案。** Go 那侧一直分好了类（`code.RemoteFsPermDenied` /
 * `RemoteFsNotFound` / …），只是那个码过不了 wails 那座桥 —— cago 的
 * `httputils.Error.Error()` 只返回 `Msg`。现在 `internal/app/coded_error.go` 把它写
 * 成 `agentre-code:<码> <原文>`，这一侧照码分类。
 *
 * 反过来按错误文案去猜分类是错的做法：文案一改就静默失灵，而且中英两套要各猜一遍。
 * 这个前缀是**契约**，两端各有测试钉住它，改它会当场变红。
 */
import type {
  DirectoryFailure,
  DirectoryFailureKind,
  ListDirOutcome,
  MkdirOutcome,
  ProjectFsPort,
} from "@agentre-ai/agentre-ui";

import { RemoteFsListDir, RemoteFsMkdir } from "../../../wailsjs/go/app/App";

// 与 wailsjs codegen 类型一致；codegen 跑后可改回 import remote_fs_svc.ListDirView。
type EntryView = {
  name: string;
  isDir: boolean;
  size: number;
  mtime: number;
  symlink?: boolean;
};

type ListDirView = {
  path: string;
  entries: EntryView[];
  truncated: boolean;
};

/** `internal/app/coded_error.go` 那一侧的同一条契约。 */
const CODED_PREFIX = /^agentre-code:(\d+)\s?([\s\S]*)$/;

/**
 * 业务码 → 选择器认识的那一档。数值出处是 `internal/pkg/code/code.go` 的
 * `20600~` 那一段（**稳定 wire 值**，与 agentre-server 读 JSON-RPC 码是同一条路子）。
 */
const KIND_BY_CODE: Record<number, DirectoryFailureKind> = {
  20600: "refused", // RemoteFsPathRefused
  20601: "denied", // RemoteFsPermDenied
  20602: "notFound", // RemoteFsNotFound
  20603: "notDir", // RemoteFsNotDir
  20604: "disconnected", // RemoteFsDeviceOffline
  20605: "exists", // RemoteFsMkdirExists
  20606: "invalidName", // RemoteFsMkdirInvalidName
};

/**
 * 把一次失败翻成可分辨的一类。
 *
 * 认得出码就用码；认不出（没有前缀、或码还没映射）一律 `unknown` 并把原文带上 ——
 * 编一个类比说「不知道」更糟。前缀本身要剥掉：`agentre-code` 是内部记号，不该出现
 * 在用户读到的那句话里。
 */
function toFailure(err: unknown): DirectoryFailure {
  const raw = err instanceof Error ? err.message : String(err);
  const m = CODED_PREFIX.exec(raw);
  if (!m) return { kind: "unknown", message: raw };
  const message = m[2];
  return { kind: KIND_BY_CODE[Number(m[1])] ?? "unknown", message };
}

/**
 * `.git` 这一条既是「**当前这个目录**是 git 仓库」的判据，又不该自己出现在列表里——
 * 与 agentre-server 那一侧同一条判定。
 *
 * 判的是当前目录而不是逐个子目录：子目录里有什么，这一次请求根本没读。
 */
function toResult(view: ListDirView) {
  const raw = view.entries ?? [];
  return {
    path: view.path,
    truncated: !!view.truncated,
    isGitRepo: raw.some((e) => e.isDir && e.name === ".git"),
    entries: raw
      .filter((e) => e.name !== ".git")
      .map((e) => ({ name: e.name, isDir: e.isDir, symlink: e.symlink })),
  };
}

export function createRemoteFsPort(): ProjectFsPort {
  return {
    async listDir(machineId, path): Promise<ListDirOutcome> {
      try {
        const view = (await RemoteFsListDir(machineId, path)) as ListDirView;
        return { ok: true, result: toResult(view) };
      } catch (err) {
        return { ok: false, failure: toFailure(err) };
      }
    },

    async mkdir(machineId, parent, name): Promise<MkdirOutcome> {
      try {
        await RemoteFsMkdir(machineId, parent, name);
        return { ok: true, result: undefined };
      } catch (err) {
        return { ok: false, failure: toFailure(err) };
      }
    },
  };
}
