import { ChevronDown, ChevronRight, Folder } from "lucide-react";
import * as React from "react";
import { useTranslation } from "react-i18next";

import { FileTypeIcon } from "@/components/agentre/file-type-icon";

import { basename } from "../../file-preview/file-meta";
import {
  collapseDirChain,
  deriveFileTree,
  type ChainEntry,
  type FileEntry,
  type FileTreeNode,
} from "../derive";

import { SidebarList } from "./sidebar-list";
import { SidebarRow } from "./sidebar-row";

type DirNode = Extract<FileTreeNode, { kind: "dir" }>;

/**
 * treeChildrenOf 是 collapseDirChain 在「变动」模式下的 childrenOf：整棵树一次性
 * 已知（deriveFileTree 的产物），所以恒不返回 null——链压缩在这个模式下总能探到
 * 底，唯一会让链终止的是遇到文件或分支，不存在「还不知道」的情况。
 */
function treeChildrenOf(
  node: FileTreeNode,
): Array<ChainEntry<FileTreeNode>> | null {
  if (node.kind !== "dir") return null;
  return node.children.map((child) => ({
    name: child.kind === "dir" ? child.name : basename(child.entry.path),
    isDir: child.kind === "dir",
    cursor: child,
  }));
}

type Props = {
  sessionId: number;
  files: FileEntry[];
  cwd: string;
  remote: boolean;
  onJumpToTurn: (turn: number) => void;
};

function DiffBadge({ plus, minus }: { plus: number; minus: number }) {
  if (plus <= 0 && minus <= 0) return null;
  return (
    <span
      aria-hidden="true"
      className="inline-flex shrink-0 items-center gap-1 font-mono text-3xs font-medium"
    >
      {plus > 0 ? <span className="text-status-running">+{plus}</span> : null}
      {minus > 0 ? <span className="text-destructive">−{minus}</span> : null}
    </span>
  );
}

export function FilesView({
  sessionId,
  files,
  cwd,
  remote,
  onJumpToTurn,
}: Props) {
  const { t } = useTranslation();
  const tree = React.useMemo(() => deriveFileTree(files), [files]);
  const filePathsKey = files.map((file) => file.path).join("\u0000");

  // 展开状态仅存组件内、不持久化；文件集合变化时重置为全部展开。
  const [collapsed, setCollapsed] = React.useState<Set<string>>(new Set());
  React.useEffect(() => {
    setCollapsed(new Set());
  }, [filePathsKey]);

  const toggleCollapse = (dirPath: string) => {
    setCollapsed((prev) => {
      const next = new Set(prev);
      if (next.has(dirPath)) next.delete(dirPath);
      else next.add(dirPath);
      return next;
    });
  };

  if (files.length === 0) {
    return (
      <div className="px-3 py-6 text-center text-xs text-muted-foreground">
        {t("chatContext.files.empty")}
      </div>
    );
  }

  const renderDir = (node: DirNode, parentPath: string, depth: number) => {
    // 链压缩（spec「路径展示」）：只包含一个子目录且不直接包含文件的目录链折
    // 叠为一行。「变动」模式的整棵树一次性可得，chain.children 因此恒不为
    // null——链总能探到底（文件或分支），不存在「还不知道」的中间态。
    const chain = collapseDirChain(node.name, node, treeChildrenOf);
    const label = chain.names.join("/");
    const chainPrefix =
      chain.names.length > 1
        ? `${chain.names.slice(0, -1).join("/")}/`
        : undefined;
    const displayName = chain.names[chain.names.length - 1];
    const startPath = parentPath ? `${parentPath}/${node.name}` : node.name;
    const terminalPath = parentPath ? `${parentPath}/${label}` : label;
    // 展开/收起状态按链首（这一行在其父容器里的位置）为键，与压缩前保持同一份
    // 语义——链变长变短不会让已经记下的展开态失效。
    const isCollapsed = collapsed.has(startPath);
    const terminalChildren = chain.children ?? [];

    return (
      <div key={startPath} className="flex flex-col">
        <SidebarRow
          sessionId={sessionId}
          cwd={cwd}
          remote={remote}
          sourceMode="changes"
          kind="dir"
          path={terminalPath}
          // 链首是这一行的稳定身份（链尾会随链变长而变），键盘落点按它记。
          rowKey={startPath}
          name={displayName}
          chainPrefix={chainPrefix}
          title={label}
          depth={depth}
          expanded={!isCollapsed}
          onToggle={() => toggleCollapse(startPath)}
          ariaLabel={
            isCollapsed
              ? t("chatContext.files.expandFolder", { name: label })
              : t("chatContext.files.collapseFolder", { name: label })
          }
          lead={
            <>
              {isCollapsed ? (
                <ChevronRight
                  className="size-3.5 shrink-0"
                  aria-hidden="true"
                />
              ) : (
                <ChevronDown className="size-3.5 shrink-0" aria-hidden="true" />
              )}
              <Folder className="size-3.5 shrink-0" aria-hidden="true" />
            </>
          }
          testId="changes-row"
          withMenu={false}
        />
        {/* 压缩后的行整体展开/收起；子项相对这一行只多缩进一级，无论链吸收
            了多少段（spec「压缩后的行作为一个整体展开 / 收起，其子项缩进
            只增加一级」）。 */}
        {!isCollapsed ? (
          <div className="flex flex-col">
            {terminalChildren.map((child) =>
              child.isDir
                ? renderDir(child.cursor as DirNode, terminalPath, depth + 1)
                : renderFile(
                    (child.cursor as Extract<FileTreeNode, { kind: "file" }>)
                      .entry,
                    depth + 1,
                  ),
            )}
          </div>
        ) : null}
      </div>
    );
  };

  const renderFile = (entry: FileEntry, depth: number) => (
    <SidebarRow
      key={entry.path}
      sessionId={sessionId}
      cwd={cwd}
      remote={remote}
      sourceMode="changes"
      kind="file"
      path={entry.path}
      name={basename(entry.path)}
      depth={depth}
      title={entry.path}
      onJumpToTurn={() => onJumpToTurn(entry.lastTurn)}
      lead={
        <>
          {/* 预留与目录 chevron 等宽的槽位，让同级目录名/文件名、图标列对齐 */}
          <span className="size-3.5 shrink-0" aria-hidden="true" />
          <FileTypeIcon path={entry.path} />
        </>
      }
      trailing={
        <span className="ml-auto flex shrink-0 items-center">
          <DiffBadge plus={entry.plus} minus={entry.minus} />
        </span>
      }
      testId="changes-row"
      rowData={{ "data-path": entry.path }}
    />
  );

  return (
    <SidebarList
      variant="tree"
      label={t("chatContext.files.treeAria")}
      className="flex flex-col gap-0.5 px-2 py-2.5"
    >
      {tree.map((node) =>
        node.kind === "dir"
          ? renderDir(node, "", 0)
          : renderFile(node.entry, 0),
      )}
    </SidebarList>
  );
}
