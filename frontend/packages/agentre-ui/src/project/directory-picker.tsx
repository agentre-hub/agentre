import * as React from "react";
import {
  AlertTriangle,
  ChevronRight,
  File as FileIcon,
  Folder,
  FolderPlus,
  GitBranch,
  Link as LinkIcon,
  Loader2,
  Monitor,
  RotateCw,
  Search,
  Server,
} from "lucide-react";

import { useUiTranslation } from "../i18n";
import { cn } from "../lib/utils";
import { Button } from "../ui/button";
import { Input } from "../ui/input";
import {
  DialogShell,
  DialogShellFooter,
  DialogShellHeader,
} from "../ui/dialog-shell";
import {
  breadcrumbOf,
  isValidFolderName,
  joinPath,
  sortEntries,
  type DirectoryEntry,
  type DirectoryFailure,
  type ListDirResult,
  type PickerMachine,
  type ProjectFsPort,
} from "./ports";

/**
 * 目录选择器，两端共用那一份（规格 2026-08-22 D 段，决策 11）。
 *
 * 点着选，而不是背下绝对路径再敲进去——敲错一个字母，不选择器的话要等到派发失败
 * 才发现。
 *
 * 合之前两端不是同一个形状：桌面端是单机文件浏览器（deviceID 传入、列文件、
 * filter / 隐藏项 / symlink），本仓是多机目录选择器（自己列机器、只列目录、git 标记）。
 * 合的时候**取并集**，两端各自获得对方已经做对的那半边。
 *
 * 它对上层只交出 `onPick(机器, 路径)`：写在哪、写不写得成，是调用方的事。
 */
export interface DirectoryPickerProps {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  fs: ProjectFsPort;
  machines: PickerMachine[];
  /** 打开时选中哪一台。调用方点的是「机器与路径」表里某一行，机器已经定了。 */
  initialMachineId?: string;
  initialPath?: string;
  onPick: (machineId: string, path: string) => void;
}

function MachineIcon({ kind }: { kind: PickerMachine["kind"] }) {
  const Icon = kind === "desktop" ? Monitor : Server;
  return <Icon className="size-3.5 shrink-0" aria-hidden="true" />;
}

