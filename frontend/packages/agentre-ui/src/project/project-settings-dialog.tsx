/**
 * 项目设置，两端共用那一份（规格 2026-08-22 B 段，决策 2/3/4/7/14）。
 *
 * **一屏三节**（基本 / 成员 / 机器与路径），不是标签页。组头的「成员…」「机器与
 * 路径…」与「未配置」角标打开的就是这个弹窗，`focus` 让视口直落那一节 —— 打开在
 * 顶部等于没有入口，那两节都在折叠线以下。
 *
 * **即时保存**：字段 `onBlur` 静默提交，保存态在头部右侧，脚部只有「完成」。合之前
 * 桌面端那一个弹窗里同时存在两种保存语义（基本页显式「保存」+ dirty 判定，成员与
 * 位置的增删立即写），用户按下一个按钮之前得先判断这一节属于哪一种。
 *
 * **路径只有一个位置**：第三节那张表里的一行。本机在桌面端宿主上是这张表里带「本机」
 * 角标的一行，不是「基本」里的一格 —— 同一个概念摆两处迟早说成两个样子，且 web 宿主
 * 根本没有「本机」，那一格没东西可放。
 *
 * 后端形状是真正的宿主差异，由 `ProjectSettingsPorts` 吃掉：桌面端是数字 id 经
 * wailsjs，agentre-server 是字符串 syncId 经 REST，包两个都不认识。
 */
import * as React from "react";
import { Loader2, MonitorSmartphone, Plus, Server, X } from "lucide-react";

import { useUiTranslation } from "../i18n";
import { agentColorOrder, tokenToCssColor } from "../lib/agent-color";
import { cn } from "../lib/utils";
import { AgentAvatar } from "../ui/agent-avatar";
import { Button } from "../ui/button";
import {
  DialogShell,
  DialogShellBody,
  DialogShellFooter,
  DialogShellHeader,
  type DialogShellSaveState,
} from "../ui/dialog-shell";
import { Input } from "../ui/input";
import { Textarea } from "../ui/textarea";
import { DirectoryPicker } from "./directory-picker";
import type {
  PickerMachine,
  ProjectCandidateView,
  ProjectFieldValues,
  ProjectMachineView,
  ProjectMemberView,
  ProjectSettingsPorts,
  ProjectSettingsView,
  ProjectWriteOutcome,
} from "./ports";

export interface ProjectSettingsDialogProps {
  open: boolean;
  onOpenChange(open: boolean): void;
  project: ProjectSettingsView;
  /** 父项目下拉的候选；包会自己把「它自己」剔掉（指向自己会造出一个走不完的环）。 */
  parentOptions: { id: string; name: string }[];
  ports: ProjectSettingsPorts;
  /** 直落到哪一节。 */
  focus?: "members" | "paths";
  /**
   * 宿主自己的图标选择器。
   *
   * 把 icon key 换成图标的那张注册表是宿主的产品决定（桌面端有 383 行的
   * icon-registry，web 宿主用的是它自己的 OrgIconPicker），包里没有 —— 与
   * `ProjectGlyph` 同一条：收已经画好的东西，不收 key。不给就整格不画。
   *
   * 是 render prop 而不是裸节点：**写仍然归包**（同一条即时保存的路），宿主只负责
   * 「长什么样、能挑哪些」。裸节点会逼宿主自己再写一遍保存态与失败落点。
   */
  iconField?: (props: {
    value: string;
    /** 当前选中的颜色 token：宿主的图标预览要跟着上色，否则同一枚图标两处两个色。 */
    color: string;
    onPick: (iconKey: string) => void;
  }) => React.ReactNode;
  /** 机器一台都没有时给的那条出路（宿主自己的路由）。不给就只留那句说明。 */
  devicesLink?: React.ReactNode;
  onChanged(): void;
}

/**
 * 一次写的结果怎么变成一句话。
 *
 * 分得出类时用包写好的那一句；分不出类时透出宿主抽好的业务文案。**两条规则一条式子**
 * —— 中继写失败按错误码分四类各给一句（把它们折成同一句「保存失败」，用户就得自己猜
 * 是该等一会儿、该换个目录还是该去开那台机器），而服务端自带文案的业务码
 * （「该 Agent 已经是这个项目的成员」）原样透出。
 */
