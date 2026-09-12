// 「脚本」页签:编辑体 + 时区选择 + 试跑;TZ_OPTIONS 是那个下拉的取值表。

import type { TFunction } from "i18next";
import { KeyRound, Plus, Terminal, Timer, Trash2 } from "lucide-react";
import {
  Button,
  Input,
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
  Switch,
  Textarea,
} from "@agentre-hub/agentre-ui";

import {
  type Draft,
  type EnvVar,
  type InterpreterOption,
} from "../hooks-page-model";

import { SectionCard } from "./section-card";

export const TZ_OPTIONS = [
  "Asia/Shanghai",
  "UTC",
  "America/New_York",
  "Europe/London",
  "Asia/Tokyo",
];

// ── Run result (inline dry-run / run-now output) ─────────────────────────────

export function ScriptTab({
  draft,
  onChange,
  interpreters,
  t,
}: {
  draft: Draft;
  onChange: (next: Draft) => void;
  interpreters: InterpreterOption[];
  t: TFunction;
}) {
  const setEnv = (env: EnvVar[]) => onChange({ ...draft, env });

  // Ensure the currently selected interpreter always appears even if not in
  // the probed list (e.g. while probing or on a platform mismatch).
  const options =
    interpreters.some((o) => o.key === draft.interpreter) ||
    interpreters.length === 0
      ? interpreters
      : [
          { key: draft.interpreter, path: "", installed: false },
          ...interpreters,
        ];
  const selected = options.find((o) => o.key === draft.interpreter);

  return (
    <div className="flex flex-col gap-4">
      <SectionCard
        icon={Timer}
        title={t("hooks.trigger.title")}
        subtitle={t("hooks.trigger.subtitle")}
      >
        <div className="flex flex-wrap items-end gap-3">
          <label className="flex flex-col gap-1">
            <span className="text-2xs text-muted-foreground">
              {t("hooks.trigger.cronLabel")}
            </span>
            <Input
              value={draft.scheduleExpr}
              onChange={(e) =>
                onChange({ ...draft, scheduleExpr: e.target.value })
              }
              className="w-44 font-mono text-xs"
              aria-label={t("hooks.trigger.cronLabel")}
            />
          </label>
          <label className="flex flex-col gap-1">
            <span className="text-2xs text-muted-foreground">
              {t("hooks.trigger.interpreter")}
            </span>
            <Select
              value={draft.interpreter}
              onValueChange={(v) => onChange({ ...draft, interpreter: v })}
            >
              <SelectTrigger
                className="w-40"
                aria-label={t("hooks.trigger.interpreter")}
              >
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                {options.map((opt) => (
                  <SelectItem
                    key={opt.key}
                    value={opt.key}
                    disabled={!opt.installed}
                  >
                    {t(`hooks.interp.${opt.key}`)}
                    {!opt.installed && (
                      <span className="ml-1.5 text-3xs text-muted-foreground">
                        {t("hooks.interp.notInstalled")}
                      </span>
                    )}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
          </label>
          <label className="flex flex-col gap-1">
            <span className="text-2xs text-muted-foreground">
              {t("hooks.trigger.interpreterPath")}
            </span>
            <Input
              value={draft.interpreterPath}
              onChange={(e) =>
                onChange({ ...draft, interpreterPath: e.target.value })
              }
              placeholder={
                selected?.installed && selected.path
                  ? t("hooks.trigger.interpreterPathAuto", {
                      path: selected.path,
                    })
                  : t("hooks.trigger.interpreterPathPlaceholder")
              }
              className="w-72 font-mono text-xs"
              aria-label={t("hooks.trigger.interpreterPath")}
            />
          </label>
          <label className="flex flex-col gap-1">
            <span className="text-2xs text-muted-foreground">
              {t("hooks.trigger.timezone")}
            </span>
            <Select
              value={draft.timezone}
              onValueChange={(v) => onChange({ ...draft, timezone: v })}
            >
              <SelectTrigger
                className="w-44"
                aria-label={t("hooks.trigger.timezone")}
              >
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                {TZ_OPTIONS.map((v) => (
                  <SelectItem key={v} value={v}>
                    {v}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
          </label>
        </div>
      </SectionCard>

      <SectionCard
        icon={Terminal}
        title={t("hooks.script.title")}
        subtitle={t("hooks.script.subtitle")}
      >
        <label className="flex flex-col gap-1">
          <span className="text-2xs text-muted-foreground">
            {t("hooks.script.name")}
          </span>
          <Input
            value={draft.name}
            onChange={(e) => onChange({ ...draft, name: e.target.value })}
            placeholder={t("hooks.script.namePlaceholder")}
            aria-label={t("hooks.script.name")}
          />
        </label>
        <Textarea
          value={draft.command}
          onChange={(e) => onChange({ ...draft, command: e.target.value })}
          placeholder={t("hooks.script.commandPlaceholder")}
          aria-label={t("hooks.script.title")}
          spellCheck={false}
          className="min-h-56 rounded-md border-border bg-code-surface font-mono text-xs leading-relaxed text-code-foreground"
        />
      </SectionCard>

      <SectionCard
        icon={KeyRound}
        title={t("hooks.env.title")}
        subtitle={t("hooks.env.subtitle")}
        action={
          <Button
            type="button"
            variant="ghost"
            size="sm"
            className="h-7 font-mono text-2xs"
            onClick={() =>
              setEnv([...draft.env, { key: "", value: "", secret: false }])
            }
          >
            <Plus className="mr-1 h-3.5 w-3.5" />
            {t("hooks.env.add")}
          </Button>
        }
      >
        {draft.env.length === 0 ? (
          <p className="py-1 text-xs text-muted-foreground">
            {t("hooks.env.empty")}
          </p>
        ) : (
          draft.env.map((row, i) => (
            <div key={i} className="flex items-center gap-2">
              <Input
                value={row.key}
                onChange={(e) =>
                  setEnv(
                    draft.env.map((r, j) =>
                      j === i ? { ...r, key: e.target.value } : r,
                    ),
                  )
                }
                placeholder={t("hooks.env.keyPlaceholder")}
                aria-label={t("hooks.env.keyPlaceholder")}
                className="w-40 font-mono text-xs"
              />
              <Input
                type={row.secret ? "password" : "text"}
                value={row.value}
                onChange={(e) =>
                  setEnv(
                    draft.env.map((r, j) =>
                      j === i ? { ...r, value: e.target.value } : r,
                    ),
                  )
                }
                placeholder={t("hooks.env.valuePlaceholder")}
                aria-label={t("hooks.env.valuePlaceholder")}
                className="flex-1 font-mono text-xs"
              />
              <label className="flex items-center gap-1.5 text-2xs text-muted-foreground">
                <Switch
                  checked={row.secret}
                  onCheckedChange={(checked) =>
                    setEnv(
                      draft.env.map((r, j) =>
                        j === i ? { ...r, secret: checked } : r,
                      ),
                    )
                  }
                  aria-label={t("hooks.env.secret")}
                />
                {t("hooks.env.secret")}
              </label>
              <Button
                type="button"
                variant="ghost"
                size="icon"
                className="h-8 w-8 text-muted-foreground"
                aria-label={t("hooks.env.remove")}
                onClick={() => setEnv(draft.env.filter((_, j) => j !== i))}
              >
                <Trash2 className="h-3.5 w-3.5" />
              </Button>
            </div>
          ))
        )}
      </SectionCard>
    </div>
  );
}

// ── Run log tab (events + payload detail) ────────────────────────────────────
