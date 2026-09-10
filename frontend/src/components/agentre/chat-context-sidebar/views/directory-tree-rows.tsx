import { ChevronDown, ChevronRight, Folder } from "lucide-react";
import type { TFunction } from "i18next";

import { FileTypeIcon } from "@/components/agentre/file-type-icon";
import { Spinner } from "@agentre-hub/agentre-ui";
import { cn } from "@/lib/utils";

import type { workspace_fs_svc } from "@/../wailsjs/go/models";

import { collapseDirChain, type ChainEntry } from "../derive";
import {
  countSubtreeChanges,
  GIT_STATUS_META,
  gitStatusLabel,
} from "../git-rows";

import { SidebarRow } from "./sidebar-row";
import { indentStyle } from "./tree-indent";
import type { Level } from "./use-directory-levels";

type Entry = workspace_fs_svc.EntryView;

/**
 * 渲染一层需要知道的全部东西：树的两份状态、git 叠加的两份派生数据，以及展开手势。
 * 逐层递归的三个渲染函数共用同一份，不各自再算一遍。
 */
export type DirectoryTreeContext = {
  sessionId: number;
  cwd: string;
  remote: boolean;
  levels: Record<string, Level>;
  expanded: ReadonlySet<string>;
  gitStatusByPath: Map<string, string>;
  gitChangePaths: string[];
  chainChildrenOf: (relPath: string) => Array<ChainEntry<string>> | null;
  toggleDir: (relPath: string) => void;
  t: TFunction;
};

function renderFile(
  ctx: DirectoryTreeContext,
  entry: Entry,
  relPath: string,
  depth: number,
) {
  const { t } = ctx;
  // 未变动的文件在清单里没有条目，statusByPath 查不到 —— 不着色、不显示
  // 字母（served requirement 明确要求两者只对「变动」文件出现）。
  const status = ctx.gitStatusByPath.get(relPath);
  const meta = status
    ? (GIT_STATUS_META[status] ?? GIT_STATUS_META.modified)
    : null;
  return (
    <SidebarRow
      key={relPath}
      sessionId={ctx.sessionId}
      cwd={ctx.cwd}
      remote={ctx.remote}
      sourceMode="directory"
      kind="file"
      path={relPath}
      name={entry.name}
      nameClassName={meta?.className}
      depth={depth}
      title={relPath}
      lead={
        <>
          {/* 与目录 chevron 等宽的槽位，让同级目录名 / 文件名对齐。 */}
          <span className="size-3.5 shrink-0" aria-hidden="true" />
          <FileTypeIcon path={relPath} />
        </>
      }
      trailing={
        meta ? (
          <>
            {/* 字母对读屏隐藏，文字标签走 sr-only（与 Git 页同一套无障碍约定，
                两者共用 gitStatusLabel/GIT_STATUS_META，不重复定义颜色语义）。
                不显示 +N/−N 角标（served requirement 明确排除）。 */}
            <span
              data-status-letter
              aria-hidden="true"
              className={cn(
                "w-3 shrink-0 text-center font-mono text-3xs font-bold",
                meta.className,
              )}
            >
              {meta.letter}
            </span>
            <span className="sr-only">{gitStatusLabel(t, status!)}</span>
          </>
        ) : undefined
      }
      testId="directory-row"
      className={entry.gitIgnored ? "opacity-50" : undefined}
      rowData={{
        "data-git-ignored": entry.gitIgnored ? "true" : undefined,
        "data-git-status": status,
      }}
    />
  );
}

