import { Folder, Search, X } from "lucide-react";
import * as React from "react";
import { useTranslation } from "react-i18next";

import { FileTypeIcon } from "@/components/agentre/file-type-icon";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";

import { PanelNotice, PanelSkeleton } from "./panel-feedback";
import { SidebarList } from "./sidebar-list";
import { SidebarRow } from "./sidebar-row";
import type { DirectorySearch, SearchHit } from "./use-directory-search";

type Props = {
  sessionId: number;
  cwd: string;
  remote: boolean;
  search: DirectorySearch;
};

/**
 * DirectorySearchPanel 是「目录」档搜索过滤态激活时的整个内容区：子档行下方
 * 的输入行（放大镜 / 输入区 / 命中计数 / 清除按钮）+ 结果区（served
 * requirement「文件搜索」）。DirectoryView 在 `search.active` 时整体换成渲染
 * 这个组件，不激活时渲染原来的树——两者互斥，树的 levels / expanded 状态因此
 * 不会被搜索过滤态触碰。
 *
 * 结果复用 SidebarRow：与 Git 页同一套「basename + 头截断目录后缀」扁平行，
 * 行的点击 / ⋯ 菜单 / 右键菜单与其它模式完全一致，不另起一套行实现。
 */
export function DirectorySearchPanel({
  sessionId,
  cwd,
  remote,
  search,
}: Props) {
  const { t } = useTranslation();
  const trimmed = search.query.trim();
  const placeholder = t("chatContext.search.placeholder");

  return (
    <>
      <div className="sticky top-0 z-10 flex h-8 shrink-0 items-center gap-1.5 border-b border-border bg-background px-2">
        <Search
          className="size-3.5 shrink-0 text-muted-foreground"
          aria-hidden="true"
        />
        <Input
          autoFocus
          value={search.query}
          onChange={(event) => search.setQuery(event.target.value)}
          placeholder={placeholder}
          aria-label={placeholder}
          className="h-6 flex-1 border-none bg-transparent px-1 text-xs shadow-none focus-visible:ring-0"
        />
        {search.state.status === "loaded" ? (
          <span
            data-testid="search-hit-count"
            className="shrink-0 font-mono text-3xs text-muted-foreground"
          >
            {t("chatContext.search.hitCount", {
              count: search.state.hits.length,
            })}
          </span>
        ) : null}
        <button
          type="button"
          aria-label={t("chatContext.search.clear")}
          className="flex size-5 shrink-0 items-center justify-center rounded-md text-muted-foreground hover:bg-muted hover:text-foreground"
          onClick={search.close}
        >
          <X className="size-3" aria-hidden="true" />
        </button>
      </div>
      <SearchBody
        sessionId={sessionId}
        cwd={cwd}
        remote={remote}
        query={search.query}
        trimmed={trimmed}
        state={search.state}
        onRetry={search.retry}
      />
    </>
  );
}

function SearchBody({
  sessionId,
  cwd,
  remote,
  query,
  trimmed,
  state,
  onRetry,
}: {
  sessionId: number;
  cwd: string;
  remote: boolean;
  query: string;
  trimmed: string;
  state: DirectorySearch["state"];
  onRetry: () => void;
}) {
  const { t } = useTranslation();

  if (trimmed === "") {
    return <PanelNotice text={t("chatContext.search.hint")} />;
  }
  if (state.status === "loading" || state.status === "idle") {
    return <PanelSkeleton label={t("chatContext.search.loading")} />;
  }
  if (state.status === "error") {
    return (
      <div className="px-3 py-6 text-center text-xs leading-relaxed text-muted-foreground">
        <p>{state.message || t("chatContext.search.readFailed")}</p>
        <Button
          type="button"
          variant="outline"
          size="sm"
          className="mt-2.5 h-7 text-2xs"
          onClick={onRetry}
        >
          {t("chatContext.directory.retry")}
        </Button>
      </div>
    );
  }

  const hits = state.hits;
  if (hits.length === 0) {
    return (
      <PanelNotice
        text={t("chatContext.search.empty", { query })}
        // 一条命中都没有、但遍历已经触及预算：结果**不完整**，「没有匹配的文件」
        // 单独出现会把「没找到」说成「找完了没有」。截断说明平时挂在结果列表末
        // 尾，空结果时那条列表根本不渲染，这里是它唯一的出口。
        hint={
          state.truncated
            ? t("chatContext.search.truncatedEmptyHint")
            : t("chatContext.search.emptyHint")
        }
      />
    );
  }

  return (
    <SidebarList
      variant="list"
      label={t("chatContext.search.resultsAria")}
      className="flex flex-col gap-0.5 px-2 py-2.5"
    >
      {hits.map((hit) => (
        <SearchRow
          key={hit.path}
          hit={hit}
          query={query}
          sessionId={sessionId}
          cwd={cwd}
          remote={remote}
        />
      ))}
      {state.truncated ? (
        <div className="py-1.5 pr-2.5 pl-2 text-2xs text-muted-foreground">
          {t("chatContext.search.truncated", { limit: hits.length })}
        </div>
      ) : null}
    </SidebarList>
  );
}

