/**
 * 新建项目 / 子项目，两端共用那一份（规格 2026-08-22 B 段，决策 9）。
 *
 * **路径不必填**：web 上建项目的人可能一台机器都没在线，挡住他等于把「只有
 * agentred 也能管理」堵在第一步。代价是这样建出来的项目在配好路径之前开不出对话
 * —— 所以表单里当场把这句话说出来，索引组头上还挂一枚可点的「未配路径」角标。
 *
 * 本机路径与 git 探测是**宿主能力**，用可选 port 表达：挂了才有那一格，没挂就整格
 * 不出现 —— 不用 `isDesktop` 分支。
 */
import * as React from "react";
import { FolderOpen, GitBranch, Loader2 } from "lucide-react";

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
import { ParentSelect } from "./parent-select";
import { ProjectIdentityFields } from "./project-identity-fields";
import type {
  ProjectCreateDraft,
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

  const canPickLocalPath = !!ports.pickLocalDirectory;
  const canProbeGit = !!ports.probeGitRepo;

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
      setName("");
      setDescription("");
      setIcon("");
      setColor("");
      setParentId(initialParentId);
      setLocalPath("");
      setGit(null);
      setBusy(false);
      setError(null);
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

  return (
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

        {/* 代价当场说出来，而不是等他开不出对话时才发现。 */}
        {!trimmedPath ? (
          <p
            data-testid="project-create-path-note"
            className="rounded-md border border-border bg-secondary/40 px-3 py-2 text-2xs text-muted-foreground"
          >
            {t("projectSettings.create.pathNote")}
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
