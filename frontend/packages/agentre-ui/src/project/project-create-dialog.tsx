/**
 * 新建项目 / 子项目，两端共用那一份（规格 2026-08-22 B 段，决策 9）。
 *
 * **路径不必填**：web 上建项目的人可能一台机器都没在线，挡住他等于把「只有
 * agentred 也能管理」堵在第一步。代价是这样建出来的项目在配好路径之前开不出对话
 * —— 所以表单里当场把这句代价说出来。**不在这里预告索引组头上那枚角标**：它自己
 * 可点、带 aria，教在这里只是让他记一个还没见过的东西。
 *
 * 本机路径与 git 探测是**宿主能力**，用可选 port 表达：挂了才有那一格，没挂就整格
 * 不出现 —— 不用 `isDesktop` 分支。「在别的机器上配路径」是同一条路子的另一个 port。
 *
 * **两次写，不是一次**：路径进不了建项目那次请求（它按「项目 × 机器」另存一处），
 * 所以是 create → setPath。两次之间那个「项目已经建出来了」的中间态由这里当场交代，
 * 不靠组头那枚角标兜底 —— 用户刚做完的选择不该静默丢掉。
 */
import * as React from "react";
import { FolderOpen, GitBranch, Loader2, Server, X } from "lucide-react";

import { useUiTranslation } from "../i18n";
import { Button } from "../ui/button";
import {
  DialogShell,
  DialogShellBody,
  DialogShellFooter,
  DialogShellHeader,
  DialogShellSubmit,
} from "../ui/dialog-shell";
import { Input } from "../ui/input";
import { DirectoryPicker } from "./directory-picker";
import { useFailureText } from "./failure-text";
import { ParentSelect } from "./parent-select";
import { ProjectIdentityFields } from "./project-identity-fields";
import type {
  PickerMachine,
  ProjectCreateDraft,
  ProjectCreateMachinesPort,
  ProjectCreatePorts,
  ProjectGitInfo,
} from "./ports";

export interface ProjectCreateDialogProps {
  open: boolean;
  onOpenChange(open: boolean): void;
  /** 父项目候选。`depth` 只影响缩进，不给就不缩。 */
  parentOptions: { id: string; name: string; depth?: number }[];
  /** 「新建子项目…」进来时预置的父项目。 */
  initialParentId?: string;
  /** 给了就在头部说清挂在哪儿。 */
  parentName?: string;
  ports: ProjectCreatePorts;
  onCreated(projectId: string): void;
}

/** git 探测的防抖：手打路径时每敲一下探一次，既慢又吵。 */
const PROBE_DEBOUNCE_MS = 300;

