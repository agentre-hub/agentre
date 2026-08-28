import * as React from "react";
import { Boxes, Copy } from "lucide-react";
import { useTranslation } from "react-i18next";

import {
  Popover,
  PopoverContent,
  PopoverTrigger,
} from "@agentre-hub/agentre-ui";
import { cn } from "@/lib/utils";

import type {
  department_svc,
  agent_backend_svc,
} from "../../../../wailsjs/go/models";

import type { TriState } from "../capability/catalog";
import { CapabilityPicker } from "../capability/capability-picker";

import { useSkillCatalog } from "./use-skill-catalog";

export type CopySource = {
  index: number;
  label: string;
  skills: department_svc.AgentSkillDTO[];
  sameType: boolean;
};

type Props = {
  agentId: number;
  index: number;
  total: number;
  backend: agent_backend_svc.BackendItem | undefined;
  skills: department_svc.AgentSkillDTO[];
  onSkillsChange: (next: department_svc.AgentSkillDTO[]) => void;
  copySources: CopySource[];
};

const BACKEND_TYPE_LABEL: Record<string, string> = {
  claudecode: "Claude Code",
  "claude-code": "Claude Code",
  codex: "Codex",
  builtin: "Built-in",
  openclaw: "OpenClaw",
  piagent: "Pi Agent",
};

function backendTypeLabel(type: string): string {
  return BACKEND_TYPE_LABEL[type] ?? type;
}