export function DirectoryPicker({
  open,
  onOpenChange,
  fs,
  machines,
  initialMachineId,
  initialPath,
  onPick,
}: DirectoryPickerProps) {
  const { t } = useUiTranslation();

  const [machineId, setMachineId] = React.useState(
    initialMachineId ?? machines.find((m) => m.online)?.id ?? "",
  );
  const [path, setPath] = React.useState(initialPath ?? "");
  const [listing, setListing] = React.useState<ListDirResult | null>(null);
  const [failure, setFailure] = React.useState<DirectoryFailure | null>(null);
  const [nonce, setNonce] = React.useState(0);
  const [query, setQuery] = React.useState("");
  const [showHidden, setShowHidden] = React.useState(false);
  const [creating, setCreating] = React.useState(false);
  const [newName, setNewName] = React.useState("");
  const [createError, setCreateError] = React.useState<string | null>(null);
  const [createBusy, setCreateBusy] = React.useState(false);

  const machineName =
    machines.find((m) => m.id === machineId)?.name ?? machineId;

  React.useEffect(() => {
    if (!open || !machineId) return;
    let alive = true;
    void fs.listDir(machineId, path).then((outcome) => {
      if (!alive) return;
      if (!outcome.ok) {
        // **失败时不清掉 listing**：已经浏览到的位置不该因为一次掉线丢掉。
        setFailure(outcome.failure);
        return;
      }
      setListing(outcome.result);
      setFailure(null);
      // 那台机器解析后的绝对路径回来了（传空串即它的 $HOME），以它为准。
      if (outcome.result.path && outcome.result.path !== path) {
        setPath(outcome.result.path);
      }
    });
    return () => {
      alive = false;
    };
  }, [open, fs, machineId, path, nonce]);

  /**
   * 「在读」是**推导**出来的，不是在 effect 里同步置位的 state：手里这份清单不是当前
   * 这条路径的、又还没失败，就说明请求在飞。effect 里同步 setState 会引来一轮级联渲染。
   */
  const loading = !!machineId && !failure && listing?.path !== path;

  function go(next: string) {
    setPath(next);
    setFailure(null);
    setCreating(false);
    setCreateError(null);
    setQuery("");
  }

  function switchMachine(next: string) {
    setMachineId(next);
    // 换机器等于换一台机器的文件系统，路径不通用：回到它的 $HOME。
    setListing(null);
    setFailure(null);
    setPath("");
    setQuery("");
    setCreating(false);
  }

  async function create() {
    const name = newName.trim();
    if (!isValidFolderName(name)) {
      setCreateError(t("directoryPicker.failure.invalidName"));
      return;
    }
    setCreateBusy(true);
    setCreateError(null);
    const outcome = await fs.mkdir(machineId, path, name);
    setCreateBusy(false);
    if (!outcome.ok) {
      setCreateError(failureText(outcome.failure));
      return;
    }
    setCreating(false);
    setNewName("");
    setNonce((n) => n + 1);
  }

  function failureText(f: DirectoryFailure): string {
    const key =
      f.kind === "denied" ||
      f.kind === "notFound" ||
      f.kind === "notDir" ||
      f.kind === "disconnected" ||
      f.kind === "exists" ||
      f.kind === "invalidName" ||
      f.kind === "refused"
        ? f.kind
        : "unknown";
    return t(`directoryPicker.failure.${key}`, {
      machine: machineName,
      message: f.message,
    });
  }

  const all = sortEntries(listing?.entries ?? []);
  const rows = all
    .filter((e) => showHidden || !e.name.startsWith("."))
    .filter(
      (e) => !query || e.name.toLowerCase().includes(query.toLowerCase()),
    );
  const crumbs = breadcrumbOf(path);

  return (
    <DialogShell open={open} onOpenChange={onOpenChange} size="lg">
      <DialogShellHeader
        title={t("directoryPicker.title", { machine: machineName })}
        onClose={() => onOpenChange(false)}
      />

      <div className="flex min-h-0 flex-1">
        {/* 机器那一栏：打开时机器已由调用方定了，这一栏是为了**不关窗就能换一台**——
            同一个项目常要在两三台机器上各配一次。

            只有一台时整栏不画：一个只有一个选项的选择器不是选择器，只是占掉 172px。
            机器是哪一台仍然说得出来——标题上点着名。 */}
        {machines.length > 1 ? (
          <div className="flex w-[172px] shrink-0 flex-col border-r border-border">
            <p className="shrink-0 px-3 py-2 text-3xs font-medium uppercase tracking-wider text-muted-foreground">
              {t("directoryPicker.machines")}
            </p>
            <ul
              data-testid="directory-picker-machines"
              className="min-h-0 flex-1 overflow-y-auto px-1.5 pb-2"
            >
              {machines.map((m) => (
                <li key={m.id}>
                  <button
                    type="button"
                    // 离线的**留在列表里**并禁用：隐藏会让人以为那台机器没配对。
                    disabled={!m.online}
                    onClick={() => switchMachine(m.id)}
                    title={m.online ? undefined : t("directoryPicker.offline")}
                    className={cn(
                      "mb-0.5 flex w-full items-center gap-1.5 rounded-md px-2 py-1.5 text-left text-xs",
                      m.id === machineId
                        ? "bg-primary-soft text-primary-text"
                        : "hover:bg-accent",
                      !m.online &&
                        "cursor-not-allowed opacity-55 hover:bg-transparent",
                    )}
                  >
                    <span
                      aria-hidden="true"
                      className={cn(
                        "inline-block size-1.5 shrink-0 rounded-full",
                        m.online ? "bg-status-running" : "bg-border-strong",
                      )}
                    />
                    <MachineIcon kind={m.kind} />
                    <span className="min-w-0 flex-1 truncate">{m.name}</span>
                  </button>
                </li>
              ))}
            </ul>
          </div>
        ) : null}

        <div className="flex min-w-0 flex-1 flex-col">
          <div className="flex shrink-0 items-center gap-2 border-b border-border px-4 py-2">
            <div
              data-testid="directory-picker-breadcrumb"
              className="flex min-w-0 flex-1 items-center gap-0.5 overflow-x-auto text-2xs text-muted-foreground"
            >
              {crumbs.map((c, i) => (
                <React.Fragment key={c.path}>
                  {i > 0 ? (
                    <ChevronRight
                      className="size-3 shrink-0"
                      aria-hidden="true"
                    />
                  ) : null}
                  <button
                    type="button"
                    data-slot="crumb"
                    onClick={() => go(c.path)}
                    className={cn(
                      "shrink-0 rounded-sm px-1 py-0.5 hover:bg-accent",
                      i === crumbs.length - 1 && "font-medium text-foreground",
                    )}
                  >
                    {c.name}
                  </button>
                </React.Fragment>
              ))}
            </div>
            {listing?.isGitRepo ? (
              <span
                data-testid="directory-picker-git"
                className="inline-flex shrink-0 items-center gap-1 rounded-sm bg-secondary px-1.5 py-0.5 text-3xs text-muted-foreground"
              >
                <GitBranch className="size-2.5" aria-hidden="true" />
                {t("directoryPicker.git")}
              </span>
            ) : null}
            <div className="relative w-[160px] shrink-0">
              <Search
                className="pointer-events-none absolute left-2 top-1/2 size-3 -translate-y-1/2 text-muted-foreground"
                aria-hidden="true"
              />
              <Input
                value={query}
                onChange={(e) => setQuery(e.target.value)}
                placeholder={t("directoryPicker.filterPlaceholder")}
                className="h-7 pl-6 text-2xs"
              />
            </div>
            <label className="flex shrink-0 cursor-pointer items-center gap-1 text-2xs text-muted-foreground">
              <input
                type="checkbox"
                checked={showHidden}
                onChange={(e) => setShowHidden(e.target.checked)}
                aria-label={t("directoryPicker.showHidden")}
              />
              {t("directoryPicker.showHidden")}
            </label>
            <Button
              variant="ghost"
              size="icon-sm"
              aria-label={t("directoryPicker.reload")}
              onClick={() => setNonce((n) => n + 1)}
            >
              <RotateCw className="size-3.5" aria-hidden="true" />
            </Button>
          </div>

          <div className="min-h-0 flex-1 overflow-y-auto px-3 py-2">
            {failure ? (
              <div
                data-testid="directory-picker-failure"
                className="flex items-center gap-2 rounded-md border border-destructive/40 bg-destructive-soft/40 px-3 py-2 text-xs text-destructive"
              >
                <AlertTriangle
                  className="size-3.5 shrink-0"
                  aria-hidden="true"
                />
                <span className="min-w-0 flex-1">{failureText(failure)}</span>
                <Button
                  variant="outline"
                  size="xs"
                  onClick={() => {
                    setFailure(null);
                    setNonce((n) => n + 1);
                  }}
                >
                  {t("directoryPicker.retry")}
                </Button>
              </div>
            ) : loading ? (
              <div className="flex items-center gap-2 px-2 py-2 text-xs text-muted-foreground">
                <Loader2 className="size-3.5 animate-spin" aria-hidden="true" />
                {t("directoryPicker.loading", { machine: machineName })}
              </div>
            ) : (
              <>
                {listing?.truncated ? (
                  <div
                    data-testid="directory-picker-truncated"
                    className="mb-1.5 flex items-center gap-2 rounded-md border border-border px-3 py-2 text-xs text-muted-foreground"
                  >
                    <AlertTriangle
                      className="size-3.5 shrink-0"
                      aria-hidden="true"
                    />
                    {t("directoryPicker.truncated")}
                  </div>
                ) : null}
                {all.length === 0 ? (
                  // 空目录不是失败：它是一条能走的路，就选它，或者在这里新建一个。
                  <p
                    data-testid="directory-picker-empty"
                    className="px-2 py-2 text-xs text-muted-foreground"
                  >
                    {t("directoryPicker.empty")}
                  </p>
                ) : (
                  <ul
                    data-testid="directory-picker-listing"
                    className="space-y-0.5"
                  >
                    {rows.map((e) => (
                      <EntryRow
                        key={e.name}
                        entry={e}
                        onEnter={go}
                        path={path}
                      />
                    ))}
                    {rows.length === 0 ? (
                      <li
                        data-testid="directory-picker-no-match"
                        className="px-2 py-2 text-xs text-muted-foreground"
                      >
                        {t("directoryPicker.noMatch", { query })}
                      </li>
                    ) : null}
                  </ul>
                )}
              </>
            )}

            {creating ? (
              <div className="mt-2 rounded-md border border-ring px-2 py-1.5">
                <div className="flex items-center gap-2">
                  <FolderPlus
                    className="size-3.5 shrink-0 text-muted-foreground"
                    aria-hidden="true"
                  />
                  <Input
                    autoFocus
                    value={newName}
                    onChange={(e) => setNewName(e.target.value)}
                    placeholder={t("directoryPicker.newFolderName")}
                    className="h-7 text-2xs"
                  />
                  <Button
                    size="xs"
                    disabled={createBusy}
                    onClick={() => void create()}
                  >
                    {t("directoryPicker.create")}
                  </Button>
                  <Button
                    variant="ghost"
                    size="xs"
                    onClick={() => {
                      setCreating(false);
                      setCreateError(null);
                    }}
                  >
                    {t("directoryPicker.cancel")}
                  </Button>
                </div>
                {createError ? (
                  <p
                    data-testid="directory-picker-create-error"
                    className="mt-1 text-2xs text-destructive"
                  >
                    {createError}
                  </p>
                ) : null}
              </div>
            ) : null}
          </div>
        </div>
      </div>

      <DialogShellFooter
        left={
          <span className="block truncate font-mono text-2xs text-muted-foreground">
            {path || "—"}
          </span>
        }
      >
        <Button variant="outline" size="sm" onClick={() => setCreating(true)}>
          <FolderPlus className="size-3.5" aria-hidden="true" />
          {t("directoryPicker.newFolder")}
        </Button>
        <Button size="sm" onClick={() => onPick(machineId, path)}>
          {t("directoryPicker.chooseHere")}
        </Button>
      </DialogShellFooter>
    </DialogShell>
  );
}

