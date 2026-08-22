/**
 * 项目组头上的三样动作，两端共用那一份（规格 2026-08-22 C 段，决策 5/10）。
 *
 * **一份定义、两种容器**：⋮ 与在组头上右键给出同一份菜单 —— 同样的条目、同样的顺序、
 * 同样的分隔线位置、同样的危险项样式。两处各摆一遍就是两处各漏一项的机会，合之前
 * 桌面端的右键菜单就少了「成员…」「机器与路径…」而 ⋮ 上有。条目只在 `menuItems()`
 * 里定义一次，两种容器各渲染一遍。
 *
 * **能力开关**：条目全集在包里定义，宿主未声明的能力其条目**整条不出现**而不是置灰
 * —— 置灰在说「你以后可以」，而 web 宿主永远不会有终端。
 *
 * **窄屏常驻、宽屏才 hover 现身**：触摸屏上没有 hover，只挂 `group-hover` 等于移动端
 * 完全够不到。
 *
 * 这些元素嵌在组头那颗收放按钮里，因此是带 `role="button"` 的元素而不是嵌套
 * `<button>` —— HTML 不允许按钮套按钮。点它们不把这一组收起来。
 */
import * as React from "react";
import {
  FolderCog,
  FolderPlus,
  GitMerge,
  MoreVertical,
  Plus,
  Settings2,
  SquareTerminal,
  Trash2,
} from "lucide-react";

import { useUiTranslation } from "../i18n";
import { cn } from "../lib/utils";
import {
  ContextMenu,
  ContextMenuContent,
  ContextMenuItem,
  ContextMenuSeparator,
  ContextMenuTrigger,
} from "../ui/context-menu";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from "../ui/dropdown-menu";
import { Popover, PopoverContent, PopoverTrigger } from "../ui/popover";
import { AgentAvatar } from "../ui/agent-avatar";
import { groupActionRevealTouchClassName } from "../session-index/group-header";

/** 组头 ＋ 里列出的一个成员。 */
export interface ProjectHeaderMember {
  id: string;
  name: string;
  color?: string;
  avatarIcon?: React.ReactNode;
  avatarDataUrl?: string;
  /**
   * 从祖先项目继承来的。照样能选 —— 这里问的是「在这个项目里开对话」，不是「管这个
   * 项目的成员」；只是要挂一枚角标说清出处，否则读者会以为自己给这个子项目加过人。
   */
  inherited?: boolean;
}

/**
 * 宿主有哪几样能力。**没声明的条目整条不出现。**
 *
 * 包因此得认识两个它自己并不实现的概念（新建终端、合并到已有）—— 这是把「条目与
 * 顺序两端强制一致」换来的代价，已接受。agentre-server 两个都不声明。
 */
export interface ProjectMenuCapabilities {
  terminal?: boolean;
  merge?: boolean;
}

/**
 * 设置弹窗里那两节的标识。写成常量不是为了复用，是因为它们**不是 UI 文案** ——
 * 直接摆字面量会撞上 `i18next/no-literal-string`，而给那一行加豁免注释等于把
 * 「这里的字符串要不要翻译」这个判断交给下一个读者。
 */
const SECTION_MEMBERS = "members" as const;
const SECTION_PATHS = "paths" as const;

export interface ProjectHeaderActionsProps {
  projectId: string;
  projectName: string;
  /** 这个项目在账号里任何一台机器上都没有路径。 */
  unconfigured: boolean;
  capabilities?: ProjectMenuCapabilities;
  /**
   * 点 ＋ 时把这个项目的成员取出来：直接成员在前、继承的在后。
   *
   * **在浮层打开之前调用**：恰好一个成员时不弹浮层，直接开对话 —— 弹出来只是多一次
   * 点击，没有可选项。宿主手里已经有的就回一个已决议的 promise；要现拉的就在这里拉，
   * 这样也不会出现「浮层弹出来又自己关掉」那一闪。
   */
  loadMembers(projectId: string): Promise<ProjectHeaderMember[]>;
  /**
   * 「新建终端」那一条的子菜单内容（挑哪台机器）。机器清单与它怎么拉是宿主的事。
   *
   * 收的是 render prop 而不是节点：**两种容器的条目组件不是同一个**
   * （`ContextMenuItem` vs `DropdownMenuItem`），同一份节点塞进另一种容器不工作。
   * 不给就退成一条普通条目，直接调 `onNewTerminal`。
   */
  terminalSubmenu?: (kind: "dropdown" | "context") => React.ReactNode;
  onNewChat(projectId: string, memberId: string): void;
  /** focus 直落设置弹窗的某一节 —— 打开在顶部等于没有入口，那两节都在折叠线以下。 */
  onOpenSettings(projectId: string, focus?: "members" | "paths"): void;
  onNewSubproject(projectId: string): void;
  onNewTerminal?(projectId: string): void;
  onMergeInto?(projectId: string): void;
  onDelete(projectId: string): void;
}