function useFailureText() {
  const { t } = useUiTranslation();
  return React.useCallback(
    (outcome: Extract<ProjectWriteOutcome, { ok: false }>) => {
      const { kind, message } = outcome.failure;
      if (kind !== "unknown") return t(`projectSettings.failure.${kind}`);
      return message?.trim() || t("projectSettings.failure.unknown");
    },
    [t],
  );
}

export function ProjectSettingsDialog({
  open,
  onOpenChange,
  project,
  parentOptions,
  ports,
  focus,
  iconField,
  devicesLink,
  onChanged,
}: ProjectSettingsDialogProps) {
  const { t } = useUiTranslation();
  const failureText = useFailureText();
  const [name, setName] = React.useState(project.name);
  const [description, setDescription] = React.useState(project.description);
  const [saveState, setSaveState] =
    React.useState<DialogShellSaveState>("idle");
  /** 整窗级错误：与按钮同一行、在它们左边（弹窗规范 4）。 */
  const [error, setError] = React.useState<string | null>(null);
  const [machines, setMachines] = React.useState<ProjectMachineView[]>([]);
  /**
   * 机器清单的三种处境要分开说。
   *
   * 折成同一个条件（`machines.length === 0`）配同一个转圈，读失败就会一直转下去 ——
   * 而把人送进这一节的正是组头那枚「未配置」角标：他点进来是要配路径的。
   */
  const [machinesState, setMachinesState] = React.useState<
    "loading" | "ready" | "failed"
  >("loading");
  const [pickerFor, setPickerFor] = React.useState<ProjectMachineView | null>(
    null,
  );

  /**
   * 直落到那一节。
   *
   * 用 **ref 回调**而不是 `useRef` + effect：这一节住在 Radix 的 Portal 里，弹窗刚
   * 打开那一帧父组件的 effect 跑到时 `ref.current` 还是 null，于是
   * `target?.scrollIntoView()` 静默地什么都不做 —— 一条可选链把「没滚」藏成了
   * 「滚过了」。ref 回调拿到的一定是已经挂好的那个节点。
   */
  const focusRef = React.useCallback((node: HTMLDivElement | null) => {
    if (node) node.scrollIntoView({ block: "start" });
  }, []);

  const reloadMachines = React.useCallback(() => {
    // 状态更新推到一个异步体里：`react-hooks/set-state-in-effect` 禁止在 effect
    // 体里裸调 setState，而这个函数正是被下面那个 effect 直接调用的。
    void (async () => {
      setMachinesState("loading");
      try {
        setMachines(await ports.listMachines(project.id));
        setMachinesState("ready");
      } catch {
        // 清单留空但状态记成 failed：读失败不是「这个账号没有机器」，把它说成
        // 空态等于让人去添加一台他其实已经有的机器。
        setMachines([]);
        setMachinesState("failed");
      }
    })();
  }, [ports, project.id]);

  React.useEffect(() => {
    if (!open) return;
    reloadMachines();
  }, [open, reloadMachines]);

  /**
   * 有一次写在飞。派生自 `saveState` 而不是另立一个 state：两份「在不在写」迟早会
   * 说出两句不一样的话。用它把成员与路径那几颗按钮停掉 —— 它们按下去在版面上没有
   * 任何立即反应（列表要等重取才变），最容易被连点，而第二次提交打到的是一条已经
   * 删掉的记录。
   */
  const writing = saveState === "saving";

  const runWrite = React.useCallback(
    (write: () => Promise<ProjectWriteOutcome>, after?: () => void) => {
      setSaveState("saving");
      setError(null);
      void write().then(
        (outcome) => {
          if (!outcome.ok) {
            setSaveState("error");
            setError(failureText(outcome));
            return;
          }
          setSaveState("saved");
          after?.();
          onChanged();
        },
        (e: unknown) => {
          // port 约定是交判别式结果，但宿主写错了也不能把窗锁死在 saving 上。
          setSaveState("error");
          setError(String(e));
        },
      );
    },
    [failureText, onChanged],
  );

  const saveFields = React.useCallback(
    (fields: ProjectFieldValues) =>
      runWrite(() => ports.updateFields(project.id, fields)),
    [ports, project.id, runWrite],
  );

  /** blur 时才提交，且**值没变就不发** —— 一次 blur 发一次会把即时保存变成噪音。 */
  const commit = React.useCallback(
    (key: keyof ProjectFieldValues, value: string, original: string) => {
      if (value === original) return;
      saveFields({ [key]: value } as ProjectFieldValues);
    },
    [saveFields],
  );

  const writeMachinePath = React.useCallback(
    (machine: ProjectMachineView, path: string) =>
      runWrite(
        () => ports.setMachinePath(project.id, machine, path),
        reloadMachines,
      ),
    [ports, project.id, reloadMachines, runWrite],
  );

  return (
    <>
      <DialogShell open={open} onOpenChange={onOpenChange} size="md">
        <DialogShellHeader
          title={project.name || t("projectSettings.title")}
          saveState={saveState}
          onClose={() => onOpenChange(false)}
        />
        <DialogShellBody className="space-y-4">
          <section data-testid="project-section-basic" className="space-y-4">
            <label className="block text-xs font-medium text-foreground">
              {t("projectSettings.field.name")}
              <Input
                data-testid="project-settings-name"
                value={name}
                onChange={(e) => setName(e.target.value)}
                onBlur={() => commit("name", name.trim(), project.name)}
                className="mt-1"
              />
            </label>
            <label className="block text-xs font-medium text-foreground">
              {t("projectSettings.field.description")}
              <Textarea
                data-testid="project-settings-description"
                value={description}
                onChange={(e) => setDescription(e.target.value)}
                onBlur={() =>
                  commit("description", description.trim(), project.description)
                }
                className="mt-1 min-h-[60px] text-xs"
              />
            </label>
            {iconField ? (
              <div>
                {iconField({
                  value: project.icon ?? "",
                  color: project.color ?? "",
                  onPick: (icon) => saveFields({ icon }),
                })}
              </div>
            ) : null}
            <div>
              <p className="text-xs font-medium text-foreground">
                {t("projectSettings.field.color")}
              </p>
              <ColorSwatches
                value={project.color}
                onPick={(color) => saveFields({ color })}
              />
            </div>
            {/* 父项目那一格是**能力**：宿主给得出候选才画。桌面端今天给不出 ——
                它的 `ProjectUpdateRequest` 没有 parentID，而 `ProjectReorder` 的
                SQL 带 `AND parent_id = ?`（只在同一个父下排序，改不了父）。少画一格
                比画一个按了没反应的下拉好；补齐要加一个 wails 绑定，属于 Go 侧。 */}
            {parentOptions.some((p) => p.id !== project.id) ? (
              <label className="block text-xs font-medium text-foreground">
                {t("projectSettings.field.parent")}
                {/* 候选里**没有它自己**：指向自己会在每一端造出一个走不完的环。
                  后代同样不合法，但那一条由服务端判 —— 禁用下拉项拦不住直接打端点。 */}
                <select
                  data-testid="project-settings-parent"
                  value={project.parentId}
                  onChange={(e) => saveFields({ parentId: e.target.value })}
                  className="mt-1 h-9 w-full rounded-md border border-border bg-background px-2 text-aux"
                >
                  <option value="">
                    {t("projectSettings.field.noParent")}
                  </option>
                  {parentOptions
                    .filter((p) => p.id !== project.id)
                    .map((p) => (
                      <option key={p.id} value={p.id}>
                        {p.name}
                      </option>
                    ))}
                </select>
              </label>
            ) : null}
          </section>

          <Section
            id="members"
            title={t("projectSettings.sections.members")}
            focused={focus === "members"}
            focusRef={focus === "members" ? focusRef : undefined}
          >
            <MembersSection
              members={project.members}
              candidates={project.candidates}
              writing={writing}
              onRemove={(member) =>
                runWrite(() => ports.removeMember(project.id, member))
              }
              onAdd={(candidate) =>
                runWrite(() => ports.addMember(project.id, candidate.id))
              }
            />
          </Section>

          <Section
            id="paths"
            title={t("projectSettings.sections.paths")}
            focused={focus === "paths"}
            focusRef={focus === "paths" ? focusRef : undefined}
          >
            <MachinesSection
              machines={machines}
              state={machinesState}
              writing={writing}
              hasNativePicker={!!ports.pickLocalDirectory}
              devicesLink={devicesLink}
              onReload={reloadMachines}
              onCommitPath={writeMachinePath}
              onChoose={(machine) => {
                // 本机目录用系统原生对话框比任何自绘面板都好（决策 11）。
                if (machine.isSelf && ports.pickLocalDirectory) {
                  void ports.pickLocalDirectory().then((picked) => {
                    if (picked) writeMachinePath(machine, picked);
                  });
                  return;
                }
                setPickerFor(machine);
              }}
              onClear={(machine) =>
                runWrite(
                  () => ports.clearMachinePath(project.id, machine),
                  reloadMachines,
                )
              }
            />
          </Section>
        </DialogShellBody>
        <div data-testid="project-settings-footer">
          {/* 即时保存的弹窗脚部只有「完成」；写失败的正文摆在它左边。 */}
          <DialogShellFooter error={error}>
            <Button
              data-testid="project-settings-done"
              onClick={() => onOpenChange(false)}
            >
              {t("projectSettings.done")}
            </Button>
          </DialogShellFooter>
        </div>
      </DialogShell>

      {pickerFor ? (
        <DirectoryPicker
          open
          onOpenChange={(next) => {
            if (!next) setPickerFor(null);
          }}
          fs={ports.fs}
          machines={machines.map(toPickerMachine)}
          initialMachineId={pickerFor.id}
          initialPath={pickerFor.path || undefined}
          onPick={(machineId, path) => {
            setPickerFor(null);
            const machine = machines.find((m) => m.id === machineId);
            if (machine) writeMachinePath(machine, path);
          }}
        />
      ) : null}
    </>
  );
}