export function ProjectCreateDialog({
  open,
  onOpenChange,
  parentOptions,
  initialParentId = "",
  parentName,
  ports,
  onCreated,
}: ProjectCreateDialogProps) {
  const { t } = useUiTranslation();
  const [name, setName] = React.useState("");
  const [description, setDescription] = React.useState("");
  const [icon, setIcon] = React.useState("");
  const [color, setColor] = React.useState("");
  const [parentId, setParentId] = React.useState(initialParentId);
  const [localPath, setLocalPath] = React.useState("");
  const [git, setGit] = React.useState<ProjectGitInfo | null>(null);
  const [probing, setProbing] = React.useState(false);
  const [busy, setBusy] = React.useState(false);
  const [error, setError] = React.useState<string | null>(null);
  const [machines, setMachines] = React.useState<PickerMachine[]>([]);
  const [picked, setPicked] = React.useState<{
    machine: PickerMachine;
    path: string;
  } | null>(null);
  const [pickerOpen, setPickerOpen] = React.useState(false);
  /**
   * 项目已经建出来了、路径还没写成时留着它的 id。
   *
   * 有它就意味着**必须**在关窗前把 `onCreated` 发出去 —— 否则索引里凭空少一个刚
   * 建好的项目，要等下一次重取才冒出来。
   */
  const [createdId, setCreatedId] = React.useState<string | null>(null);
  const [pathError, setPathError] = React.useState<string | null>(null);
  /**
   * 同一件事的 ref 副本：渲染要 `createdId`，而**关窗那一刻**要的是此刻的真值。
   * 只读 state 的话，「重试成功 → 清掉 → 关窗」这一串里关窗读到的还是清之前那个，
   * 于是 `onCreated` 发两次。ref 只在事件回调里写，不在渲染期。
   */
  const pendingIdRef = React.useRef<string | null>(null);

  const canPickLocalPath = !!ports.pickLocalDirectory;
  const canProbeGit = !!ports.probeGitRepo;
  const failureText = useFailureText();

  /**
   * 机器清单只在弹窗开着时读一次。读不上来就当作「没得挑」——那一格整格不出现，
   * 比留一个点开是空的入口好。
   */
  const machinesPort = ports.machines;
  React.useEffect(() => {
    if (!open || !machinesPort) return;
    let cancelled = false;
    void machinesPort.list().then(
      (rows) => {
        if (!cancelled) setMachines(rows);
      },
      () => {
        if (!cancelled) setMachines([]);
      },
    );
    return () => {
      cancelled = true;
    };
  }, [open, machinesPort]);

  const canPickMachine = !!machinesPort && machines.length > 0;

  /**
   * 路径变了就重探，防抖 300ms。
   *
   * 纯视觉反馈，不影响能不能建 —— 探测在飞时按钮照常可按。
   */
  const probeGitRepo = ports.probeGitRepo;
  React.useEffect(() => {
    if (!probeGitRepo) return;
    const path = localPath.trim();
    if (!path) {
      setGit(null);
      return;
    }
    let cancelled = false;
    setProbing(true);
    const timer = setTimeout(() => {
      void probeGitRepo(path)
        .then((info) => {
          if (!cancelled) setGit(info);
        })
        .finally(() => {
          if (!cancelled) setProbing(false);
        });
    }, PROBE_DEBOUNCE_MS);
    return () => {
      cancelled = true;
      clearTimeout(timer);
    };
  }, [localPath, probeGitRepo]);

  // 状态重置放在「关闭」这一侧：下一次打开天然从初值开始，不在 effect 里同步 setState。
  function handleOpenChange(next: boolean) {
    if (!next) {
      // 项目已经建出来了就一定要说出去，哪怕他是直接把窗关掉的。
      const announce = pendingIdRef.current;
      pendingIdRef.current = null;
      setName("");
      setDescription("");
      setIcon("");
      setColor("");
      setParentId(initialParentId);
      setLocalPath("");
      setGit(null);
      setBusy(false);
      setError(null);
      setPicked(null);
      setPickerOpen(false);
      setCreatedId(null);
      setPathError(null);
      if (announce) onCreated(announce);
    }
    onOpenChange(next);
  }

  const trimmedName = name.trim();
  const trimmedPath = localPath.trim();
  // 名字是**唯一**必填的一格：路径不必填（决策 9），两端一套校验。
  const canSubmit = !!trimmedName && !busy;

  async function handleBrowse() {
    const picked = await ports.pickLocalDirectory?.();
    if (!picked) return;
    setLocalPath(picked);
    // 没填名字时把目录名当默认名 —— 十有八九就是它。
    setName(
      (current) => current || picked.split("/").filter(Boolean).pop() || "",
    );
  }

  /**
   * 第二次写：把路径落到那台机器上。成功才算走完，`onCreated` 在这里发。
   *
   * 失败时**不关窗**：项目确实建出来了，但用户刚挑的机器与目录会静默消失，而他并
   * 不知道该去哪儿补。所以留在窗里说清哪一半成了、哪一半没成，「重试」只重发这一步。
   */
  function writePath(projectId: string, port: ProjectCreateMachinesPort) {
    if (!picked) return;
    setBusy(true);
    setPathError(null);
    void port.setPath(projectId, picked.machine, picked.path).then(
      (outcome) => {
        setBusy(false);
        if (!outcome.ok) {
          pendingIdRef.current = projectId;
          setCreatedId(projectId);
          setPathError(failureText(outcome));
          return;
        }
        pendingIdRef.current = null;
        setCreatedId(null);
        onCreated(projectId);
        handleOpenChange(false);
      },
      (e: unknown) => {
        setBusy(false);
        pendingIdRef.current = projectId;
        setCreatedId(projectId);
        setPathError(String(e));
      },
    );
  }

  function submit() {
    if (!canSubmit) return;
    // 指针语义：**只送这次真的填了的键**，没填的不翻成空串送下去。
    const draft: ProjectCreateDraft = { name: trimmedName };
    if (description.trim()) draft.description = description.trim();
    if (icon.trim()) draft.icon = icon.trim();
    if (color) draft.color = color;
    if (parentId) draft.parentId = parentId;
    if (trimmedPath) draft.localPath = trimmedPath;

    setBusy(true);
    setError(null);
    void ports.create(draft).then(
      (outcome) => {
        // 建成之后还要接着写路径时**不放下 busy**：中间放一下会闪出一个「还能再
        // 点一次提交」的瞬间，而那一下会建出第二个项目。
        if (outcome.ok && picked && machinesPort) {
          writePath(outcome.id, machinesPort);
          return;
        }
        setBusy(false);
        if (!outcome.ok) {
          // 写失败时**不关窗、不清空**：用户填的东西还在，就地给出错误。
          setError(
            outcome.failure.message?.trim() ||
              t("projectSettings.create.failed"),
          );
          return;
        }
        onCreated(outcome.id);
        handleOpenChange(false);
      },
      (e: unknown) => {
        setBusy(false);
        setError(String(e));
      },
    );
  }

  /** 目录选择器：两个分支（表单 / 半成态）挂的是同一个，所以只写一份。 */
  const picker =
    pickerOpen && machinesPort ? (
      <DirectoryPicker
        open
        onOpenChange={(next) => {
          if (!next) setPickerOpen(false);
        }}
        fs={machinesPort.fs}
        machines={machines}
        initialMachineId={picked?.machine.id}
        initialPath={picked?.path || undefined}
        onPick={(machineId, path) => {
          setPickerOpen(false);
          const machine = machines.find((m) => m.id === machineId);
          if (machine) setPicked({ machine, path });
        }}
      />
    ) : null;

  /**
   * 建成了、路径没写成的那一半。
   *
   * **整窗换成它**，而不是继续摆着那张表单：项目已经建出来了，名字与图标那几格再
   * 让人改只是骗人。剩下的事只有两件 —— 换台机器再试一次，或者先这样。
   */
  if (createdId) {
    return (
      <>
        <DialogShell
          open={open}
          onOpenChange={handleOpenChange}
          size="md"
          busy={busy}
        >
          <DialogShellHeader
            title={t("projectSettings.create.title")}
            onClose={() => handleOpenChange(false)}
            busy={busy}
          />
          <DialogShellBody className="space-y-3">
            <p
              data-testid="project-create-half-done"
              className="rounded-md border border-border bg-secondary/40 px-3 py-2 text-xs text-muted-foreground"
            >
              <span className="text-foreground">
                {t("projectSettings.create.pathCreated", { name: trimmedName })}
              </span>{" "}
              {t("projectSettings.create.pathFailed", {
                machine: picked?.machine.name ?? "",
              })}{" "}
              {pathError}
            </p>
            <Button
              data-testid="project-create-machine-pick"
              type="button"
              variant="outline"
              className="w-full justify-start"
              onClick={() => setPickerOpen(true)}
            >
              <FolderOpen className="size-3.5" aria-hidden="true" />
              {picked
                ? `${picked.machine.name} · ${picked.path}`
                : t("projectSettings.create.pickMachine")}
            </Button>
          </DialogShellBody>
          <DialogShellFooter>
            <Button
              data-testid="project-create-keep-anyway"
              variant="ghost"
              onClick={() => handleOpenChange(false)}
            >
              {t("projectSettings.create.keepAnyway")}
            </Button>
            <DialogShellSubmit
              data-testid="project-create-retry-path"
              busy={busy}
              disabled={!picked || busy}
              onClick={() => {
                if (machinesPort) writePath(createdId, machinesPort);
              }}
            >
              {t("projectSettings.create.retryPath")}
            </DialogShellSubmit>
          </DialogShellFooter>
        </DialogShell>
        {picker}
      </>
    );
  }

  return (
    <>
      <DialogShell
        open={open}
        onOpenChange={handleOpenChange}
        size="md"
        busy={busy}
      >
        <DialogShellHeader
          title={
            parentName
              ? t("projectSettings.create.subtitleOf", { name: parentName })
              : t("projectSettings.create.title")
          }
          onClose={() => handleOpenChange(false)}
          busy={busy}
        />
        <DialogShellBody className="space-y-4">
          {/* 身份区与「项目设置」共用那一份 —— 两个弹窗此前各写一遍，于是分叉成了
            「新建用 IconPicker、设置要手打 icon key」。 */}
          <ProjectIdentityFields
            testIdPrefix="project-create"
            autoFocusName
            name={name}
            description={description}
            icon={icon}
            color={color}
            onNameChange={setName}
            onDescriptionChange={setDescription}
            onPickIcon={setIcon}
            onPickColor={setColor}
          />

          {canPickLocalPath ? (
            <div>
              <p className="text-xs font-medium text-foreground">
                {t("projectSettings.create.localPath")}
              </p>
              <div className="mt-1 flex items-stretch gap-2">
                <Input
                  data-testid="project-create-path"
                  value={localPath}
                  onChange={(e) => setLocalPath(e.target.value)}
                  className="flex-1 font-mono text-xs"
                />
                <Button
                  data-testid="project-create-browse"
                  type="button"
                  variant="outline"
                  onClick={() => void handleBrowse()}
                >
                  <FolderOpen className="size-3.5" aria-hidden="true" />
                  {t("projectSettings.create.browse")}
                </Button>
              </div>
              {canProbeGit && trimmedPath ? (
                <GitNote info={git} probing={probing} />
              ) : null}
            </div>
          ) : null}

          {parentOptions.length > 0 ? (
            <ParentSelect
              data-testid="project-create-parent"
              value={parentId}
              options={parentOptions}
              onChange={setParentId}
            />
          ) : null}

          {/* 一行只读摘要 + 「选择…」：挑机器那一栏本来就长在目录选择器里，
            在这里再画一个下拉等于把同一件事画两遍。 */}
          {canPickMachine ? (
            <div>
              <p className="text-xs font-medium text-foreground">
                {t("projectSettings.create.machine")}
              </p>
              <div className="mt-1 flex items-stretch gap-2">
                {picked ? (
                  <div
                    data-testid="project-create-machine"
                    className="flex min-w-0 flex-1 items-center gap-2 rounded-md border border-border bg-secondary/40 px-2.5 py-1.5 text-xs"
                  >
                    <Server
                      className="size-3.5 shrink-0 text-muted-foreground"
                      aria-hidden="true"
                    />
                    <span className="shrink-0 font-medium text-foreground">
                      {picked.machine.name}
                    </span>
                    <span className="truncate font-mono text-2xs text-muted-foreground">
                      {picked.path}
                    </span>
                    <button
                      type="button"
                      data-testid="project-create-machine-clear"
                      aria-label={t("projectSettings.create.clearMachine")}
                      className="ml-auto shrink-0 rounded-sm p-0.5 text-muted-foreground hover:bg-accent hover:text-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring/50"
                      onClick={() => setPicked(null)}
                    >
                      <X className="size-3" aria-hidden="true" />
                    </button>
                  </div>
                ) : null}
                <Button
                  data-testid="project-create-machine-pick"
                  type="button"
                  variant="outline"
                  className={picked ? undefined : "flex-1 justify-start"}
                  onClick={() => setPickerOpen(true)}
                >
                  <FolderOpen className="size-3.5" aria-hidden="true" />
                  {picked
                    ? t("projectSettings.create.browse")
                    : t("projectSettings.create.pickMachine")}
                </Button>
              </div>
            </div>
          ) : null}

          {/* 代价当场说出来，而不是等他开不出对话时才发现。**只说代价，不预告角标**：
            角标自己可点、带 aria，教在这里等于让他记一个还没见过的东西。
            按宿主有没有路径这一格分两版 —— 差别只在下一步该往哪走。 */}
          {!trimmedPath && !picked ? (
            <p
              data-testid="project-create-path-note"
              className="rounded-md border border-border bg-secondary/40 px-3 py-2 text-2xs text-muted-foreground"
            >
              {canPickLocalPath
                ? t("projectSettings.create.pathNote")
                : t("projectSettings.create.pathNoteRemote")}
            </p>
          ) : null}
        </DialogShellBody>
        <div data-testid="project-create-footer">
          <DialogShellFooter error={error}>
            <Button variant="ghost" onClick={() => handleOpenChange(false)}>
              {t("common.cancel")}
            </Button>
            <DialogShellSubmit
              data-testid="project-create-submit"
              busy={busy}
              disabled={!canSubmit}
              onClick={submit}
            >
              {t("projectSettings.create.submit")}
            </DialogShellSubmit>
          </DialogShellFooter>
        </div>
      </DialogShell>

      {picker}
    </>
  );
}