interface MenuItemSpec {
  id: string;
  label: string;
  icon: React.ReactNode;
  danger?: boolean;
  separatorBefore?: boolean;
  /** 有它就渲染成子菜单，没有就是一条普通条目。 */
  submenu?: (kind: "dropdown" | "context") => React.ReactNode;
  run: () => void;
}

/** 条目全集与顺序，**只在这里定义一次**。 */
function useMenuItems(p: ProjectHeaderActionsProps): MenuItemSpec[] {
  const { t } = useUiTranslation();
  const items: MenuItemSpec[] = [
    {
      id: "settings",
      label: t("projectHeader.menu.settings"),
      icon: <Settings2 className="size-3.5" aria-hidden="true" />,
      run: () => p.onOpenSettings(p.projectId, undefined),
    },
    {
      id: "new-subproject",
      label: t("projectHeader.menu.newSubproject"),
      icon: <FolderPlus className="size-3.5" aria-hidden="true" />,
      run: () => p.onNewSubproject(p.projectId),
    },
    {
      id: "members",
      label: t("projectHeader.menu.members"),
      icon: <Plus className="size-3.5" aria-hidden="true" />,
      run: () => p.onOpenSettings(p.projectId, SECTION_MEMBERS),
    },
    {
      id: "paths",
      // 一台机器上都没配路径时它照常在列 —— 它正是去配路径的地方。
      label: t("projectHeader.menu.paths"),
      icon: <FolderCog className="size-3.5" aria-hidden="true" />,
      run: () => p.onOpenSettings(p.projectId, SECTION_PATHS),
    },
  ];
  if (p.capabilities?.terminal) {
    items.push({
      id: "terminal",
      label: t("projectHeader.menu.terminal"),
      icon: <SquareTerminal className="size-3.5" aria-hidden="true" />,
      submenu: p.terminalSubmenu,
      run: () => p.onNewTerminal?.(p.projectId),
    });
  }
  if (p.capabilities?.merge) {
    items.push({
      id: "merge",
      label: t("projectHeader.menu.merge"),
      icon: <GitMerge className="size-3.5" aria-hidden="true" />,
      run: () => p.onMergeInto?.(p.projectId),
    });
  }
  items.push({
    id: "delete",
    label: t("projectHeader.menu.delete"),
    icon: <Trash2 className="size-3.5" aria-hidden="true" />,
    danger: true,
    separatorBefore: true,
    run: () => p.onDelete(p.projectId),
  });
  return items;
}

/** 在组头上右键，给出与 ⋮ **同一份**菜单。 */
export function ProjectHeaderContextMenu({
  children,
  ...props
}: ProjectHeaderActionsProps & { children: React.ReactNode }) {
  const { t } = useUiTranslation();
  const items = useMenuItems(props);
  return (
    <ContextMenu>
      <ContextMenuTrigger asChild>
        <div data-testid="project-context-target" className="contents">
          {children}
        </div>
      </ContextMenuTrigger>
      <ContextMenuContent
        className="min-w-[180px]"
        aria-label={t("projectHeader.menu.label", { name: props.projectName })}
      >
        {items.map((item) => (
          <React.Fragment key={item.id}>
            {item.separatorBefore ? <ContextMenuSeparator /> : null}
            {item.submenu ? (
              item.submenu("context")
            ) : (
              <ContextMenuItem
                data-testid={`project-menu-item-${item.id}`}
                data-variant={item.danger ? "destructive" : undefined}
                variant={item.danger ? "destructive" : undefined}
                onSelect={item.run}
              >
                {item.icon}
                <span>{item.label}</span>
              </ContextMenuItem>
            )}
          </React.Fragment>
        ))}
      </ContextMenuContent>
    </ContextMenu>
  );
}

