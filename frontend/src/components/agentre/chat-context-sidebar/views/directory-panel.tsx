import { Eye, EyeOff, Search } from "lucide-react";
import { useTranslation } from "react-i18next";

import { Toggle } from "@agentre-hub/agentre-ui";

import { useChatSidebarStore } from "@/stores/chat-sidebar-store";

import type { Change } from "../git-rows";

import { DirectoryView } from "./directory-view";
import { GitStatusBar } from "./git-status-bar";
import { useDirectorySearch } from "./use-directory-search";
import type { GitState } from "./use-git-state";

type Props = {
  sessionId: number;
  /** 当前工作根的绝对路径（多根会话下不一定是会话 cwd）。 */
  cwd: string;
  /** 传给 workspacefs.* 绑定的 root 实参（空串 = 会话 cwd）。 */
  root: string;
  remote: boolean;
  /** 当前工作根的分支状态，第二行左端的状态条用它；null = 整条收起。 */
  gitState?: GitState | null;
  /**
   * 目录树的 git 状态叠加数据：非 git 仓库、读取失败或尚未加载时为 null——目录树
   * 照常渲染，只是不带叠加。
   */
  gitChanges?: Change[] | null;
  /** R10：cwd 为空时的结构化原因，据此渲染专用空态。 */
  cwdUnavailableReason?: string;
  /** 会话绑定的项目 id，R10 空态的"指定本机路径"入口据此调用 ProjectSetLocalPath。 */
  projectId?: number;
  /** 指定路径成功后的回调——调用方据此重新 LoadSession。 */
  onCwdSpecified?: () => void;
};

/**
 * DirectoryPanel 是「目录」页的外壳：一行左端是 git 状态条（决策 7：分支 /
 * 领先落后 / 未提交数只挂目录页）、右端是「显示忽略项」与「搜索」两个纯图标
 * 按钮，下面是内容区。「目录」已是顶层 tab（决策 1），页内不再有档位胶囊；这
 * 一行连同顶层 TabBar 共两行常驻 chrome，搜索输入框只在过滤态激活期间占临时
 * 的第三行。
 *
 * 目录树带 git 状态叠加，因此它就是本侧栏的 git 全景视图（决策 1）。
 */
export function DirectoryPanel({
  sessionId,
  cwd,
  root,
  remote,
  gitState = null,
  gitChanges = null,
  cwdUnavailableReason,
  projectId,
  onCwdSpecified,
}: Props) {
  const { t } = useTranslation();
  const showIgnored = useChatSidebarStore((s) => s.showIgnored);
  const setShowIgnored = useChatSidebarStore((s) => s.setShowIgnored);
  // 搜索过滤态与查询串是本次查看会话的临时交互状态，不进持久化 store（决策见
  // use-directory-search.ts 顶部注释）；持有在这一层是因为搜索按钮与「显示
  // 忽略项」同一行在这里，而结果区在 DirectoryView——两者需要共享同一份状态。
  const search = useDirectorySearch(sessionId, root, showIgnored);

  return (
    <div className="flex min-h-0 flex-1 flex-col">
      <div className="flex h-8 shrink-0 items-center gap-1.5 border-b border-border px-2">
        <GitStatusBar state={gitState} />
        <Toggle
          size="sm"
          className="ml-auto h-6 w-6 shrink-0 p-0 data-[state=on]:bg-transparent data-[state=on]:text-primary"
          pressed={showIgnored}
          onPressedChange={setShowIgnored}
          aria-label={t("chatContext.directory.showIgnored")}
          title={
            showIgnored
              ? t("chatContext.directory.showIgnoredOn")
              : t("chatContext.directory.showIgnoredOff")
          }
        >
          {showIgnored ? (
            <Eye className="size-3" aria-hidden="true" />
          ) : (
            <EyeOff className="size-3" aria-hidden="true" />
          )}
        </Toggle>
        {cwd !== "" ? (
          <Toggle
            size="sm"
            className="h-6 w-6 shrink-0 p-0 data-[state=on]:bg-transparent data-[state=on]:text-primary"
            pressed={search.active}
            onPressedChange={search.toggle}
            aria-label={t("chatContext.directory.search")}
            title={
              search.active
                ? t("chatContext.directory.searchOn")
                : t("chatContext.directory.searchOff")
            }
          >
            <Search className="size-3" aria-hidden="true" />
          </Toggle>
        ) : null}
      </div>
      <div className="min-h-0 flex-1 overflow-auto">
        <DirectoryView
          sessionId={sessionId}
          cwd={cwd}
          root={root}
          remote={remote}
          showIgnored={showIgnored}
          cwdUnavailableReason={cwdUnavailableReason}
          projectId={projectId}
          onCwdSpecified={onCwdSpecified}
          gitChanges={gitChanges}
          search={search}
        />
      </div>
    </div>
  );
}