// ExecTargetSkillsBlock 是折在执行目标行内的技能块：技能发现来源与授权都钉死在
// props.backend 这一档，不与 Agent 名下别的档合并（不需要并集,不需要"几台装了"的
// 分母)。GrantedChips/CapabilityPicker 两个既有组件的内部渲染逻辑不动——这里的
// chip 行是按同一套 tone 规则(inherit/on/off)自行渲染,因为这一块的标题行要放
// 档位序号 + 机器·后端,不是 GrantedChips 那个通用的"技能 · SKILL PACKS"标题。
export function ExecTargetSkillsBlock(props: Props) {
  const { t } = useTranslation();
  const isRemote = Boolean(props.backend?.deviceId);
  const online = !isRemote || Boolean(props.backend?.online);
  const skillCatalog = useSkillCatalog(
    props.agentId,
    props.backend?.id ?? 0,
    online,
  );
  const [pickerOpen, setPickerOpen] = React.useState(false);
  const [copyOpen, setCopyOpen] = React.useState(false);

  const idToName = (id: string) => id.split("@")[0] || id;
  const skillCountById = React.useMemo(() => {
    const m = new Map<string, number>();
    for (const it of skillCatalog.items) m.set(it.id, it.contents?.length ?? 0);
    return m;
  }, [skillCatalog.items]);
  const globallyOn = React.useMemo(
    () =>
      new Set(
        skillCatalog.items.filter((i) => i.globallyEnabled).map((i) => i.id),
      ),
    [skillCatalog.items],
  );

  const onSkills = props.skills.filter((s) => s.enabled);
  const offSkills = props.skills.filter((s) => !s.enabled);
  const overriddenIds = new Set(props.skills.map((s) => s.id));
  const inheritedIds = [...globallyOn].filter((id) => !overriddenIds.has(id));

  const skillStateOf = (id: string): TriState => {
    const s = props.skills.find((x) => x.id === id);
    if (!s) return "inherit";
    return s.enabled ? "on" : "off";
  };
  const setSkillState = (id: string, next: TriState) => {
    const rest = props.skills.filter((s) => s.id !== id);
    const nextSkills =
      next === "inherit" ? rest : [...rest, { id, enabled: next === "on" }];
    props.onSkillsChange(nextSkills);
  };
  const removeOverride = (id: string) =>
    props.onSkillsChange(props.skills.filter((s) => s.id !== id));

  const pickerItems = skillCatalog.items.map((it) => ({
    ...it,
    state: it.state ? skillStateOf(it.id) : undefined,
  }));

  const openPicker = () => {
    if (!online) return;
    setPickerOpen(true);
    if (!skillCatalog.fetched) void skillCatalog.load(false);
  };

  const machine = props.backend?.deviceId
    ? props.backend.deviceName || props.backend.deviceId
    : t("org.agent.execTargets.localMachine");
  const backendLabel = props.backend
    ? backendTypeLabel(props.backend.type)
    : "";

  const triLabels: Record<TriState, string> = {
    inherit: t("capability.triState.inherit"),
    on: t("capability.triState.on"),
    off: t("capability.triState.off"),
  };

  const chips: Array<{
    id: string;
    label: string;
    count?: number;
    tone: TriState;
  }> = [
    ...inheritedIds.map((id) => ({
      id,
      label: idToName(id),
      count: skillCountById.get(id),
      tone: "inherit" as const,
    })),
    ...onSkills.map((s) => ({
      id: s.id,
      label: idToName(s.id),
      count: skillCountById.get(s.id),
      tone: "on" as const,
    })),
    ...offSkills.map((s) => ({
      id: s.id,
      label: idToName(s.id),
      count: skillCountById.get(s.id),
      tone: "off" as const,
    })),
  ];

  return (
    <div
      className="flex flex-col gap-1.5 rounded-md border border-border bg-input-bg p-2"
      data-testid={`exec-target-skills-block-${props.index}`}
    >
      <div className="flex items-center gap-1.5">
        {props.total > 1 && (
          <span className="inline-flex size-4 shrink-0 items-center justify-center rounded-sm bg-secondary font-mono text-2xs text-muted-foreground">
            {props.index + 1}
          </span>
        )}
        <span className="truncate text-2xs font-semibold">
          {machine} · {backendLabel}
        </span>
        {isRemote && (
          <span className="inline-flex shrink-0 items-center gap-1 rounded-sm bg-secondary px-1.5 py-0.5 font-mono text-2xs text-muted-foreground">
            {online
              ? t("org.agent.execTargets.online")
              : t("org.agent.execTargets.offline")}
          </span>
        )}
        <div className="flex-1" />
        {online && (
          <span className="shrink-0 font-mono text-2xs text-muted-foreground">
            {t("org.agent.skills.countCompact", {
              inherit: inheritedIds.length,
              on: onSkills.length,
              off: offSkills.length,
            })}
          </span>
        )}
      </div>

      <div className="flex flex-col gap-1.5">
        <div className="flex flex-wrap items-center gap-1.5">
          {chips.length === 0 && (
            <span className="text-2xs text-muted-foreground">
              {t("org.agent.skills.empty")}
            </span>
          )}
          {chips.map((chip) => {
            const toneClass =
              chip.tone === "inherit"
                ? "border-border bg-secondary/60 text-muted-foreground"
                : chip.tone === "off"
                  ? "border-destructive/30 bg-destructive/10 text-destructive"
                  : "border-border bg-card";
            return (
              <span
                key={chip.id}
                className={cn(
                  "inline-flex max-w-full min-w-0 items-center gap-1.5 rounded-md border px-2 py-1",
                  toneClass,
                )}
              >
                <Boxes
                  className={cn(
                    "size-3",
                    chip.tone === "off"
                      ? "text-destructive"
                      : "text-primary-text",
                  )}
                  aria-hidden="true"
                />
                {/* 包 id 是一串不可断的路径：窄栏下必须能收缩，否则芯片把行撑出
                    栏宽，外层滚动容器再把溢出藏起来（谁都不报错）。 */}
                <span
                  className={cn(
                    "min-w-0 truncate font-mono text-2xs font-medium",
                    chip.tone === "off" && "line-through",
                  )}
                  title={chip.label}
                >
                  {chip.label}
                </span>
                {typeof chip.count === "number" && (
                  <span className="rounded bg-secondary px-1 font-mono text-2xs text-muted-foreground">
                    {chip.count}
                  </span>
                )}
                {chip.tone !== "inherit" && (
                  <button
                    type="button"
                    aria-label={t("capability.picker.remove", {
                      name: chip.label,
                    })}
                    onClick={() => removeOverride(chip.id)}
                    className="text-muted-foreground hover:text-foreground"
                  >
                    ✕
                  </button>
                )}
              </span>
            );
          })}
          <button
            type="button"
            aria-label={t("org.agent.skills.manage")}
            disabled={!online}
            onClick={openPicker}
            className={cn(
              "inline-flex items-center gap-1 rounded-md border px-2 py-1 font-mono text-2xs font-semibold",
              online
                ? "border-primary/30 bg-primary-soft text-primary-text"
                : "cursor-not-allowed border-border text-muted-foreground",
            )}
          >
            + {t("org.agent.skills.manage")}
          </button>
          {props.copySources.length > 0 && (
            <Popover open={copyOpen} onOpenChange={setCopyOpen}>
              <PopoverTrigger asChild>
                <button
                  type="button"
                  className="inline-flex items-center gap-1 rounded-md border border-border px-2 py-1 font-mono text-2xs text-muted-foreground hover:bg-accent"
                >
                  <Copy className="size-3" aria-hidden="true" />
                  {t("org.agent.skills.copyFrom")}
                </button>
              </PopoverTrigger>
              <PopoverContent className="w-64 p-2">
                <p className="px-1 pb-1 font-mono text-2xs font-semibold text-muted-foreground">
                  {t("org.agent.skills.copyFrom")}
                </p>
                <div className="flex flex-col gap-0.5">
                  {props.copySources.map((src) => (
                    <button
                      type="button"
                      key={src.index}
                      disabled={!src.sameType}
                      onClick={() => {
                        props.onSkillsChange(src.skills);
                        setCopyOpen(false);
                      }}
                      className={cn(
                        "flex flex-col gap-0.5 rounded-sm px-1.5 py-1 text-left text-xs",
                        src.sameType
                          ? "hover:bg-accent"
                          : "cursor-not-allowed opacity-40",
                      )}
                    >
                      <span>{src.label}</span>
                      {!src.sameType && (
                        <span className="font-mono text-2xs text-muted-foreground">
                          {t("org.agent.skills.copyFromDisabledHint")}
                        </span>
                      )}
                    </button>
                  ))}
                </div>
              </PopoverContent>
            </Popover>
          )}
        </div>
        {!online && (
          <p className="font-mono text-2xs text-muted-foreground">
            {t("org.agent.skills.offlineNote")}
          </p>
        )}
      </div>

      <CapabilityPicker
        open={pickerOpen}
        title={t("org.agent.skillPicker.title")}
        subtitle={t("org.agent.skillPicker.subtitleForTarget", {
          index: props.index + 1,
          machine,
          backend: backendLabel,
        })}
        searchPlaceholder={t("org.agent.skillPicker.searchPlaceholder")}
        items={pickerItems}
        loading={skillCatalog.loading}
        triLabels={triLabels}
        footerSummary={t("org.agent.skills.count", {
          inherit: inheritedIds.length,
          on: onSkills.length,
          off: offSkills.length,
        })}
        footerNote={t("org.agent.skillPicker.personalNote")}
        onToggle={() => {}}
        onSetState={setSkillState}
        onConfirm={() => setPickerOpen(false)}
        onCancel={() => setPickerOpen(false)}
        onRescan={() => void skillCatalog.load(true)}
      />
    </div>
  );
}