function EntryRow({
  entry,
  path,
  onEnter,
}: {
  entry: DirectoryEntry;
  path: string;
  onEnter: (next: string) => void;
}) {
  const { t } = useUiTranslation();
  return (
    <li>
      <button
        type="button"
        // 文件**可见但不可选**：它给的是「这个目录是不是我要的那个」的上下文。
        disabled={!entry.isDir}
        onClick={() => onEnter(joinPath(path, entry.name))}
        className={cn(
          "flex w-full items-center gap-2 rounded-sm px-2 py-1.5 text-left text-xs",
          entry.isDir ? "hover:bg-accent" : "cursor-default opacity-55",
        )}
      >
        {entry.isDir ? (
          <Folder
            className="size-3.5 shrink-0 text-muted-foreground"
            aria-hidden="true"
          />
        ) : (
          <FileIcon
            className="size-3.5 shrink-0 text-muted-foreground"
            aria-hidden="true"
          />
        )}
        <span className="min-w-0 flex-1 truncate">{entry.name}</span>
        {entry.symlink ? (
          <LinkIcon
            data-testid={`directory-picker-symlink-${entry.name}`}
            className="size-3 shrink-0 text-muted-foreground"
            aria-label={t("directoryPicker.symlink")}
          />
        ) : null}
      </button>
    </li>
  );
}