function toPickerMachine(m: ProjectMachineView): PickerMachine {
  return { id: m.id, name: m.name, kind: m.kind, online: m.online };
}

/** 后两节各自是一块带描边的区域：`focus` 落在哪一节，哪一节亮边。 */
function Section({
  id,
  title,
  focused,
  focusRef,
  children,
}: {
  id: string;
  title: string;
  focused: boolean;
  focusRef?: (node: HTMLDivElement | null) => void;
  children: React.ReactNode;
}) {
  return (
    <div
      ref={focusRef}
      data-testid={`project-section-${id}`}
      data-focused={focused ? "true" : undefined}
      className={cn(
        "rounded-md border p-3",
        focused ? "border-ring" : "border-border",
      )}
    >
      <p className="text-xs font-medium text-foreground">{title}</p>
      {children}
    </div>
  );
}

function ColorSwatches({
  value,
  onPick,
}: {
  value?: string;
  onPick: (color: string) => void;
}) {
  return (
    <div className="mt-1 grid grid-cols-8 gap-1.5">
      {agentColorOrder.map((c) => (
        <button
          key={c}
          type="button"
          aria-label={c}
          data-testid={`project-settings-color-${c}`}
          onClick={() => onPick(c)}
          style={{ backgroundColor: tokenToCssColor(c) ?? undefined }}
          className={cn(
            "size-6 rounded-full",
            value === c &&
              "outline outline-2 outline-offset-2 outline-foreground",
          )}
        />
      ))}
    </div>
  );
}

