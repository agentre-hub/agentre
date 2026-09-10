import { FolderOpen } from "lucide-react";
import * as React from "react";
import { useTranslation } from "react-i18next";

import { ProjectSetLocalPath, SelectDirectory } from "@/../wailsjs/go/app/App";
import { Button, Spinner } from "@agentre-hub/agentre-ui";

import type { Change } from "../git-rows";

import { DirectorySearchPanel } from "./directory-search-panel";
import {
  renderDirectoryLevel,
  type DirectoryTreeContext,
} from "./directory-tree-rows";
import { PanelNotice, PanelSkeleton } from "./panel-feedback";
import { SidebarList } from "./sidebar-list";
import { ROOT, useDirectoryLevels, type Level } from "./use-directory-levels";
import {
  INACTIVE_DIRECTORY_SEARCH,
  type DirectorySearch,
} from "./use-directory-search";

type Props = {
  sessionId: number;
  /**
   * 当前工作根的绝对路径（多根会话下不一定是会话 cwd）；空串表示这个会话没有
   * 工作目录，直接出空态、不打后端。行的绝对路径与右键菜单都以它为基准。
   */
  cwd: string;
  /** 传给 workspacefs.* 绑定的 root 实参（空串 = 会话 cwd）。 */
  root: string;
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
 * DirectoryView 是「目录」页的内容区：当前工作根的完整文件树。
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
  root,
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

  const { load, chainChildrenOf } = useDirectoryLevels({
    sessionId,
    cwd,
    root,
    showIgnored,
    levels,
    setLevels,
    expanded,
    setExpanded,
  });

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

  const rootLevel = levels[ROOT];
  if (rootLevel === undefined || rootLevel.status === "loading") {
    return <PanelSkeleton label={t("chatContext.directory.loading")} />;
  }
  if (rootLevel.status === "error") {
    return (
      <div className="px-3 py-6 text-center text-xs leading-relaxed text-muted-foreground">
        <p>{rootLevel.message || t("chatContext.directory.readFailed")}</p>
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

  const treeCtx: DirectoryTreeContext = {
    sessionId,
    cwd,
    remote,
    levels,
    expanded,
    gitStatusByPath,
    gitChangePaths,
    chainChildrenOf,
    toggleDir,
    t,
  };

  return (
    <SidebarList
      variant="tree"
      label={t("chatContext.directory.treeAria")}
      className="flex flex-col gap-0.5 px-2 py-2.5"
    >
      {renderDirectoryLevel(treeCtx, ROOT, 0)}
    </SidebarList>
  );
}