/**
 * 探测结果就地标出来。
 *
 * 三种处境各有各的样子：在探 / 是仓库 / 不是仓库。**「不是仓库」也要说出来** ——
 * 留白会让人以为还在探，而这一句同时告诉他「没关系」。探不出来（port 回 null）时
 * 什么都不标：编一个「不是仓库」比不说更糟。
 */
function GitNote({
  info,
  probing,
}: {
  info: ProjectGitInfo | null;
  probing: boolean;
}) {
  const { t } = useUiTranslation();
  if (probing) {
    return (
      <div className="mt-2 flex items-center gap-1.5 text-2xs text-muted-foreground">
        <Loader2 className="size-3 animate-spin" aria-hidden="true" />
        {t("projectSettings.create.detectingGit")}
      </div>
    );
  }
  if (!info) return null;
  if (!info.isGitRepo) {
    return (
      <p
        data-testid="project-create-git"
        className="mt-2 text-2xs text-muted-foreground"
      >
        {t("projectSettings.create.noGit")}
      </p>
    );
  }
  return (
    <div
      data-testid="project-create-git"
      className="mt-2 flex items-start gap-2 rounded-md border border-status-running/30 bg-status-running-bg/50 px-2.5 py-1.5 text-2xs"
    >
      <GitBranch
        className="mt-0.5 size-3 text-status-running"
        aria-hidden="true"
      />
      <div className="flex min-w-0 flex-1 flex-col gap-0.5">
        <span className="font-medium text-foreground">
          {t("projectSettings.create.gitDetected", {
            branch: info.branch || t("projectSettings.create.unknownBranch"),
          })}
        </span>
        <span className="truncate font-mono text-2xs text-muted-foreground">
          {t("projectSettings.create.gitOrigin", {
            origin: info.origin || t("projectSettings.create.noOrigin"),
          })}
        </span>
      </div>
    </div>
  );
}
