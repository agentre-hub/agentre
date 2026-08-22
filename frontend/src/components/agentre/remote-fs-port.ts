/**
 * 桌面端这一侧的 `ProjectFsPort`（规格 2026-08-22 D 段，决策 7 / 11）。
 *
 * 包里的目录选择器不认识 wails，也不认识中继——它只认 `ProjectFsPort`。这份 adapter
 * 就是把 wails 那两个绑定裹成那个契约，宿主专属的东西一件都不往包里漏。
 *
 * **关于错误分类的一处如实说明。** port 的契约是「交出判别式结果」，而 Go 那侧其实
 * 已经分好了类（`code.RemoteFsPermDenied` / `RemoteFsNotFound` / …）——但那个分类
 * **过不了 wails 边界**：到前端手里只剩一句本地化文本，没有码可读。按错误文案去反猜
 * 分类是错的做法（文案一改就静默失灵，而且中英两套要各猜一遍），所以这里一律交
 * `unknown` 并把那句原文带上，由包渲染成「读不到 X 上的这个目录。<原文>」。
 *
 * 代价：桌面端拿不到包里逐类写好的那几句出路（「换一个目录，或者在那台机器上放开
 * 权限」）。用户看到的仍是可分辨的句子（Go 那句），只是少了后半截建议。要补上得让
 * Go 侧把码带过 wails —— 本轮的硬不变量是「没有 Go 侧改动」，因此留到另一轮。
 */
import type {
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

/** 分不出类就如实说分不出，不去猜。 */
function unknown(err: unknown): { kind: "unknown"; message: string } {
  return { kind: "unknown", message: String(err) };
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
        return { ok: false, failure: unknown(err) };
      }
    },

    async mkdir(machineId, parent, name): Promise<MkdirOutcome> {
      try {
        await RemoteFsMkdir(machineId, parent, name);
        return { ok: true, result: undefined };
      } catch (err) {
        return { ok: false, failure: unknown(err) };
      }
    },
  };
}
