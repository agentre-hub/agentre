import { initialOf } from "../session-index/glyph";
import { AgentAvatar, type AgentAvatarSize } from "../ui/agent-avatar";

/**
 * 部门的身份记号：预设 ▸ 图标 ▸ 部门名首字，画在部门主题色上（规格 2026-11-02
 * 「预置头像」的优先级表）。
 *
 * 三档与 Agent / 项目那两枚同源，直接复用 `AgentAvatar` —— 尺寸、圆角、可及名与
 * 「预设整格画图、不叠色底」都在那一层实现好了，这里只补一条部门自己的规则：兜底
 * 画的是**原样首字**（`initialOf`，不套 Agent 那套两词首字母缩写），部门没有图标时
 * 不能被读成一个缩写。
 *
 * `icon` 是持久化的 icon key（`avatar_icon` 同一列），预设与词表图标都在
 * `AgentAvatar` 里认；认不出的 key 与「没有图标」同一条路。
 */
export type DepartmentMarkProps = {
  /** 部门名：既是首字的来源，也是这一格的可及名。 */
  name: string;
  /** 图标字段里的持久化 key，如 `"rocket"` / `"preset:team-dev"`。 */
  icon?: string;
  /** 部门主题色 token（`agent-1`…`agent-16` / `neutral`）。 */
  accentColor?: string;
  size?: AgentAvatarSize;
  className?: string;
};

export function DepartmentMark({
  name,
  icon,
  accentColor,
  size,
  className,
}: DepartmentMarkProps) {
  return (
    <AgentAvatar
      name={name}
      initials={initialOf(name)}
      color={accentColor}
      iconKey={icon}
      size={size}
      className={className}
    />
  );
}
