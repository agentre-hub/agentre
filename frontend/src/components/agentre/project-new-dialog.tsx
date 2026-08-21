import * as React from "react";
import { Check, FolderOpen, GitBranch, Loader2 } from "lucide-react";
import { useTranslation } from "react-i18next";

import { Button } from "@/components/ui/button";
import {
  Dialog,
  DialogBody,
  DialogContent,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import { Textarea } from "@/components/ui/textarea";
import { cn } from "@/lib/utils";

import { IconPicker } from "./icon-picker";
import {
  ProjectCreate,
  ProjectDetectGitRepo,
  SelectDirectory,
} from "../../../wailsjs/go/app/App";
import type { app } from "../../../wailsjs/go/models";
import {
  agentColorClassNames,
  agentColorOrder,
  type AgentColor,
} from "./types";

type ProjectTreeNode = app.ProjectTreeNode;
type ProjectGitRepoInfo = app.ProjectGitRepoInfo;

export type ProjectNewDialogProps = {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  tree: ProjectTreeNode[];
  /** 用户点 + 时如果当前选中某个项目，可用作默认父项目 ID。 */
  initialParentID?: number;
  /** 创建成功时回调；调用方触发 refresh + 选中新项目。 */
  onCreated: (projectID: number) => void;
};

type FormState = {
  path: string;
  name: string;
  parentID: number;
  icon: string;
  color: AgentColor;
  description: string;
};

const initialForm = (parentID: number): FormState => ({
  path: "",
  name: "",
  parentID,
  icon: "folder",
  color: "agent-1",
  description: "",
});

// flattenTree 把项目树拍平成 [{id, name, depth}] 供父项目下拉用，depth 决定缩进。
function flattenTree(
  nodes: ProjectTreeNode[],
  depth = 0,
): { id: number; name: string; depth: number }[] {
  const out: { id: number; name: string; depth: number }[] = [];
  for (const n of nodes) {
    if (!n.project) continue;
    out.push({ id: n.project.id, name: n.project.name, depth });
    if (n.children) out.push(...flattenTree(n.children, depth + 1));
  }
  return out;
}

function ProjectNewDialog({
  open,
  onOpenChange,
  tree,
  initialParentID = 0,
  onCreated,
}: ProjectNewDialogProps) {
  const { t } = useTranslation();
  const [form, setForm] = React.useState<FormState>(() =>
    initialForm(initialParentID),
  );
  const [git, setGit] = React.useState<ProjectGitRepoInfo | null>(null);
  const [detecting, setDetecting] = React.useState(false);
  const [submitError, setSubmitError] = React.useState<string | null>(null);
  const [submitting, setSubmitting] = React.useState(false);

  // 每次重开弹窗 / 切父项目时重置表单 —— 用户期望「新建」是一次全新流程。
  React.useEffect(() => {
    if (open) {
      setForm(initialForm(initialParentID));
      setGit(null);
      setSubmitError(null);
    }
  }, [open, initialParentID]);

  // path 变化后异步探测 git 仓库 —— 防抖 300ms，纯视觉反馈，不影响行为。
  React.useEffect(() => {
    if (!form.path.trim()) {
      setGit(null);
      return;
    }
    const path = form.path;
    let cancelled = false;
    setDetecting(true);
    const timer = window.setTimeout(() => {
      void ProjectDetectGitRepo(path)
        .then((info) => {
          if (cancelled) return;
          setGit(info);
        })
        .finally(() => {
          if (!cancelled) setDetecting(false);
        });
    }, 300);
    return () => {
      cancelled = true;
      window.clearTimeout(timer);
    };
  }, [form.path]);

  const handleBrowse = async () => {
    try {
      const picked = await SelectDirectory(t("projectNew.selectDirectory"));
      if (picked) {
        setForm((f) => ({
          ...f,
          path: picked,
          // 没填名字时把目录名当默认名
          name: f.name || picked.split("/").pop() || "",
        }));
      }
    } catch {
      // 用户取消 —— 静默
    }
  };

  const canSubmit =
    form.path.trim().length > 0 && form.name.trim().length > 0 && !submitting;

  const handleSubmit = async () => {
    setSubmitError(null);
    setSubmitting(true);
    try {
      const created = await ProjectCreate({
        parentID: form.parentID,
        name: form.name.trim(),
        icon: form.icon,
        color: form.color,
        description: form.description.trim(),
        path: form.path.trim(),
        initialAgentIDs: [],
      });
      onCreated(created.id);
      onOpenChange(false);
    } catch (err) {
      setSubmitError(String(err));
    } finally {
      setSubmitting(false);
    }
  };

  const parentOptions = React.useMemo(() => flattenTree(tree), [tree]);

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="max-w-[540px]">
        <DialogHeader>
          <DialogTitle>{t("projects.actions.newProject")}</DialogTitle>
        </DialogHeader>
        <DialogBody className="flex flex-col gap-3.5">
          {/* 路径 */}
          <Field label={t("projectNew.localPath")} required>
            <div className="flex items-stretch gap-2">
              <Input
                data-testid="project-new-path"
                value={form.path}
                onChange={(e) =>
                  setForm((f) => ({ ...f, path: e.target.value }))
                }
                placeholder="/Users/you/Code/your-repo"
                className="h-9 flex-1 font-mono text-xs"
              />
              <Button
                type="button"
                variant="outline"
                size="sm"
                className="h-9 gap-1 px-3 text-xs"
                onClick={() => void handleBrowse()}
              >
                <FolderOpen className="size-3.5" aria-hidden="true" />
                {t("projectNew.browse")}
              </Button>
            </div>
            {detecting ? (
              <div className="mt-2 flex items-center gap-1.5 text-2xs text-muted-foreground">
                <Loader2 className="size-3 animate-spin" aria-hidden="true" />
                {t("projectNew.detectingGit")}
              </div>
            ) : git?.isGitRepo ? (
              <GitDetectedCallout info={git} />
            ) : form.path.trim() ? (
              <div className="mt-2 text-2xs text-muted-foreground">
                {t("projectNew.noGit")}
              </div>
            ) : null}
          </Field>

          {/* 名字 + 父项目 */}
          <div className="grid grid-cols-2 gap-3">
            <Field label={t("projectSettings.basic.name")} required>
              <Input
                data-testid="project-new-name"
                value={form.name}
                onChange={(e) =>
                  setForm((f) => ({ ...f, name: e.target.value }))
                }
                placeholder="Agentre"
                className="h-9 text-xs"
              />
            </Field>
            <Field label={t("projectNew.parentProject")}>
              <Select
                value={String(form.parentID)}
                onValueChange={(v) =>
                  setForm((f) => ({ ...f, parentID: Number(v) }))
                }
              >
                <SelectTrigger className="h-9 text-xs">
                  <SelectValue placeholder={t("projectNew.none")} />
                </SelectTrigger>
                <SelectContent>
                  <SelectItem value="0">{t("projectNew.topLevel")}</SelectItem>
                  {parentOptions.map((opt) => (
                    <SelectItem key={opt.id} value={String(opt.id)}>
                      {"  ".repeat(opt.depth)}
                      {opt.name}
                    </SelectItem>
                  ))}
                </SelectContent>
              </Select>
            </Field>
          </div>

          {/* 图标 + 颜色 */}
          <div className="grid grid-cols-2 gap-3">
            <Field label={t("org.department.icon")}>
              <IconPicker
                value={form.icon}
                onChange={(icon) => setForm((f) => ({ ...f, icon }))}
                accentColor={form.color}
              />
            </Field>
            <Field label={t("org.department.themeColor")}>
              <div className="grid grid-cols-8 gap-1.5">
                {agentColorOrder.map((c) => (
                  <button
                    key={c}
                    type="button"
                    aria-label={c}
                    onClick={() => setForm((f) => ({ ...f, color: c }))}
                    className={cn(
                      "inline-flex size-6 items-center justify-center rounded-full transition-transform hover:scale-110",
                      agentColorClassNames[c],
                      form.color === c &&
                        "outline outline-2 outline-offset-2 outline-foreground",
                    )}
                  >
                    {form.color === c ? (
                      <Check
                        className="size-3 text-agent-foreground"
                        aria-hidden="true"
                      />
                    ) : null}
                  </button>
                ))}
              </div>
            </Field>
          </div>

          {/* 描述 */}
          <Field label={t("projectSettings.basic.description")}>
            <Textarea
              value={form.description}
              onChange={(e) =>
                setForm((f) => ({ ...f, description: e.target.value }))
              }
              placeholder={t("projectNew.descriptionPlaceholder")}
              className="min-h-[60px] text-xs"
            />
          </Field>

          {submitError ? (
            <div className="rounded-md border border-destructive bg-destructive-soft px-3 py-2 text-2xs text-destructive">
              {submitError}
            </div>
          ) : null}
        </DialogBody>
        <DialogFooter>
          <Button
            type="button"
            variant="ghost"
            onClick={() => onOpenChange(false)}
            disabled={submitting}
          >
            {t("common.cancel")}
          </Button>
          <Button
            type="button"
            data-testid="project-new-submit"
            disabled={!canSubmit}
            onClick={() => void handleSubmit()}
          >
            {submitting ? (
              <Loader2 className="size-3.5 animate-spin" aria-hidden="true" />
            ) : null}
            {t("projectNew.create")}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}

function Field({
  label,
  required,
  children,
}: {
  label: string;
  required?: boolean;
  children: React.ReactNode;
}) {
  return (
    <label className="flex flex-col gap-1.5 text-xs">
      <span className="font-medium text-foreground">
        {label}
        {required ? <span className="ml-0.5 text-destructive">*</span> : null}
      </span>
      {children}
    </label>
  );
}

function GitDetectedCallout({ info }: { info: ProjectGitRepoInfo }) {
  const { t } = useTranslation();
  return (
    <div className="mt-2 flex items-start gap-2 rounded-md border border-status-running/30 bg-status-running-bg/50 px-2.5 py-1.5 text-2xs">
      <GitBranch
        className="mt-0.5 size-3 text-status-running"
        aria-hidden="true"
      />
      <div className="flex min-w-0 flex-1 flex-col gap-0.5">
        <span className="font-medium text-foreground">
          {t("projectNew.gitDetected")}
        </span>
        <span className="truncate font-mono text-2xs text-muted-foreground">
          {t("projectNew.gitSummary", {
            branch: info.currentBranch || t("projectNew.unknown"),
            origin: info.origin || t("projectNew.noOrigin"),
          })}
        </span>
      </div>
    </div>
  );
}

export { ProjectNewDialog };
