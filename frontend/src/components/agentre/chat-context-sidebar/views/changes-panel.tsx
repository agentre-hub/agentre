import { useTranslation } from "react-i18next";

import { GitView } from "./git-view";
import { PanelNotice } from "./panel-feedback";
import type { GitChangesState } from "./use-git-changes";

type Props = {
  sessionId: number;
  cwd: string;
  remote: boolean;
  /** 会话有一个 git 工作目录；为 false（不是 git 仓库或没有工作目录）时只给一句说明。 */
  gitAvailable: boolean;
  git: GitChangesState;
  onRetry: () => void;
};

/**
 * ChangesPanel 是「变更」工具窗的内容：工作区相对 HEAD 还没提交的文件，一份扁平
 * 列表。
 *
 * 它只读 git，不从消息里按工具名派生——AI 常用 Bash / python 写文件，按工具名认出
 * 来的清单是残缺的，与 git 并列只会给出两个对不上的数字。没有 git 工作区时也就
 * 无从列出，给一句说明而不是空白。
 */
export function ChangesPanel({
  sessionId,
  cwd,
  remote,
  gitAvailable,
  git,
  onRetry,
}: Props) {
  const { t } = useTranslation();
  return (
    <div className="min-h-0 flex-1 overflow-auto">
      {gitAvailable ? (
        <GitView
          sessionId={sessionId}
          cwd={cwd}
          remote={remote}
          state={git}
          onRetry={onRetry}
        />
      ) : (
        <PanelNotice text={t("chatContext.changes.notARepo")} />
      )}
    </div>
  );
}
