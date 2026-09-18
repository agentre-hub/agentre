import { OwnSessionsHeader } from "./own-sessions-header";
import { SessionGroup } from "./session-group";
import type { SessionGroupProps } from "./session-group";

/** 稳定的空列表：会话下沉进内层组时外层传它，别每次渲染新建一个数组。 */
const NO_SESSIONS: NonNullable<SessionGroupProps["sessions"]> = [];

export type ProjectSessionGroupProps = SessionGroupProps & {
  /**
   * 子项目插槽（已经渲染好的子树）。传 undefined 表示「这一层没有要渲染的子项目」——
   * 别传一个只会渲染出 null 的元素，那会被当成有子项目，平白多出一行子分组组头。
   */
  subprojects?: React.ReactNode;
  /** 父项目名，进子分组组头的无障碍名。 */
  ownSessionsName: string;
  /**
   * 子分组组头上的数。分页的宿主给项目的会话总数（首屏只是一页窗口，写「已加载
   * 几条」会与「查看全部 N」自相矛盾）；不给就数 `sessions`。
   */
  ownSessionsCount?: number;
};

/**
 * 项目轴上的一个项目组。
 *
 * 父项目同时有自己的会话和子项目时，把自己的会话下沉进一个可独立折叠的内层组
 * （持久化键 `<persistenceKey>:sessions`）。共用父级那一个箭头会把两者绑死 —— 会话
 * 多的父项目会把子项目整个挤出视野，而那正是这个子分组存在的理由。没有子项目、或
 * 自己没有会话时，它就是一个普通的 `SessionGroup`，子项目接在会话之后。
 *
 * 折叠态气泡（`collapsedAttentionSessions`）留在**外层**：把父项目整卡收起来时，
 * 要冒的是整棵子树的 attention，内层那个组头这时候根本不在屏幕上。
 */
export function ProjectSessionGroup({
  subprojects,
  ownSessionsName,
  ownSessionsCount,
  sessions = NO_SESSIONS,
  attentionSessions,
  totalSessions,
  renderSessionsPopover,
  renderAfterSessions,
  ...props
}: ProjectSessionGroupProps) {
  const hasSubprojects = subprojects !== undefined && subprojects !== null;
  const nest = hasSubprojects && sessions.length > 0;

  if (!nest) {
    return (
      <SessionGroup
        {...props}
        sessions={sessions}
        attentionSessions={attentionSessions}
        totalSessions={totalSessions}
        renderSessionsPopover={renderSessionsPopover}
        renderAfterSessions={
          hasSubprojects ? (
            <>
              {renderAfterSessions}
              {subprojects}
            </>
          ) : (
            renderAfterSessions
          )
        }
      />
    );
  }

  const {
    persistenceKey,
    selectedSessionId,
    onSessionSelect,
    renderLink,
    onOpenInNewTab,
    onRenameSession,
    onDeleteSession,
    canDeleteSession,
  } = props;

  return (
    <SessionGroup
      {...props}
      sessions={NO_SESSIONS}
      attentionSessions={NO_SESSIONS}
      renderAfterSessions={
        <>
          <SessionGroup
            persistenceKey={
              persistenceKey ? `${persistenceKey}:sessions` : undefined
            }
            defaultExpanded
            renderHeader={({ expanded, toggle }) => (
              <OwnSessionsHeader
                name={ownSessionsName}
                count={ownSessionsCount ?? sessions.length}
                expanded={expanded}
                onToggle={toggle}
              />
            )}
            sessions={sessions}
            attentionSessions={attentionSessions}
            totalSessions={totalSessions}
            renderSessionsPopover={renderSessionsPopover}
            selectedSessionId={selectedSessionId}
            onSessionSelect={onSessionSelect}
            renderLink={renderLink}
            onOpenInNewTab={onOpenInNewTab}
            onRenameSession={onRenameSession}
            onDeleteSession={onDeleteSession}
            canDeleteSession={canDeleteSession}
          />
          {renderAfterSessions}
          {subprojects}
        </>
      }
    />
  );
}
