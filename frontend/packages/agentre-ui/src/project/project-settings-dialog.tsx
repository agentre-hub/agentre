/**
 * 项目设置，两端共用那一份（规格 2026-08-22 B 段，决策 2/3/4/7/14；2026-08-27 改版）。
 *
 * **一屏三节**（身份 / 成员 / 机器与路径），不是标签页。
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
 *
 * ### 2026-08-27 这一轮换掉的四件事
 *
 * 1. **身份区合一**（`ProjectIdentityFields`）：字形预览 + 名字 + 简介一块，图标与
 *    颜色收进字形那个弹层。头部标题因此改回静态「项目设置」—— 名字已经在正文第一行，
 *    再当一次标题就是同一句话说两遍。
 * 2. **节的语法统一**成「细标题 + 分隔线」，不再有描边方块（此前「基本」没标题没
 *    描边、后两节是方块，版面在说后两节是特殊的，其实同级）。节头 `sticky`：十台
 *    机器滚下去时，头上写着自己还在哪一节。
 * 3. **成员候选改成选人层**：此前候选平铺到底、加不了的把原因塞进 chip 里，宽度乱跳，
 *    Agent 一多这一节就糊了。
 * 4. **「机器与路径」只列相关的**：本机 + 已配路径的 + 有成员在上面的（`hasMember`），
 *    账号里其余的收进「＋ 在别的机器上配路径」。这一节回答的是「这个项目在哪」，
 *    不是「账号里有哪些机器」—— 配了八台 agentred，一个刚建的项目此前要画九行，
 *    其中八行是空的。
 */
import * as React from "react";
import {
  FolderOpen,
  Loader2,
  MonitorSmartphone,
  Plus,
  Search,
  Server,
  X,
} from "lucide-react";