function MembersSection({
  members,
  candidates,
  writing,
  onRemove,
  onAdd,
}: {
  members: ProjectMemberView[];
  candidates: ProjectCandidateView[];
  writing: boolean;
  onRemove: (member: ProjectMemberView) => void;
  onAdd: (candidate: ProjectCandidateView) => void;
}) {
  const { t } = useUiTranslation();
  return (
    <>
      <ul className="mt-2 space-y-1">
        {members.map((m) => (
          <li
            key={m.id}
            data-testid={`project-member-row-${m.id}`}
            className={cn(
              "flex items-center gap-2 text-xs",
              m.inherited && "opacity-70",
            )}
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
              // 继承来的能用，只是出处不在这个项目里 —— 所以这里移除不了它。
              <span
                title={
                  m.inheritedFrom
                    ? t("projectSettings.members.inheritedFrom", {
                        name: m.inheritedFrom,
                      })
                    : undefined
                }
                className="rounded-sm bg-secondary px-1.5 py-0.5 text-2xs text-muted-foreground"
              >
                {t("projectSettings.members.inherited")}
              </span>
            ) : (
              <button
                type="button"
                data-testid={`project-member-remove-${m.id}`}
                disabled={writing}
                aria-label={t("projectSettings.members.remove", {
                  name: m.name,
                })}
                onClick={() => onRemove(m)}
                className="rounded-sm p-1 text-muted-foreground hover:bg-accent hover:text-foreground disabled:cursor-not-allowed disabled:opacity-55"
              >
                <X className="size-3.5" aria-hidden="true" />
              </button>
            )}
          </li>
        ))}
        {members.length === 0 ? (
          <li className="text-xs text-muted-foreground">
            {t("projectSettings.members.empty")}
          </li>
        ) : null}
      </ul>
      {candidates.length > 0 ? (
        <div className="mt-2 flex flex-wrap gap-1">
          {candidates.map((a) => (
            <button
              key={a.id}
              type="button"
              data-testid={`project-member-add-${a.id}`}
              disabled={writing || a.disabled}
              title={a.disabledReason}
              onClick={() => onAdd(a)}
              className="inline-flex items-center gap-1 rounded-md border border-border px-2 py-1 text-2xs hover:bg-accent disabled:cursor-not-allowed disabled:opacity-65"
            >
              <Plus className="size-3" aria-hidden="true" />
              {a.name}
              {/* 加不了的候选留在列表里并说出原因，不是静默消失。 */}
              {a.disabledReason ? (
                <span className="text-muted-foreground">
                  {a.disabledReason}
                </span>
              ) : null}
            </button>
          ))}
        </div>
      ) : (
        <p className="mt-2 text-2xs text-muted-foreground">
          {t("projectSettings.members.noCandidates")}
        </p>
      )}
    </>
  );
}