/** 组头名字右侧那一串：未配置角标 + ＋ + ⋮。 */
export function ProjectHeaderActions(props: ProjectHeaderActionsProps) {
  const { t } = useUiTranslation();
  const items = useMenuItems(props);
  const [addOpen, setAddOpen] = React.useState(false);
  const [members, setMembers] = React.useState<ProjectHeaderMember[] | null>(
    null,
  );
  const [membersFailed, setMembersFailed] = React.useState(false);
  /**
   * 这次关闭是「挑完了」还是「放弃了」。
   *
   * 挑完之后 Radix 默认把焦点还给 ＋，正好抹掉宿主刚给新对话输入框的那次 focus ——
   * 用户看到光标闪一下又没了。但**放弃时（Esc / 点外面）该还回去**，那是键盘用户
   * 找回位置的唯一途径。所以只在挑完那一支拦，不是无条件拦。
   */
  const pickedRef = React.useRef(false);

  const affordance = cn(
    "shrink-0 cursor-pointer rounded-sm p-0.5 text-muted-foreground hover:bg-accent hover:text-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring/50",
    groupActionRevealTouchClassName,
  );

  /** 嵌在组头按钮里的可点元素：不是 `<button>`，且不把这一组收起来。 */
  function affordanceProps(run: () => void) {
    return {
      role: "button",
      tabIndex: 0,
      onClick: (e: React.MouseEvent) => {
        e.stopPropagation();
        e.preventDefault();
        run();
      },
      onKeyDown: (e: React.KeyboardEvent) => {
        if (e.key !== "Enter" && e.key !== " ") return;
        e.preventDefault();
        e.stopPropagation();
        run();
      },
    };
  }

  const { loadMembers, onNewChat, projectId } = props;
  function handleAdd() {
    setMembersFailed(false);
    setMembers(null);
    void loadMembers(projectId).then(
      (loaded) => {
        // 恰好一个成员时不弹浮层，直接开对话。
        if (loaded.length === 1) {
          onNewChat(projectId, loaded[0].id);
          return;
        }
        setMembers(loaded);
        setAddOpen(true);
      },
      () => {
        // 读不上来不等于「这个项目没有成员」，说成空态会让人去加一个他已经加过的人。
        setMembersFailed(true);
        setAddOpen(true);
      },
    );
  }

  return (
    <>
      {props.unconfigured ? (
        <span
          data-testid={`project-unconfigured-${props.projectId}`}
          aria-label={t("projectHeader.unconfigured.aria", {
            name: props.projectName,
          })}
          className={cn(
            "shrink-0 cursor-pointer rounded-sm bg-muted px-1 py-0.5 text-3xs font-normal text-muted-foreground hover:bg-accent hover:text-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring/50",
          )}
          {...affordanceProps(() =>
            props.onOpenSettings(props.projectId, SECTION_PATHS),
          )}
        >
          {t("projectHeader.unconfigured.badge")}
        </span>
      ) : null}

      <Popover open={addOpen} onOpenChange={setAddOpen}>
        <PopoverTrigger asChild>
          <span
            data-testid={`project-add-${props.projectId}`}
            aria-label={t("projectHeader.add.aria", {
              name: props.projectName,
            })}
            className={affordance}
            {...affordanceProps(handleAdd)}
          >
            <Plus className="size-3" aria-hidden="true" />
          </span>
        </PopoverTrigger>
        {/*
          浮层里的点击**不许回流进组头那颗收放按钮**。

          Radix 的 PopoverContent 走 Portal：DOM 上挂在 document.body 下，但 React
          合成事件仍按 **React 树**冒泡，而这个组件被宿主塞在组头那颗
          `<button onClick={toggle}>` 里。不拦的话点一个成员 = 点了组头，那个项目
          当场收起来 —— 而 SessionGroup 还会把收起来这件事写进 localStorage。

          拦在**浮层根**上而不是逐个按钮上：逐个拦只堵住今天这几颗，以后每加一项
          都得记得再写一次。这条边界只有一个位置。
        */}
        <PopoverContent
          data-testid="project-add-popover"
          align="start"
          className="w-[220px] p-1"
          onClick={(e) => e.stopPropagation()}
          onCloseAutoFocus={(e) => {
            if (!pickedRef.current) return;
            pickedRef.current = false;
            e.preventDefault();
          }}
        >
          {membersFailed ? (
            <p
              data-testid="project-add-failed"
              className="px-2 py-2 text-xs text-destructive"
            >
              {t("projectHeader.add.failed")}
            </p>
          ) : members === null ? (
            <p className="px-2 py-2 text-xs text-muted-foreground">
              {t("projectHeader.add.loading")}
            </p>
          ) : members.length === 0 ? (
            // 空浮层什么也没说：给一条去加成员的路。
            <div
              data-testid="project-add-empty"
              className="px-2 py-2 text-xs text-muted-foreground"
            >
              <p>{t("projectHeader.add.noMembers")}</p>
              <button
                type="button"
                data-testid="project-add-empty-action"
                onClick={() => {
                  setAddOpen(false);
                  props.onOpenSettings(props.projectId, SECTION_MEMBERS);
                }}
                className="mt-1 text-primary-text hover:underline"
              >
                {t("projectHeader.add.addMembers")}
              </button>
            </div>
          ) : (
            <ul>
              {members.map((m) => (
                <li key={m.id}>
                  <button
                    type="button"
                    data-testid={`project-member-option-${m.id}`}
                    onClick={() => {
                      pickedRef.current = true;
                      setAddOpen(false);
                      props.onNewChat(props.projectId, m.id);
                    }}
                    className="flex w-full items-center gap-2 rounded-sm px-2 py-1.5 text-left text-xs hover:bg-accent"
                  >
                    <AgentAvatar
                      name={m.name}
                      color={m.color}
                      icon={m.avatarIcon}
                      avatarDataUrl={m.avatarDataUrl}
                      size="xs"
                    />
                    <span className="min-w-0 flex-1 truncate">{m.name}</span>
                    {m.inherited ? (
                      <span className="shrink-0 rounded-sm bg-secondary px-1.5 py-0.5 text-2xs text-muted-foreground">
                        {t("projectHeader.add.inherited")}
                      </span>
                    ) : null}
                  </button>
                </li>
              ))}
            </ul>
          )}
        </PopoverContent>
      </Popover>

      <DropdownMenu>
        <DropdownMenuTrigger asChild>
          <span
            data-testid={`project-menu-${props.projectId}`}
            aria-label={t("projectHeader.menu.aria", {
              name: props.projectName,
            })}
            className={affordance}
            role="button"
            tabIndex={0}
            onClick={(e) => e.stopPropagation()}
            onKeyDown={(e) => {
              if (e.key === "Enter" || e.key === " ") e.stopPropagation();
            }}
          >
            <MoreVertical className="size-3" aria-hidden="true" />
          </span>
        </DropdownMenuTrigger>
        <DropdownMenuContent
          align="end"
          aria-label={t("projectHeader.menu.label", {
            name: props.projectName,
          })}
          onClick={(e) => e.stopPropagation()}
        >
          {items.map((item) => (
            <React.Fragment key={item.id}>
              {item.separatorBefore ? <DropdownMenuSeparator /> : null}
              {item.submenu ? (
                item.submenu("dropdown")
              ) : (
                <DropdownMenuItem
                  data-testid={`project-menu-item-${item.id}`}
                  data-variant={item.danger ? "destructive" : undefined}
                  variant={item.danger ? "destructive" : undefined}
                  onSelect={item.run}
                >
                  {item.icon}
                  <span>{item.label}</span>
                </DropdownMenuItem>
              )}
            </React.Fragment>
          ))}
        </DropdownMenuContent>
      </DropdownMenu>
    </>
  );
}
