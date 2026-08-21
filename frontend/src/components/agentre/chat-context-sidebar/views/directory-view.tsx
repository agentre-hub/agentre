import { ChevronDown, ChevronRight, Folder, FolderOpen } from "lucide-react";
import * as React from "react";
import { useTranslation } from "react-i18next";

import {
  ProjectSetLocalPath,
  SelectDirectory,
  WorkspaceFsListDir,
} from "@/../wailsjs/go/app/App";
import { FileTypeIcon } from "@/components/agentre/file-type-icon";
import { Button } from "@/components/ui/button";
import { Spinner } from "@/components/ui/spinner";
import { cn } from "@/lib/utils";
import { useSessionStatus } from "@/stores/session-status-store";

import type { workspace_fs_svc } from "@/../wailsjs/go/models";

import { collapseDirChain, type ChainEntry } from "../derive";
import {
  type Change,
  countSubtreeChanges,
  GIT_STATUS_META,
  gitStatusLabel,
} from "../git-rows";

import { DirectorySearchPanel } from "./directory-search-panel";
import { errorText, PanelNotice, PanelSkeleton } from "./panel-feedback";
import { SidebarList } from "./sidebar-list";
import { SidebarRow } from "./sidebar-row";
import { indentStyle } from "./tree-indent";
import {
  INACTIVE_DIRECTORY_SEARCH,
  type DirectorySearch,
} from "./use-directory-search";

type Entry = workspace_fs_svc.EntryView;

/** 一层目录的取数状态。已加载的层按「相对 cwd 的路径」缓存，根是空串。 */
type Level =
  | { status: "loading" }
  | { status: "loaded"; entries: Entry[]; truncated: boolean }
  | { status: "error"; message: string };

const ROOT = "";

type Props = {
  sessionId: number;
  /** 会话工作目录；空串表示这个会话没有工作目录，直接出空态、不打后端。 */
  cwd: string;
  remote: boolean;
  showIgnored: boolean;
  /**
   * cwd 为空时的结构化原因（R10，ChatSessionDetail.cwdUnavailableReason）。
   * "local-path-missing" 时渲染专用空态（图标 + 指定入口），而不是复用
   * WorkspaceFsNoCwd 那句笼统的"没有工作目录"——三种没有 cwd 的情形要能分清
   * 该去哪里修。
   */
  cwdUnavailableReason?: string;
  /** 会话绑定的项目 id；R10 空态的"指定本机路径"入口据此调用 ProjectSetLocalPath。 */
  projectId?: number;
  /** 指定路径成功后的回调——调用方据此重新 LoadSession，让 cwd 变成非空。 */
  onCwdSpecified?: () => void;
  /**
   * git 状态叠加数据（served requirement「目录模式的 git 状态叠加」）：与 Git
   * 页「未提交」档同一份数据，由调用方（ChatContextSidebar 的 useGitChanges）
   * 统一取数并下发，本组件不自己发起取数。非 git 仓库、读取失败或尚未加载时
   * 为 null——目录树照常渲染，只是不带叠加。
   */
  gitChanges?: Change[] | null;
  /**
   * 搜索过滤态（served requirement「文件搜索」）：由 FilesPanel 的
   * useDirectorySearch 统一持有并下发，本组件不自己管这份状态——搜索按钮与
   * 「显示忽略项」同一行，在 FilesPanel 那一层，而不是这里。默认给一个恒定的
   * 「未激活」句柄，让不传这个 prop 的既有调用方（如 row.test.tsx 里直接渲染
   * DirectoryView 的用例）照常只渲染树，行为不变。
   */
  search?: DirectorySearch;
};

/**
 * DirectoryView 是「文件」页的「目录」模式：会话工作目录的完整文件树。
 *
 * 树按目录懒加载，每次只列一层（设计决策 6）：大仓一次性递归会遍历几十万文件。
 * 已加载的层与展开集合只存在组件内、不持久化，切换会话时清空（决策 12）；数据是
 * 快照，在「本模式可见且无缓存」与「当前会话轮次结束」两个时机自动重拉，没有手动
 * 刷新按钮（决策 13）。
 *
 * 路径解析、`.git` 恒隐藏与忽略判定全在后端（`WorkspaceFsListDir` 的入参是
 * sessionID 而不是路径，决策 2），本地会话与远端 agentred 会话走同一个绑定。
 */