function splitBasename(path: string): { name: string; dir: string } {
  const cut = path.lastIndexOf("/");
  return {
    name: cut < 0 ? path : path.slice(cut + 1),
    dir: cut < 0 ? "" : path.slice(0, cut),
  };
}

function SearchRow({
  hit,
  query,
  sessionId,
  cwd,
  remote,
}: {
  hit: SearchHit;
  query: string;
  sessionId: number;
  cwd: string;
  remote: boolean;
}) {
  const { name, dir } = splitBasename(hit.path);
  return (
    <SidebarRow
      sessionId={sessionId}
      cwd={cwd}
      remote={remote}
      sourceMode="directory"
      // 搜索结果放弃树形，与其它扁平行（Git 页）用同一个 SidebarRow：命中的
      // 目录没有「展开」的意义（结果本身就是扁平列表，没有可以就地探入的下一
      // 层），因此一律按 kind="file" 渲染——图标按 isDir 换成 Folder，点击语义
      // 与「不可预览的文件行」完全一致（previewKind 对没有已知扩展名的目录名
      // 恒判 null，行天然不响应单击、也不出 hover 高亮），⋯ 菜单里「用默认应用
      // 打开 / 在文件管理器中显示 / 复制路径」三项对目录同样成立、照常渲染。
      kind="file"
      path={hit.path}
      name={name}
      nameChildren={<HighlightedText text={name} query={query} />}
      // 高亮把 basename 拆成了 <mark> + 纯文本几个兄弟节点：无障碍名计算会在
      // 节点之间插入空格（如 "chat .go"），把本来连续的文件名拆花。显式给出
      // ariaLabel，让可访问名固定等于未拆分的 name，不受高亮标记影响。
      ariaLabel={name}
      depth={0}
      title={hit.path}
      lead={
        hit.isDir ? (
          <Folder className="size-3.5 shrink-0" aria-hidden="true" />
        ) : (
          <FileTypeIcon path={hit.path} />
        )
      }
      trailing={
        // 目录后缀从头截断（rtl 让省略号落在开头），与 Git 页同一套形态；
        // 根目录下的命中 dir 为空串。
        <span
          dir="rtl"
          className="min-w-0 flex-1 shrink-[9999] truncate text-left font-mono text-3xs opacity-55"
        >
          {dir}
        </span>
      }
      testId="search-row"
      rowData={{
        "data-path": hit.path,
        "data-is-dir": hit.isDir ? "true" : undefined,
      }}
    />
  );
}

/** HighlightedText 把 name 中所有大小写不敏感的 query 命中包一层 <mark>。 */
function HighlightedText({
  text,
  query,
}: {
  text: string;
  query: string;
}): React.ReactElement {
  const needle = query.trim().toLowerCase();
  if (needle === "") return <>{text}</>;

  const lower = text.toLowerCase();
  const parts: React.ReactNode[] = [];
  let cursor = 0;
  let idx = lower.indexOf(needle, cursor);
  if (idx < 0) return <>{text}</>;

  let key = 0;
  while (idx >= 0) {
    if (idx > cursor) {
      parts.push(
        <React.Fragment key={key++}>{text.slice(cursor, idx)}</React.Fragment>,
      );
    }
    parts.push(
      <mark key={key++} className="rounded-sm bg-primary/25 text-inherit">
        {text.slice(idx, idx + needle.length)}
      </mark>,
    );
    cursor = idx + needle.length;
    idx = lower.indexOf(needle, cursor);
  }
  if (cursor < text.length) {
    parts.push(<React.Fragment key={key}>{text.slice(cursor)}</React.Fragment>);
  }
  return <>{parts}</>;
}
