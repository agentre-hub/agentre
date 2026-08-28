import {
  ChevronDown,
  ChevronRight,
  Columns2,
  Copy,
  ExternalLink,
  Eye,
  FolderOpen,
  List,
} from "lucide-react";
import * as React from "react";
import { useTranslation } from "react-i18next";

import {
  ContextMenuItem,
  ContextMenuSeparator,
  DropdownMenuItem,
  DropdownMenuSeparator,
} from "@agentre-hub/agentre-ui";

/**
 * 一行菜单的全部输入。文件行与目录行共用这一个模型：`kind` 决定项集合，三个
 * 「能不能做」的判定则全部编码成 null —— 不可预览的文件 previewPath 为 null
 * （预览两项整项不渲染），远端会话 absPath 为 null（本机三项整项不渲染），
 * 非「变动」模式 onJumpToTurn 为 null（跳轮次整项不渲染）。整项不渲染而非置灰
 * 是 spec「右键菜单」的明确取舍。
 */
export type RowMenuModel = {
  kind: "file" | "dir";
  /** 会话级 relPath；null = 该行不可预览。 */
  previewPath: string | null;
  /** 本机绝对路径；null = 远端会话或会话没有工作目录。 */
  absPath: string | null;
  /** 复制用的相对路径（换算不出相对路径时退回行本身的路径）。 */
  relPath: string;
  /** 文件名，「复制文件名」用。 */
  name: string;
  /** 目录行的展开态，决定菜单首项是「展开」还是「收起」。 */
  expanded: boolean;
  onToggle: () => void;
  onPreview: () => void;
  onPreviewInNewTab: () => void;
  onJumpToTurn: (() => void) | null;
  onOpenWith: () => void;
  onReveal: () => void;
  onCopy: (text: string) => void;
};

/**
 * 菜单项组件对：右键菜单与 ⋯ 按钮菜单是两套 Radix 原语，但项集合只有 RowMenu
 * 这一份定义 —— 两处渲染同一个组件、只换 Item / Separator，三种模式的菜单因此
 * 不会各自漂移。
 */
export type RowMenuParts = {
  Item: React.ComponentType<{
    onSelect?: (event: Event) => void;
    children: React.ReactNode;
  }>;
  Separator: React.ComponentType<Record<string, never>>;
};

export const CONTEXT_MENU_PARTS: RowMenuParts = {
  Item: ContextMenuItem,
  Separator: ContextMenuSeparator,
};

export const DROPDOWN_MENU_PARTS: RowMenuParts = {
  Item: DropdownMenuItem,
  Separator: DropdownMenuSeparator,
};

/**
 * RowMenu 渲染一行的菜单项。分组顺序（spec「右键菜单」）：
 * 文件行 = 预览两项 / 跳到对应轮次 / 本机打开两项 / 复制三项；
 * 目录行 = 展开收起 / 在文件管理器中显示 / 复制两项。
 * 空组不留分隔线。
 */
export function RowMenu({
  model,
  parts,
}: {
  model: RowMenuModel;
  parts: RowMenuParts;
}) {
  const { t } = useTranslation();
  const { Item, Separator } = parts;
  // 拆出 absPath / onJumpToTurn 的局部 const：null 判定要在回调闭包里仍然收窄，
  // 读 model.xxx 会在闭包里丢掉收窄、只能靠断言。
  const { absPath, onJumpToTurn } = model;
  const copyAbsPath =
    absPath === null
      ? []
      : [
          <Item key="copyAbs" onSelect={() => model.onCopy(absPath)}>
            <Copy />
            <span>{t("chatContext.row.copyAbsPath")}</span>
          </Item>,
        ];
  const reveal = (
    <Item key="reveal" onSelect={() => model.onReveal()}>
      <FolderOpen />
      <span>{t("chatContext.row.reveal")}</span>
    </Item>
  );
  const groups: React.ReactNode[][] =
    model.kind === "dir"
      ? [
          [
            <Item key="toggle" onSelect={() => model.onToggle()}>
              {model.expanded ? <ChevronDown /> : <ChevronRight />}
              <span>
                {model.expanded
                  ? t("chatContext.row.collapse")
                  : t("chatContext.row.expand")}
              </span>
            </Item>,
          ],
          absPath === null ? [] : [reveal],
          [
            <Item key="copyRel" onSelect={() => model.onCopy(model.relPath)}>
              <Copy />
              <span>{t("chatContext.row.copyRelPath")}</span>
            </Item>,
            ...copyAbsPath,
          ],
        ]
      : [
          model.previewPath === null
            ? []
            : [
                <Item key="preview" onSelect={() => model.onPreview()}>
                  <Eye />
                  <span>{t("chatContext.row.preview")}</span>
                </Item>,
                <Item
                  key="previewNewTab"
                  onSelect={() => model.onPreviewInNewTab()}
                >
                  <Columns2 />
                  <span>{t("chatContext.row.previewInNewTab")}</span>
                </Item>,
              ],
          onJumpToTurn === null
            ? []
            : [
                <Item key="jump" onSelect={() => onJumpToTurn()}>
                  <List />
                  <span>{t("chatContext.row.jumpToTurn")}</span>
                </Item>,
              ],
          absPath === null
            ? []
            : [
                <Item key="openWith" onSelect={() => model.onOpenWith()}>
                  <ExternalLink />
                  <span>{t("chatContext.row.openWith")}</span>
                </Item>,
                reveal,
              ],
          [
            <Item key="copyRel" onSelect={() => model.onCopy(model.relPath)}>
              <Copy />
              <span>{t("chatContext.row.copyRelPath")}</span>
            </Item>,
            ...copyAbsPath,
            <Item key="copyName" onSelect={() => model.onCopy(model.name)}>
              <Copy />
              <span>{t("chatContext.row.copyFileName")}</span>
            </Item>,
          ],
        ];

  const filled = groups.filter((group) => group.length > 0);
  return (
    <>
      {filled.map((group, index) => (
        <React.Fragment key={index}>
          {index > 0 ? <Separator /> : null}
          {group}
        </React.Fragment>
      ))}
    </>
  );
}
