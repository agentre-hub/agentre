import { Ellipsis } from "lucide-react";
import * as React from "react";
import { useTranslation } from "react-i18next";
import {
  resolvePreviewRelPath,
  toRelPath,
  copyTextWithToast,
} from "@agentre-ai/agentre-ui";

import {
  ContextMenu,
  ContextMenuContent,
  ContextMenuTrigger,
} from "@/components/ui/context-menu";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu";
import { cn } from "@/lib/utils";
import { useChatSidebarStore } from "@/stores/chat-sidebar-store";

import type {
  FilePreviewTab,
  PreviewSourceMode,
} from "@/stores/chat-sidebar-store";

import {
  CONTEXT_MENU_PARTS,
  DROPDOWN_MENU_PARTS,
  RowMenu,
  type RowMenuModel,
} from "./row-menu";
import { useSidebarListRow } from "./sidebar-list";
import { indentStyle } from "./tree-indent";
import { openTarget, useOpenFile, useRevealFile } from "./use-open-file";

type Props = {
  sessionId: number;
  /** 会话工作目录；空串表示会话没有工作目录（本机能力随之消失）。 */
  cwd: string;
  remote: boolean;
  /** 行来自哪个模式：决定预览标签的首视图，也决定有没有「跳到对应轮次」。 */
  sourceMode: PreviewSourceMode;
  kind: "file" | "dir";
  /** 行的路径：「变动」模式可能是工具调用给的绝对路径，其余模式相对 cwd。 */
  path: string;
  /** 显示名（basename、目录名，或链压缩行的末段）。 */
  name: string;
  /**
   * 名称的可选替代渲染（搜索结果高亮命中子串用）。设置时代替 `name` 的纯文本
   * 显示，但 `name` 本身仍照旧驱动 `data-name` 与菜单的「复制文件名」——高亮
   * 只改视觉，不改这一行在其它地方被识别 / 操作的方式。拆成多个兄弟节点会让
   * 无障碍名计算在节点之间插入多余空格，调用方设置本 prop 时应一并显式传
   * `ariaLabel={name}`，让可访问名固定等于未拆分的纯文本。只对非链压缩行
   * 生效（链压缩行的截断方向依赖纯文本测量，搜索结果本就不会同时是链压缩
   * 行）。
   */
  nameChildren?: React.ReactNode;
  /**
   * 名称文字色的覆盖：目录模式的 git 状态叠加给变动文件着色时用（served
   * requirement「目录模式的 git 状态叠加」）；未设置时用默认的 muted 文字色。
   */
  nameClassName?: string;
  /**
   * 链压缩行独有：链中除末段外的前缀（含尾部 "/"，如 "internal/service/"）。
   * 未设置时按普通单段名渲染，行为与压缩前完全一致。设置时整段「前缀 + name」
   * 作为一个整体从头截断（保留末段）——与 Git 页目录后缀的截断方向一致。
   */
  chainPrefix?: string;
  depth: number;
  title?: string;
  /** 仅「变动」模式的文件行：跳到该文件最后被改动的轮次。 */
  onJumpToTurn?: () => void;
  /** 仅目录行。 */
  expanded?: boolean;
  onToggle?: () => void;
  /**
   * 键盘导航用的稳定行标识，默认取 `path`。链压缩的目录行必须传链首：链会随着
   * 更深的层加载进来而变长，`path`（链尾）跟着变，roving 落点会因此被当成「这
   * 一行没了」而弹回首行。
   */
  rowKey?: string;
  /** 主按钮的可访问名；目录行用「展开 / 收起 X」，文件行留空取文本内容。 */
  ariaLabel?: string;
  /** 名称之前的固定宽列：树模式是展开箭头 + 类型图标，Git 模式是状态字母。 */
  lead: React.ReactNode;
  /** 名称之后、⋯ 之前的模式特有信息（diff 角标、目录后缀…）。 */
  trailing?: React.ReactNode;
  testId: string;
  className?: string;
  /** 额外的 data-* 属性（各模式的既有断言锚点）。 */
  rowData?: Record<string, string | undefined>;
  /**
   * 是否给这一行菜单与 ⋯ 槽位。默认给；只有「变动」模式的目录行不给 —— spec
   * 「右键菜单」列举的是「三种模式的文件行、目录模式的目录行」，而变动模式的
   * 目录行是从工具调用路径派生出的结构分组，本身没有可靠的磁盘路径（工具给的
   * 可能是绝对路径），「在文件管理器中显示 / 复制绝对路径」会指向不存在的位置。
   */
  withMenu?: boolean;
};