function renderDir(
  ctx: DirectoryTreeContext,
  entry: Entry,
  relPath: string,
  depth: number,
) {
  const { t } = ctx;
  // 展开/收起状态与「点这一行触发的取数」按链首（这一行在父层里的位置）为
  // 键，与压缩前完全一致：链变长变短不会让已经记下的展开态或缓存失效。
  const isOpen = ctx.expanded.has(relPath);
  const chain = collapseDirChain(entry.name, relPath, ctx.chainChildrenOf);
  const label = chain.names.join("/");
  const chainPrefix =
    chain.names.length > 1
      ? `${chain.names.slice(0, -1).join("/")}/`
      : undefined;
  const displayName = chain.names[chain.names.length - 1];
  // 链尾（chain.cursor）与展开后要渲染的那一层是同一个：无论链吸收了几
  // 段，子项都只比这一行多缩进一级（spec「其子项缩进只增加一级」）。
  const frontierPath = chain.cursor;
  const frontierLevel = ctx.levels[frontierPath];
  // 压缩链的子树统计按**链首**（relPath）算，不是链尾：「中间段只有一个子目
  // 录、不含文件」只对磁盘上还在的条目成立，listDir 看不到已删除的文件而 git
  // 照报。被链吸收掉的那几段里的变动在整棵树上没有任何一行，这个数字是它们
  // 唯一的出口；按链尾算会让同一行的数字在链变长的那一刻悄悄变小。
  const subtreeCount = countSubtreeChanges(ctx.gitChangePaths, relPath);
  return (
    <div key={relPath} className="flex flex-col">
      <SidebarRow
        sessionId={ctx.sessionId}
        cwd={ctx.cwd}
        remote={ctx.remote}
        sourceMode="directory"
        kind="dir"
        path={frontierPath}
        // 链首是这一行的稳定身份：链会随着更深的层加载进来而变长，键盘落点
        // 按链首记才不会在子项到达的那一刻被当成「这一行没了」。
        rowKey={relPath}
        name={displayName}
        chainPrefix={chainPrefix}
        depth={depth}
        title={label}
        expanded={isOpen}
        onToggle={() => ctx.toggleDir(relPath)}
        // 子树变动数的文字标签只能落在行的可访问名里：行的主按钮有显式
        // aria-label，放进按钮内部的 sr-only 文本会被 accname 计算整个盖掉。
        // 着色 + 裸数字因此不会成为唯一的信息载体（spec「键盘与无障碍」）。
        ariaLabel={[
          isOpen
            ? t("chatContext.files.collapseFolder", { name: label })
            : t("chatContext.files.expandFolder", { name: label }),
          subtreeCount > 0
            ? t("chatContext.directory.subtreeChanges", {
                count: subtreeCount,
              })
            : null,
        ]
          .filter((part) => part !== null)
          .join(", ")}
        lead={
          <>
            {frontierLevel?.status === "loading" ? (
              <Spinner
                className="size-3.5 shrink-0"
                aria-label={t("chatContext.directory.loading")}
              />
            ) : isOpen ? (
              <ChevronDown className="size-3.5 shrink-0" aria-hidden="true" />
            ) : (
              <ChevronRight className="size-3.5 shrink-0" aria-hidden="true" />
            )}
            <Folder className="size-3.5 shrink-0" aria-hidden="true" />
          </>
        }
        trailing={
          subtreeCount > 0 ? (
            // 数量为零时不显示（served requirement）；非零时用与「modified」
            // 同一份状态色，代表「这棵子树里有变动」，不区分具体是哪几类状态
            // 的混合（子树可能同时含多种状态，见 mockup H2：单个数字用一种
            // 状态色，不是 +N/−N 角标，也不逐类拆分）。数字对读屏隐藏，文字
            // 标签在行的 ariaLabel 里（与 Git 状态字母同一套无障碍约定）。
            <span
              data-testid="dir-subtree-count"
              aria-hidden="true"
              className={cn(
                "shrink-0 font-mono text-3xs font-medium tabular-nums",
                GIT_STATUS_META.modified.className,
              )}
            >
              {subtreeCount}
            </span>
          ) : undefined
        }
        testId="directory-row"
        className={entry.gitIgnored ? "opacity-50" : undefined}
        rowData={{
          "data-git-ignored": entry.gitIgnored ? "true" : undefined,
        }}
      />
      {isOpen ? (
        <div className="flex flex-col">
          {renderDirectoryLevel(ctx, frontierPath, depth + 1)}
        </div>
      ) : null}
    </div>
  );
}

/**
 * renderDirectoryLevel 渲染一层已加载的条目。单层读取失败只让这一个节点出错误行，
 * 树的其余部分照常可用。
 */
export function renderDirectoryLevel(
  ctx: DirectoryTreeContext,
  parentPath: string,
  depth: number,
): React.ReactNode {
  const { t } = ctx;
  const level = ctx.levels[parentPath];
  if (level === undefined || level.status === "loading") return null;
  if (level.status === "error") {
    return (
      <div
        role="alert"
        className="py-1.5 pr-2.5 text-xs text-destructive"
        style={indentStyle(depth)}
      >
        {level.message || t("chatContext.directory.readFailed")}
      </div>
    );
  }
  if (level.entries.length === 0) {
    return (
      <div
        className="py-1.5 pr-2.5 text-xs text-muted-foreground"
        style={indentStyle(depth)}
      >
        {t("chatContext.directory.empty")}
      </div>
    );
  }
  return (
    <>
      {sortEntries(level.entries).map((entry) => {
        const relPath = parentPath ? `${parentPath}/${entry.name}` : entry.name;
        return entry.isDir
          ? renderDir(ctx, entry, relPath, depth)
          : renderFile(ctx, entry, relPath, depth);
      })}
      {level.truncated ? (
        // 截断不静默：条目数就是后端这一层的实际上限（后端先过滤忽略项再截断）。
        <div
          className="py-1.5 pr-2.5 text-2xs text-muted-foreground"
          style={indentStyle(depth)}
        >
          {t("chatContext.directory.truncated", {
            limit: level.entries.length,
          })}
        </div>
      ) : null}
    </>
  );
}

/** 目录在前、各自名称字母序（后端按 os.ReadDir 的文件名序返回，不分目录/文件）。 */
function sortEntries(entries: Entry[]): Entry[] {
  return [...entries].sort((a, b) => {
    if (a.isDir !== b.isDir) return a.isDir ? -1 : 1;
    return a.name.localeCompare(b.name);
  });
}