import { useUiTranslation } from "../i18n";
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
import { Popover, PopoverContent, PopoverTrigger } from "../ui/popover";
import { DirectoryPicker } from "./directory-picker";
import { ParentSelect } from "./parent-select";
import { ProjectIdentityFields } from "./project-identity-fields";
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
  /**
   * 直落到哪一节。
   *
   * 改版之后弹窗整窗落在外壳 720px 的上限以内，组头菜单里那两条深链已经并回
   * 「项目设置…」；**留着这一档是给组头那枚「未配置」角标的** —— 它说的就是
   * 「机器与路径」那件事，点它该直接到那里。
   */
  focus?: "members" | "paths";
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
  /**
   * 名字那一格的失败，紧贴输入框下面。
   *
   * **只有名字有这一档**：重名是唯一一种「针对某一格」的业务失败；其余的写
   * （换父项目成环、该 Agent 已是成员、路径写不进去）说的都不是某一格的事，
   * 落脚部才对。给每一格都配一条错误行只会让人以为哪一格都可能单独出事。
   */
  const [nameError, setNameError] = React.useState<string | null>(null);
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
   * 从「＋ 在别的机器上配路径」挑出来、这一次要直接列出来的机器。
   *
   * 刚挑出来的那台还没有路径，不挂这一份它就会立刻从表里消失 —— 用户挑完一台机器，
   * 版面上什么都没发生。
   */
  const [revealed, setRevealed] = React.useState<Set<string>>(new Set());

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
    (
      write: () => Promise<ProjectWriteOutcome>,
      opts: { after?: () => void; field?: "name" } = {},
    ) => {
      setSaveState("saving");
      setError(null);
      setNameError(null);
      void write().then(
        (outcome) => {
          if (!outcome.ok) {
            setSaveState("error");
            const text = failureText(outcome);
            if (opts.field === "name") setNameError(text);
            else setError(text);
            return;
          }
          setSaveState("saved");
          opts.after?.();
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
    (fields: ProjectFieldValues, field?: "name") =>
      runWrite(() => ports.updateFields(project.id, fields), { field }),
    [ports, project.id, runWrite],
  );

  /** blur 时才提交，且**值没变就不发** —— 一次 blur 发一次会把即时保存变成噪音。 */
  const commit = React.useCallback(
    (key: keyof ProjectFieldValues, value: string, original: string) => {
      if (value === original) return;
      saveFields(
        { [key]: value } as ProjectFieldValues,
        key === "name" ? "name" : undefined,
      );
    },
    [saveFields],
  );

  const writeMachinePath = React.useCallback(
    (machine: ProjectMachineView, path: string) =>
      runWrite(() => ports.setMachinePath(project.id, machine, path), {
        after: reloadMachines,
      }),
    [ports, project.id, reloadMachines, runWrite],
  );

  /**
   * 列出来的与收进「＋」里的。
   *
   * `hasMember` 是**可选能力**：宿主答不出「某个成员的 Agent 绑在哪台机器上」时，
   * 这一节退成「本机 + 已配路径的」，功能不减。清单本身照旧是全的，所以那颗「＋」
   * 里永远挑得到剩下的机器。
   */
  const relevant = machines.filter(
    (m) => m.isSelf || m.path || m.hasMember || revealed.has(m.id),
  );
  /**
   * 一台都不相关时把全集摊开。
   *
   * 那颗「＋」是用来**收噪音**的，不是用来把这一节清空的。桌面端永远有「本机」那一行
   * 所以碰不到；agentre-server 上没有本机，一个还没配过路径的项目于是一台都不相关 ——
   * 若照收不误，用户点进来看到的是一节空白加一颗「＋」，而他正是来配路径的。
   */
  const listed = relevant.length > 0 ? relevant : machines;
  const hidden = machines.filter((m) => !listed.includes(m));
  const unconfigured = listed.filter((m) => !m.path).length;

  return (
    <>
      <DialogShell open={open} onOpenChange={onOpenChange} size="md">
        <DialogShellHeader
          // 名字已经在正文第一行 —— 标题再说一遍就是同一句话说两遍。
          title={t("projectSettings.title")}
          saveState={saveState}
          onClose={() => onOpenChange(false)}
        />
        <DialogShellBody>
          <div data-testid="project-section-identity" className="space-y-4">
            <ProjectIdentityFields
              testIdPrefix="project-settings"
              name={name}
              description={description}
              icon={project.icon}
              color={project.color}
              nameError={nameError}
              onNameChange={setName}
              onDescriptionChange={setDescription}
              onNameBlur={() => commit("name", name.trim(), project.name)}
              onDescriptionBlur={() =>
                commit("description", description.trim(), project.description)
              }
              onPickIcon={(icon) => saveFields({ icon })}
              onPickColor={(color) => saveFields({ color })}
            />
            {/* 父项目那一格是**能力**：宿主给得出候选（除它自己以外还有别人）
                才画。少画一格比画一个按了没反应的下拉好。 */}
            {parentOptions.some((p) => p.id !== project.id) ? (
              <ParentSelect
                data-testid="project-settings-parent"
                value={project.parentId}
                options={parentOptions}
                excludeId={project.id}
                onChange={(parentId) => saveFields({ parentId })}
              />
            ) : null}
          </div>

          <Section
            id="members"
            title={t("projectSettings.sections.members")}
            count={project.members.length}
            focused={focus === "members"}
            focusRef={focus === "members" ? focusRef : undefined}
            actions={
              <AddMemberPopover
                candidates={project.candidates}
                writing={writing}
                onAdd={(candidate) =>
                  runWrite(() => ports.addMember(project.id, candidate.id))
                }
              />
            }
          >
            <MembersSection
              members={project.members}
              writing={writing}
              onRemove={(member) =>
                runWrite(() => ports.removeMember(project.id, member))
              }
            />
          </Section>

          <Section
            id="paths"
            title={t("projectSettings.sections.paths")}
            focused={focus === "paths"}
            focusRef={focus === "paths" ? focusRef : undefined}
            right={
              machinesState === "ready" ? (
                <span className="flex items-center gap-2">
                  <span data-testid="project-paths-count">
                    {t("projectSettings.paths.count", { count: listed.length })}
                  </span>
                  {unconfigured > 0 ? (
                    <span
                      data-testid="project-paths-unconfigured"
                      className="rounded-sm bg-status-waiting-bg px-1.5 py-0.5 text-status-waiting"
                    >
                      {t("projectSettings.paths.unconfiguredCount", {
                        count: unconfigured,
                      })}
                    </span>
                  ) : null}
                </span>
              ) : undefined
            }
          >
            <MachinesSection
              machines={listed}
              hidden={hidden}
              state={machinesState}
              writing={writing}
              hasNativePicker={!!ports.pickLocalDirectory}
              devicesLink={devicesLink}
              onReload={reloadMachines}
              onReveal={(machine) =>
                setRevealed((prev) => new Set(prev).add(machine.id))
              }
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
                runWrite(() => ports.clearMachinePath(project.id, machine), {
                  after: reloadMachines,
                })
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

/**
 * 一节 = 一道分隔线 + 一行细标题，**不是一个描边方块**。
 *
 * 合之前「基本」没标题没描边、后两节是方块 —— 版面在说后两节是特殊的，其实它们同级。
 *
 * 节头 `sticky`：十台机器一路滚下去时，头上得写着自己还在哪一节。它靠负外边距把
 * 正文的左右内边距吃回来，否则钉住时底色盖不满、字会从背后透出来。
 *
 * `focus` 落在哪一节，哪一节亮起来：左侧一条 accent + 淡底。此前是换描边色 —— 在一个
 * 到处都是描边方块的版面里，那一格根本认不出来。
 */
function Section({
  id,
  title,
  count,
  right,
  actions,
  focused,
  focusRef,
  children,
}: {
  id: string;
  title: string;
  count?: number;
  right?: React.ReactNode;
  actions?: React.ReactNode;
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
        "mt-4 border-t border-border pt-1",
        focused &&
          "-ml-3 rounded-r-md border-l-2 border-l-ring bg-accent/40 pl-3",
      )}
    >
      <div className="sticky top-0 z-10 -mx-5 flex items-center gap-2 bg-card px-5 py-2">
        <p className="text-2xs font-semibold tracking-wide text-muted-foreground">
          {title}
        </p>
        {count !== undefined ? (
          <span
            // 前缀刻意不是 `project-section-` —— 那个前缀是「一节」的标记，
            // 计数挂上去会被当成第四节。
            data-testid={`project-${id}-count`}
            className="rounded-sm bg-secondary px-1.5 py-0.5 text-2xs text-muted-foreground"
          >
            {count}
          </span>
        ) : null}
        {right ? (
          <span className="text-2xs text-muted-foreground">{right}</span>
        ) : null}
        <span className="flex-1" />
        {actions}
      </div>
      {children}
    </div>
  );
}

/**
 * 候选不再平铺到底，收进一个带搜索的选人层。
 *
 * 此前它们是一排 chip，加不了的还把原因塞进 chip 里（宽度乱跳）；账号里 Agent 一多，
 * 这一节就糊成一片。加不了的**仍然留在层里并说出原因**，不是静默消失 —— 一个用户
 * 以为自己有的 Agent 凭空不见，比一行灰字更让人找不着北。
 */
function AddMemberPopover({
  candidates,
  writing,
  onAdd,
}: {
  candidates: ProjectCandidateView[];
  writing: boolean;
  onAdd: (candidate: ProjectCandidateView) => void;
}) {
  const { t } = useUiTranslation();
  const [open, setOpen] = React.useState(false);
  const [query, setQuery] = React.useState("");
  const matched = candidates.filter((c) =>
    c.name.toLowerCase().includes(query.trim().toLowerCase()),
  );

  return (
    <Popover
      open={open}
      onOpenChange={(next) => {
        setOpen(next);
        if (!next) setQuery("");
      }}
    >
      <PopoverTrigger asChild>
        <Button
          data-testid="project-member-add-open"
          variant="outline"
          size="xs"
          // 一个都加不了时按钮照样在，点开说明为什么 —— 少一颗按钮等于少一句解释。
          disabled={writing}
        >
          <Plus className="size-3" aria-hidden="true" />
          {t("projectSettings.members.addTitle")}
        </Button>
      </PopoverTrigger>
      <PopoverContent align="end" className="w-[280px] p-2">
        <div className="relative">
          <Search
            className="pointer-events-none absolute left-2.5 top-1/2 size-3 -translate-y-1/2 text-muted-foreground"
            aria-hidden="true"
          />
          <Input
            data-testid="project-member-search"
            aria-label={t("projectSettings.members.searchPlaceholder")}
            placeholder={t("projectSettings.members.searchPlaceholder")}
            value={query}
            onChange={(e) => setQuery(e.target.value)}
            className="h-8 pl-7 text-xs"
          />
        </div>
        <ul className="mt-2 max-h-[220px] space-y-0.5 overflow-y-auto">
          {matched.map((a) => (
            <li key={a.id}>
              <button
                type="button"
                data-testid={`project-member-add-${a.id}`}
                disabled={writing || a.disabled}
                onClick={() => {
                  onAdd(a);
                  setOpen(false);
                }}
                className="flex w-full items-center gap-2 rounded-md px-2 py-1.5 text-left text-xs hover:bg-accent disabled:cursor-not-allowed disabled:hover:bg-transparent"
              >
                <AgentAvatar
                  name={a.name}
                  color={a.color}
                  icon={a.avatarIcon}
                  avatarDataUrl={a.avatarDataUrl}
                  size="xs"
                />
                <span className="min-w-0 flex-1 truncate">{a.name}</span>
              </button>
              {/* 加不了的原因单独一行 —— 塞进那一行里会把每一条候选撑成不同的宽度。 */}
              {a.disabledReason ? (
                <p className="px-2 pb-1 pl-9 text-2xs text-status-waiting">
                  {a.disabledReason}
                </p>
              ) : null}
            </li>
          ))}
          {matched.length === 0 ? (
            <li
              data-testid="project-member-none"
              className="px-2 py-2 text-2xs text-muted-foreground"
            >
              {candidates.length === 0
                ? t("projectSettings.members.noCandidates")
                : t("projectSettings.members.noMatch")}
            </li>
          ) : null}
        </ul>
      </PopoverContent>
    </Popover>
  );
}

function MembersSection({
  members,
  writing,
  onRemove,
}: {
  members: ProjectMemberView[];
  writing: boolean;
  onRemove: (member: ProjectMemberView) => void;
}) {
  const { t } = useUiTranslation();
  return (
    <ul className="space-y-0.5 pb-1">
      {members.map((m) => (
        <li
          key={m.id}
          data-testid={`project-member-row-${m.id}`}
          className={cn(
            "flex items-center gap-2 rounded-md px-1.5 py-1 text-xs",
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
        <li className="px-1.5 py-1 text-xs text-muted-foreground">
          {t("projectSettings.members.empty")}
        </li>
      ) : null}
    </ul>
  );
}

function MachinesSection({
  machines,
  hidden,
  state,
  writing,
  hasNativePicker,
  devicesLink,
  onReload,
  onReveal,
  onCommitPath,
  onChoose,
  onClear,
}: {
  machines: ProjectMachineView[];
  hidden: ProjectMachineView[];
  state: "loading" | "ready" | "failed";
  writing: boolean;
  hasNativePicker: boolean;
  devicesLink?: React.ReactNode;
  onReload: () => void;
  onReveal: (machine: ProjectMachineView) => void;
  onCommitPath: (machine: ProjectMachineView, path: string) => void;
  onChoose: (machine: ProjectMachineView) => void;
  onClear: (machine: ProjectMachineView) => void;
}) {
  const { t } = useUiTranslation();
  return (
    <ul className="space-y-1.5 pb-1">
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
      {state === "ready" && machines.length === 0 && hidden.length === 0 ? (
        // 终局，不是空窗：等多久都不会有东西冒出来，给的必须是一条出路。
        <li
          data-testid="project-paths-empty"
          className="flex flex-wrap items-center gap-x-2 gap-y-1 text-xs text-muted-foreground"
        >
          <span>{t("projectSettings.paths.empty")}</span>
          {devicesLink}
        </li>
      ) : null}
      {state === "ready" && hidden.length > 0 ? (
        <li>
          <AddMachinePopover machines={hidden} onReveal={onReveal} />
        </li>
      ) : null}
    </ul>
  );
}

/**
 * 账号里其余的机器收在这颗「＋」后面。
 *
 * 这一节回答的是「这个项目在哪」，所以默认只列相关的那几台；但清单本身仍然是全的，
 * 于是这里挑得到剩下的每一台。**离线的照样挑得了** —— 路径写往哪去与那台机器在不在
 * 线是两件事（见 `ProjectMachineView.writeNeedsOnline`）。
 */
function AddMachinePopover({
  machines,
  onReveal,
}: {
  machines: ProjectMachineView[];
  onReveal: (machine: ProjectMachineView) => void;
}) {
  const { t } = useUiTranslation();
  const [open, setOpen] = React.useState(false);
  const [query, setQuery] = React.useState("");
  const matched = machines.filter((m) =>
    m.name.toLowerCase().includes(query.trim().toLowerCase()),
  );

  return (
    <Popover
      open={open}
      onOpenChange={(next) => {
        setOpen(next);
        if (!next) setQuery("");
      }}
    >
      <PopoverTrigger asChild>
        <button
          type="button"
          data-testid="project-paths-add-open"
          className="flex w-full items-center gap-2 rounded-md border border-dashed border-border-strong px-2.5 py-2 text-xs text-muted-foreground transition-colors hover:bg-accent hover:text-foreground"
        >
          <Plus className="size-3.5" aria-hidden="true" />
          {t("projectSettings.paths.addMachine")}
        </button>
      </PopoverTrigger>
      <PopoverContent align="start" className="w-[300px] p-2">
        <div className="relative">
          <Search
            className="pointer-events-none absolute left-2.5 top-1/2 size-3 -translate-y-1/2 text-muted-foreground"
            aria-hidden="true"
          />
          <Input
            data-testid="project-paths-machine-search"
            aria-label={t("projectSettings.paths.machineSearch")}
            placeholder={t("projectSettings.paths.machineSearch")}
            value={query}
            onChange={(e) => setQuery(e.target.value)}
            className="h-8 pl-7 text-xs"
          />
        </div>
        <ul className="mt-2 max-h-[220px] space-y-0.5 overflow-y-auto">
          {matched.map((m) => (
            <li key={m.id}>
              <button
                type="button"
                data-testid={`project-paths-add-${m.id}`}
                onClick={() => {
                  onReveal(m);
                  setOpen(false);
                }}
                className="flex w-full items-center gap-2 rounded-md px-2 py-1.5 text-left text-xs hover:bg-accent"
              >
                <MachineDot online={m.online} />
                <MachineIcon kind={m.kind} />
                <span className="min-w-0 flex-1 truncate">{m.name}</span>
                {!m.online ? (
                  <span className="shrink-0 rounded-sm bg-secondary px-1.5 py-0.5 text-2xs text-muted-foreground">
                    {t("projectSettings.paths.offline")}
                  </span>
                ) : null}
              </button>
            </li>
          ))}
          {matched.length === 0 ? (
            <li className="px-2 py-2 text-2xs text-muted-foreground">
              {t("projectSettings.paths.allListed")}
            </li>
          ) : null}
        </ul>
        <p className="mt-2 border-t border-border px-2 pt-2 text-2xs leading-relaxed text-muted-foreground">
          {t("projectSettings.paths.addHint")}
        </p>
      </PopoverContent>
    </Popover>
  );
}

function MachineDot({ online }: { online: boolean }) {
  return (
    <span
      aria-hidden="true"
      className={cn(
        "size-1.5 shrink-0 rounded-full",
        online ? "bg-status-running" : "bg-muted-foreground/40",
      )}
    />
  );
}

function MachineIcon({ kind }: { kind: ProjectMachineView["kind"] }) {
  const Comp = kind === "agentred" ? Server : MonitorSmartphone;
  return (
    <Comp
      className="size-3 shrink-0 text-muted-foreground"
      aria-hidden="true"
    />
  );
}

/**
 * 一台机器一行，**两行制**：机器名一行，路径独占一行。
 *
 * 一行摆不下 —— 560px 的弹窗减去内边距只剩约 520px，机器名那一格占死 130px，两个
 * 动作再吃掉 85px，路径输入框缩成 10px 的 mono；而路径**正是这一行的正文**。分成
 * 两行之后它拿到整行宽度，11px mono 摆得开一条完整路径。
 *
 * 路径是**可编辑输入**而不是只读正文：agentred 的路径由服务端直写，离线也配得了，
 * 只留一颗要求在线的「浏览…」等于让离线的那几台完全配不了；而桌面端本机那一行
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
      className="space-y-1.5 rounded-md bg-secondary/50 px-2.5 py-2"
    >
      <div className="flex items-center gap-2 text-xs">
        <MachineDot online={machine.online} />
        <MachineIcon kind={machine.kind} />
        <span className="min-w-0 truncate font-medium" title={displayName}>
          {displayName}
        </span>
        {showSelfBadge ? (
          <span className="shrink-0 rounded-sm bg-secondary px-1.5 py-0.5 text-2xs text-muted-foreground">
            {selfLabel}
          </span>
        ) : null}
        {!machine.online ? (
          <span className="shrink-0 rounded-sm bg-secondary px-1.5 py-0.5 text-2xs text-muted-foreground">
            {t("projectSettings.paths.offline")}
          </span>
        ) : null}
        <span className="flex-1" />
        {machine.removable ? (
          <button
            type="button"
            data-testid={`project-path-remove-${machine.id}`}
            aria-label={t("projectSettings.paths.remove", {
              machine: displayName,
            })}
            disabled={writing || !writable}
            onClick={() => onClear(machine)}
            className="-my-1 rounded-sm p-1 text-muted-foreground hover:bg-accent hover:text-foreground disabled:cursor-not-allowed disabled:opacity-55"
          >
            <X className="size-3.5" aria-hidden="true" />
          </button>
        ) : null}
      </div>
      <div className="flex items-center gap-2">
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
          className="h-7 min-w-0 flex-1 bg-card font-mono text-2xs"
        />
        <Button
          data-testid={`project-path-choose-${machine.id}`}
          variant="outline"
          size="xs"
          disabled={!browsable}
          onClick={() => onChoose(machine)}
        >
          <FolderOpen className="size-3" aria-hidden="true" />
          {t("projectSettings.paths.choose")}
        </Button>
      </div>
    </li>
  );
}