/**
 * SidebarRow 是「变动 / 目录 / Git」三个模式唯一的行渲染实现。
 *
 * 收敛到一处是刻意的：三种模式此前各写一遍行，点击语义因此长期分叉（变动跳轮次、
 * Git 打开文件、目录不可点）。现在单击语义只有一份——可预览的文件行单击 = 开临时
 * 预览标签、双击 = 转常驻，目录行单击 = 展开收起，不可预览的文件行不响应单击也不
 * 出 hover 高亮（spec「行的形态与交互」）。行右端是恒占 24px、hover 或键盘聚焦才
 * 显形的 ⋯ 槽位，它与右键打开同一份 RowMenu。
 */
export function SidebarRow({
  sessionId,
  cwd,
  remote,
  sourceMode,
  kind,
  path,
  name,
  nameChildren,
  nameClassName,
  chainPrefix,
  depth,
  title,
  onJumpToTurn,
  expanded = false,
  onToggle,
  rowKey,
  ariaLabel,
  lead,
  trailing,
  testId,
  className,
  rowData,
  withMenu = true,
}: Props) {
  const { t } = useTranslation();
  // ⋯ 菜单受控：右键之外，键盘的 Shift+F10 / 菜单键也要能开出同一份菜单
  // （ContextMenu 原语没有受控的 open，所以键盘走 DropdownMenu 这一份）。
  const [menuOpen, setMenuOpen] = React.useState(false);
  const openPreview = useChatSidebarStore((s) => s.openPreview);
  const openPreviewInNewTab = useChatSidebarStore((s) => s.openPreviewInNewTab);
  const restoreClobberedPreviewTab = useChatSidebarStore(
    (s) => s.restoreClobberedPreviewTab,
  );
  const activePreviewPath = useChatSidebarStore(
    (s) => s.previewTabsBySession[sessionId]?.activePath,
  );
  const openFile = useOpenFile(cwd);
  const revealFile = useRevealFile(cwd);

  // clobberedTempRef 记下**本次手势的第一次 click** 原地替换掉的临时标签
  // （openPreview 的返回值），只为了双击手势自我修复：真实鼠标双击在派发 dblclick
  // 前会先各打一次 click，第一次 click 把当时的临时标签原地替换成这一行；dblclick
  // 触发时用这个槽位把它补回来，不然双击结束后就只剩双击的这一行，而不是「原临时
  // 标签 + 双击的行转常驻」两个标签。
  const clobberedTempRef = React.useRef<FilePreviewTab | null>(null);

  // 可预览性与「路径 → 会话级 relPath」的判定只有 previewable.ts 一处；null 表示
  // 扩展名不在 allowlist 内，或绝对路径解析后落在 cwd 之外。
  const previewPath = kind === "file" ? resolvePreviewRelPath(path, cwd) : null;
  // 复制相对路径：换算不出相对路径（越出 cwd）时退回行本身的路径，菜单项不消失。
  const relPath = toRelPath(path, cwd) ?? path;
  // 远端会话拿到的是对端路径，本机命令不能碰；无 cwd 时也拼不出绝对路径。
  const absPath = remote || cwd === "" ? null : openTarget(path, cwd);

  const interactive = kind === "dir" || previewPath !== null;
  const active = previewPath !== null && previewPath === activePreviewPath;

  // openPreviewFromRow 打开预览，并按「这是手势里的第几次 click」维护
  // clobberedTempRef。clickCount 取 MouseEvent.detail —— 浏览器按系统双击间隔判定
  // 的连击序号，也是这里唯一能把「双击的第二次 click」与「一次全新的单击」区分开
  // 的东西：这两种情形在 store 看来都是「这一行已经开着」、都没替换掉任何标签。
  //   - 连击序号 ≥2（双击的第二次 click）：不动槽位，否则第一次 click 记下的标签
  //     就丢了，双击结束后补不回来。
  //   - 其余（第一次 click，以及菜单 / Enter 这类没有连击序号的入口）：无条件覆盖
  //     槽位。上一次手势替换掉的标签必须在这里被清掉，否则「先单击开出临时标签、
  //     之后再双击同一行转常驻」会把它当成刚被这次双击吞掉的标签复活出来。
  const openPreviewFromRow = (clickCount: number) => {
    if (previewPath === null) return;
    const clobbered = openPreview(sessionId, previewPath, sourceMode);
    if (clickCount < 2) clobberedTempRef.current = clobbered;
  };

  const copy = React.useCallback(
    (text: string) => {
      void copyTextWithToast(text, { successTitle: t("common.copied") });
    },
    [t],
  );

  const model: RowMenuModel = {
    kind,
    previewPath,
    absPath,
    relPath,
    name,
    expanded,
    onToggle: () => onToggle?.(),
    // 菜单项 / Enter 没有连击序号，一律按「一次全新的单击」处理。
    onPreview: () => openPreviewFromRow(1),
    onPreviewInNewTab: () => {
      if (previewPath !== null) {
        openPreviewInNewTab(sessionId, previewPath, sourceMode);
      }
    },
    onJumpToTurn: onJumpToTurn ?? null,
    onOpenWith: () => openFile(path),
    onReveal: () => revealFile(path),
    onCopy: copy,
  };

  // 行接进所在列表的键盘模型：Enter / ←→ / Shift+F10 要执行的就是单击、展开收起
  // 与右键菜单这同一组动作，不另起一套键盘专用分支。
  const listRow = useSidebarListRow(`${kind}:${rowKey ?? path}`, {
    toggle: kind === "dir" ? () => model.onToggle() : null,
    activate:
      kind === "file" && previewPath !== null ? () => model.onPreview() : null,
    openMenu: withMenu ? () => setMenuOpen(true) : null,
  });

  const nameNode = chainPrefix ? (
    // 压缩行的截断方向反过来：dir="rtl" + text-left 让省略号落在开头，尾部
    // （末段，节点的身份）永远保留，与 Git 页目录后缀的截断方向一致。
    <span
      dir="rtl"
      className={cn(
        "min-w-0 shrink truncate text-left font-mono",
        nameClassName,
      )}
      data-testid="chain-name"
    >
      <span className="opacity-55" data-testid="chain-prefix">
        {chainPrefix}
      </span>
      {name}
    </span>
  ) : (
    <span className={cn("min-w-0 shrink truncate font-mono", nameClassName)}>
      {nameChildren ?? name}
    </span>
  );
  const body = (
    <>
      {lead}
      {nameNode}
      {trailing}
    </>
  );
  const bodyClassName =
    "flex min-w-0 flex-1 items-center gap-1.5 py-1.5 text-left";

  const row = (
    <div
      {...rowData}
      {...listRow}
      // 展开态与层级挂在行上（不在行内按钮上）：行才是 treeitem，读屏据此宣告
      // 「已展开 / 已收起、第几层」，键盘导航也读同一份属性。扁平列表没有层级，
      // 只用 aria-selected 表达「这一行正开在预览面板里」。
      aria-expanded={kind === "dir" ? expanded : undefined}
      aria-level={listRow?.role === "treeitem" ? depth + 1 : undefined}
      aria-selected={listRow ? active : undefined}
      data-testid={testId}
      data-name={name}
      title={title}
      style={indentStyle(depth)}
      className={cn(
        "group/row flex items-center gap-1.5 rounded-md pr-1 pl-2 text-xs text-muted-foreground transition-colors",
        "outline-none focus-visible:ring-2 focus-visible:ring-ring/50",
        interactive && "hover:bg-muted/50",
        active && "bg-accent text-accent-foreground",
        className,
      )}
    >
      {interactive ? (
        <button
          type="button"
          // 行内的按钮都不是 Tab 停靠点：整个列表对 Tab 只停一次，进列表之后靠
          // 方向键走行、Shift+F10 开菜单。
          tabIndex={-1}
          aria-label={ariaLabel}
          className={bodyClassName}
          onClick={(event) => {
            if (kind === "dir") model.onToggle();
            // event.detail 是这次 click 在手势里的连击序号，双击的第二次 click 靠
            // 它被识别出来（见 openPreviewFromRow）。
            else openPreviewFromRow(event.detail);
          }}
          // 双击 = 转常驻标签，与预览标签条的双击语义一致；目录行没有第二种打开
          // 方式，双击就是连续两次展开收起，交给 onClick 自然处理。真实鼠标双击
          // 在派发这个事件前已经跑过两次 onClick 了（见 clobberedTempRef 声明处
          // 的注释）：先转常驻，再补回被原地替换掉的临时标签——不然「原临时标签
          // 依旧临时 + 双击的行转常驻」就会塌缩成只剩双击的这一行。先转常驻再补
          // 回也保证任何时刻都不会同时存在两个临时标签。
          onDoubleClick={
            kind === "file"
              ? () => {
                  model.onPreviewInNewTab();
                  const clobbered = clobberedTempRef.current;
                  clobberedTempRef.current = null;
                  if (clobbered) {
                    restoreClobberedPreviewTab(sessionId, clobbered);
                  }
                }
              : undefined
          }
        >
          {body}
        </button>
      ) : (
        // 不可交互的行同样要认 ariaLabel:搜索结果的高亮把 basename 拆成
        // <mark> + 纯文本几个兄弟节点,无障碍名计算会在节点之间插进空格,而目录
        // 命中与不在 allowlist 内的文件恰恰全落在这一支——ariaLabel 正是为它们
        // 传进来的(见 nameChildren 的说明与 directory-search-panel.tsx)。
        <div aria-label={ariaLabel} className={bodyClassName}>
          {body}
        </div>
      )}
      {withMenu ? (
        <DropdownMenu open={menuOpen} onOpenChange={setMenuOpen}>
          <DropdownMenuTrigger asChild>
            <button
              type="button"
              tabIndex={-1}
              aria-label={t("chatContext.row.menu")}
              // 槽位恒占 24px、只切换可见性：条件渲染会让整行文字在 hover 时左右
              // 跳（spec「行的形态与交互」）。键盘走到这一行（行本身聚焦）时同样
              // 显形，否则 ⋯ 这个入口对键盘用户是隐形的。
              className="flex size-6 shrink-0 items-center justify-center rounded-md text-muted-foreground opacity-0 transition-opacity group-focus-within/row:opacity-100 group-hover/row:opacity-100 hover:text-foreground focus-visible:opacity-100 data-[state=open]:opacity-100"
            >
              <Ellipsis className="size-3.5" aria-hidden="true" />
            </button>
          </DropdownMenuTrigger>
          <DropdownMenuContent align="end">
            <RowMenu model={model} parts={DROPDOWN_MENU_PARTS} />
          </DropdownMenuContent>
        </DropdownMenu>
      ) : null}
    </div>
  );

  if (!withMenu) return row;
  return (
    <ContextMenu>
      <ContextMenuTrigger asChild>{row}</ContextMenuTrigger>
      <ContextMenuContent>
        <RowMenu model={model} parts={CONTEXT_MENU_PARTS} />
      </ContextMenuContent>
    </ContextMenu>
  );
}