function MachinesSection({
  machines,
  state,
  writing,
  hasNativePicker,
  devicesLink,
  onReload,
  onCommitPath,
  onChoose,
  onClear,
}: {
  machines: ProjectMachineView[];
  state: "loading" | "ready" | "failed";
  writing: boolean;
  hasNativePicker: boolean;
  devicesLink?: React.ReactNode;
  onReload: () => void;
  onCommitPath: (machine: ProjectMachineView, path: string) => void;
  onChoose: (machine: ProjectMachineView) => void;
  onClear: (machine: ProjectMachineView) => void;
}) {
  const { t } = useUiTranslation();
  return (
    <ul className="mt-2 space-y-1.5">
      {machines.map((m) => (
        <MachineRow
          key={m.id}
          machine={m}
          writing={writing}
          hasNativePicker={hasNativePicker}
          onCommitPath={onCommitPath}
          onChoose={onChoose}
          onClear={onClear}
        />
      ))}
      {state === "loading" ? (
        <li
          data-testid="project-paths-loading"
          className="flex items-center gap-2 text-xs text-muted-foreground"
        >
          <Loader2 className="size-3 animate-spin" aria-hidden="true" />
          {t("common.loading")}
        </li>
      ) : null}
      {state === "failed" ? (
        <li
          data-testid="project-paths-failed"
          className="flex items-center gap-2 text-xs text-muted-foreground"
        >
          <span className="flex-1">{t("projectSettings.paths.failed")}</span>
          <Button variant="outline" size="xs" onClick={onReload}>
            {t("common.retry")}
          </Button>
        </li>
      ) : null}
      {state === "ready" && machines.length === 0 ? (
        // 终局，不是空窗：等多久都不会有东西冒出来，给的必须是一条出路。
        <li
          data-testid="project-paths-empty"
          className="flex flex-wrap items-center gap-x-2 gap-y-1 text-xs text-muted-foreground"
        >
          <span>{t("projectSettings.paths.empty")}</span>
          {devicesLink}
        </li>
      ) : null}
    </ul>
  );
}