export function DirectoryView({
  sessionId,
  cwd,
  remote,
  showIgnored,
  cwdUnavailableReason,
  projectId,
  onCwdSpecified,
  gitChanges = null,
  search = INACTIVE_DIRECTORY_SEARCH,
}: Props) {
  const { t } = useTranslation();
  const [levels, setLevels] = React.useState<Record<string, Level>>({});

  // 状态叠加只需要两样从变动清单派生的东西：按相对路径查状态（文件行着色/
  // 加字母）、以及路径本身的数组（目录行的子树计数）。两者都不依赖已加载的
  // 层——变动清单自带完整的仓库相对路径，子树计数因此在目录还没展开时也能
  // 算出来（decisive context：「不能只从已加载层派生」）。
  const gitStatusByPath = React.useMemo(() => {
    const map = new Map<string, string>();
    for (const c of gitChanges ?? []) map.set(c.path, c.status);
    return map;
  }, [gitChanges]);
  const gitChangePaths = React.useMemo(
    () => (gitChanges ?? []).map((c) => c.path),
    [gitChanges],
  );
  const [expanded, setExpanded] = React.useState<ReadonlySet<string>>(
    () => new Set(),
  );
  // R10「本机未配置路径」空态专用：就地指定路径的忙碌态与错误信息。
  const [specifying, setSpecifying] = React.useState(false);
  const [specifyErr, setSpecifyErr] = React.useState<string | null>(null);

  const handleSpecifyPath = async () => {
    if (!projectId) return;
    setSpecifyErr(null);
    setSpecifying(true);
    try {
      const picked = await SelectDirectory(t("projectNew.selectDirectory"));
      if (!picked) return;
      await ProjectSetLocalPath({ id: projectId, path: picked });
      onCwdSpecified?.();
    } catch (e) {
      setSpecifyErr(String(e));
    } finally {
      setSpecifying(false);
    }
  };

  // gen 是取数代际：会话 / 忽略开关变化后，先前在途的响应必须丢弃，否则慢的那
  // 一次会把新快照覆盖回旧数据。
  const genRef = React.useRef(0);
  const sessionRef = React.useRef(sessionId);

  // 重取那一步需要「当前展开了哪些层」，但它不能把 expanded 放进依赖里（那样每次
  // 展开都会重取整棵树），所以在这里把最新值镜像到 ref。
  const expandedRef = React.useRef(expanded);
  React.useEffect(() => {
    expandedRef.current = expanded;
  }, [expanded]);

  // doneTick 每次本会话轮次结束自增一次；别的会话结束不会动它。轮次结束是「文件
  // 可能变了」的唯一强信号，快照据此重拉（决策 13）。
  const doneTick = useSessionStatus(sessionId)?.doneTick ?? 0;

  const load = React.useCallback(
    (relPath: string) => {
      const gen = genRef.current;
      setLevels((prev) => ({ ...prev, [relPath]: { status: "loading" } }));
      WorkspaceFsListDir(sessionId, relPath, showIgnored).then(
        (res) => {
          if (genRef.current !== gen) return;
          setLevels((prev) => ({
            ...prev,
            [relPath]: {
              status: "loaded",
              entries: res.entries ?? [],
              truncated: res.truncated,
            },
          }));
        },
        (err: unknown) => {
          if (genRef.current !== gen) return;
          setLevels((prev) => ({
            ...prev,
            [relPath]: { status: "error", message: errorText(err) },
          }));
        },
      );
    },
    [sessionId, showIgnored],
  );

  // 快照失效并重取：换会话时连展开态一起清空；「显示忽略项」变化或本会话轮次结束
  // 时保留展开态，把根与每个已展开的层各重拉一遍（开关会改变后端返回的条目集合，
  // 轮次结束则可能改变任意一层的内容）。
  React.useEffect(() => {
    if (cwd === "") return;
    genRef.current += 1;
    const sameSession = sessionRef.current === sessionId;
    sessionRef.current = sessionId;
    const keep = sameSession ? expandedRef.current : new Set<string>();
    if (!sameSession) setExpanded(keep);
    setLevels({});
    for (const relPath of [ROOT, ...keep]) load(relPath);
  }, [sessionId, showIgnored, cwd, load, doneTick]);

  // chainChildrenOf 是 collapseDirChain 在这个懒加载数据源下的 childrenOf：只读
  // 已经取回的 levels，未加载的层返回 null——链压缩因此只折叠「已经知道」的部
  // 分，绝不会为了探链本身去发起额外请求（spec「树形模式的链压缩」）。
  const chainChildrenOf = React.useCallback(
    (relPath: string): Array<ChainEntry<string>> | null => {
      const level = levels[relPath];
      if (level === undefined || level.status !== "loaded") return null;
      return level.entries.map((entry) => ({
        name: entry.name,
        isDir: entry.isDir,
        cursor: relPath ? `${relPath}/${entry.name}` : entry.name,
      }));
    },
    [levels],
  );

  // 一个用户「展开」动作要能揭示整条已经缓存到的链，而不是每次只多下一段、
  // 逼用户对同一行反复点击（第二次点击只会把它收起）。这个 effect 在展开的
  // 起点仍处于「链探到头但还不知道再往下」时补一次取数；链每深入一层，
  // levels 变化会让它再检查一遍，直到链的真正末端解析出来（分支/文件/空目
  // 录）或失败为止——用户展开一次，压缩链背后的多级懒加载对其不可见。
  React.useEffect(() => {
    for (const start of expanded) {
      const chain = collapseDirChain(start, start, chainChildrenOf);
      if (chain.children === null && levels[chain.cursor] === undefined) {
        load(chain.cursor);
      }
    }
  }, [expanded, levels, chainChildrenOf, load]);

  const toggleDir = (relPath: string) => {
    const isOpen = expanded.has(relPath);
    setExpanded((prev) => {
      const next = new Set(prev);
      if (isOpen) next.delete(relPath);
      else next.add(relPath);
      return next;
    });
    // 首次展开才取数；收起再展开读缓存。读失败的那一层没有缓存可读，收起再展开
    // 就是它的重试手势（这一层不单独放重试按钮，240px 的行里塞不下）。
    const cached = levels[relPath];
    if (!isOpen && (cached === undefined || cached.status === "error")) {
      load(relPath);
    }
  };

  if (cwd === "") {
    // R10：本机未配置路径是一个专门的空态——不复用 WorkspaceFsNoCwd 那句笼统的
    // "没有工作目录"，就地给出与项目设置基本页签同一套文案与动作。
    if (cwdUnavailableReason === "local-path-missing") {
      return (
        <div
          data-testid="directory-local-path-missing"
          className="flex flex-col items-center gap-2 px-5 py-10 text-center"
        >
          <FolderOpen
            className="size-6 text-muted-foreground"
            aria-hidden="true"
          />
          <p className="text-sm font-semibold">
            {t("chatContext.directory.localPathMissingTitle")}
          </p>
          <p className="max-w-[22rem] text-xs leading-relaxed text-muted-foreground">
            {t("chatContext.directory.localPathMissingHint")}
          </p>
          <Button
            type="button"
            size="sm"
            className="mt-1 h-7 px-2 text-2xs"
            disabled={specifying || !projectId}
            onClick={() => void handleSpecifyPath()}
          >
            {specifying ? (
              <Spinner className="size-3" aria-label={t("common.loading")} />
            ) : null}
            {t("chatContext.directory.localPathMissingAction")}
          </Button>
          {specifyErr ? (
            <p className="text-2xs text-destructive">{specifyErr}</p>
          ) : null}
        </div>
      );
    }
    return <PanelNotice text={t("chatContext.directory.noCwd")} />;
  }

  // 搜索过滤态激活时整个内容区换成扁平的搜索结果；树的 levels / expanded 状态
  // 在上面那些 effect 里照常维护，不受这个分支影响——关闭过滤态回来时，同一份
  // 缓存与展开集合原样还在（spec「关闭过滤态…树的展开状态不受影响」）。
  if (search.active) {
    return (
      <DirectorySearchPanel
        sessionId={sessionId}
        cwd={cwd}
        remote={remote}
        search={search}
      />
    );
  }

  const root = levels[ROOT];
  if (root === undefined || root.status === "loading") {
    return <PanelSkeleton label={t("chatContext.directory.loading")} />;
  }
  if (root.status === "error") {
    return (
      <div className="px-3 py-6 text-center text-xs leading-relaxed text-muted-foreground">
        <p>{root.message || t("chatContext.directory.readFailed")}</p>
        <Button
          type="button"
          variant="outline"
          size="sm"
          className="mt-2.5 h-7 text-2xs"
          onClick={() => load(ROOT)}
        >
          {t("chatContext.directory.retry")}
        </Button>
      </div>
    );
  }

  const renderFile = (entry: Entry, relPath: string, depth: number) => {
    // 未变动的文件在清单里没有条目，statusByPath 查不到 —— 不着色、不显示
    // 字母（served requirement 明确要求两者只对「变动」文件出现）。
    const status = gitStatusByPath.get(relPath);
    const meta = status
      ? (GIT_STATUS_META[status] ?? GIT_STATUS_META.modified)
      : null;
    return (
      <SidebarRow
        key={relPath}
        sessionId={sessionId}
        cwd={cwd}
        remote={remote}
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
  };

  const renderDir = (entry: Entry, relPath: string, depth: number) => {
    // 展开/收起状态与「点这一行触发的取数」按链首（这一行在父层里的位置）为
    // 键，与压缩前完全一致：链变长变短不会让已经记下的展开态或缓存失效。
    const isOpen = expanded.has(relPath);
    const chain = collapseDirChain(entry.name, relPath, chainChildrenOf);
    const label = chain.names.join("/");
    const chainPrefix =
      chain.names.length > 1
        ? `${chain.names.slice(0, -1).join("/")}/`
        : undefined;
    const displayName = chain.names[chain.names.length - 1];
    // 链尾（chain.cursor）与展开后要渲染的那一层是同一个：无论链吸收了几
    // 段，子项都只比这一行多缩进一级（spec「其子项缩进只增加一级」）。
    const frontierPath = chain.cursor;
    const frontierLevel = levels[frontierPath];
    // 压缩链的子树统计按**链首**（relPath）算，不是链尾：「中间段只有一个子目
    // 录、不含文件」只对磁盘上还在的条目成立，listDir 看不到已删除的文件而 git
    // 照报。被链吸收掉的那几段里的变动在整棵树上没有任何一行，这个数字是它们
    // 唯一的出口；按链尾算会让同一行的数字在链变长的那一刻悄悄变小。
    const subtreeCount = countSubtreeChanges(gitChangePaths, relPath);
    return (
      <div key={relPath} className="flex flex-col">
        <SidebarRow
          sessionId={sessionId}
          cwd={cwd}
          remote={remote}
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
          onToggle={() => toggleDir(relPath)}
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
                <ChevronRight
                  className="size-3.5 shrink-0"
                  aria-hidden="true"
                />
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
            {renderLevel(frontierPath, depth + 1)}
          </div>
        ) : null}
      </div>
    );
  };

  // renderLevel 渲染一层已加载的条目。单层读取失败只让这一个节点出错误行，树的
  // 其余部分照常可用。
  const renderLevel = (parentPath: string, depth: number): React.ReactNode => {
    const level = levels[parentPath];
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
          const relPath = parentPath
            ? `${parentPath}/${entry.name}`
            : entry.name;
          return entry.isDir
            ? renderDir(entry, relPath, depth)
            : renderFile(entry, relPath, depth);
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
  };

  return (
    <SidebarList
      variant="tree"
      label={t("chatContext.directory.treeAria")}
      className="flex flex-col gap-0.5 px-2 py-2.5"
    >
      {renderLevel(ROOT, 0)}
    </SidebarList>
  );
}

/** 目录在前、各自名称字母序（后端按 os.ReadDir 的文件名序返回，不分目录/文件）。 */
function sortEntries(entries: Entry[]): Entry[] {
  return [...entries].sort((a, b) => {
    if (a.isDir !== b.isDir) return a.isDir ? -1 : 1;
    return a.name.localeCompare(b.name);
  });
}