/**
 * 一台机器一行。
 *
 * 路径是**可编辑输入**而不是只读正文：agentred 的路径由服务端直写，离线也配得了，
 * 只留一颗要求在线的「选择…」等于让离线的那几台完全配不了；而桌面端本机那一行
 * 本来就是一个输入框（它是宿主写自己）。能不能改由**写往哪去**决定，不由能不能
 * 浏览目录决定 —— 这两件事此前被同一个 `disabled` 绑在了一起。
 */
function MachineRow({
  machine,
  writing,
  hasNativePicker,
  onCommitPath,
  onChoose,
  onClear,
}: {
  machine: ProjectMachineView;
  writing: boolean;
  hasNativePicker: boolean;
  onCommitPath: (machine: ProjectMachineView, path: string) => void;
  onChoose: (machine: ProjectMachineView) => void;
  onClear: (machine: ProjectMachineView) => void;
}) {
  const { t } = useUiTranslation();
  /**
   * 「本机」这枚记号**只出现一次**：宿主给得出机器名（agentre-server 上有指纹对应的
   * 设备名）时它是名字旁边的角标；给不出时（桌面端没有一个说得出自己叫什么的绑定）
   * 它就是名字本身，此时再挂一枚同字的角标只是把同一句话说两遍。
   */
  const selfLabel = t("projectSettings.paths.thisMachine");
  const displayName = machine.name || (machine.isSelf ? selfLabel : "");
  const showSelfBadge = machine.isSelf && displayName !== selfLabel;
  const [path, setPath] = React.useState(machine.path);
  // 重取回来的值要盖掉本地草稿，否则写成功后这一行还显示着旧值。
  React.useEffect(() => setPath(machine.path), [machine.path]);

  /** 改得动吗：要那台机器在线的那一档才看在线（`writeNeedsOnline` 的注释写明了为什么）。 */
  const writable = !machine.writeNeedsOnline || machine.online;
  /** 浏览目录是另一回事：离线的机器答不出目录里有什么。本机走原生对话框，不受此限。 */
  const browsable = machine.isSelf ? hasNativePicker : machine.online;

  return (
    <li
      data-testid={`project-path-row-${machine.id}`}
      className="flex items-center gap-2 text-xs"
    >
      <span
        aria-hidden="true"
        className={cn(
          "size-1.5 shrink-0 rounded-full",
          machine.online ? "bg-status-running" : "bg-muted-foreground/40",
        )}
      />
      {machine.kind === "agentred" ? (
        <Server
          className="size-3 shrink-0 text-muted-foreground"
          aria-hidden="true"
        />
      ) : (
        <MonitorSmartphone
          className="size-3 shrink-0 text-muted-foreground"
          aria-hidden="true"
        />
      )}
      <span className="w-[110px] shrink-0 truncate" title={displayName}>
        {displayName}
      </span>
      {showSelfBadge ? (
        <span className="shrink-0 rounded-sm bg-secondary px-1.5 py-0.5 text-2xs text-muted-foreground">
          {selfLabel}
        </span>
      ) : null}
      <Input
        data-testid={`project-path-input-${machine.id}`}
        aria-label={t("projectSettings.paths.path", { machine: displayName })}
        value={path}
        disabled={!writable}
        placeholder={t("projectSettings.paths.noPath")}
        onChange={(e) => setPath(e.target.value)}
        onBlur={() => {
          const next = path.trim();
          if (next === machine.path) return;
          onCommitPath(machine, next);
        }}
        className="h-7 min-w-0 flex-1 font-mono text-2xs"
      />
      <Button
        data-testid={`project-path-choose-${machine.id}`}
        variant="outline"
        size="xs"
        disabled={!browsable}
        onClick={() => onChoose(machine)}
      >
        {t("projectSettings.paths.choose")}
      </Button>
      {machine.removable ? (
        <button
          type="button"
          data-testid={`project-path-remove-${machine.id}`}
          aria-label={t("projectSettings.paths.remove", {
            machine: displayName,
          })}
          disabled={writing || !writable}
          onClick={() => onClear(machine)}
          className="rounded-sm p-1 text-muted-foreground hover:bg-accent hover:text-foreground disabled:cursor-not-allowed disabled:opacity-55"
        >
          <X className="size-3.5" aria-hidden="true" />
        </button>
      ) : null}
    </li>
  );
}
