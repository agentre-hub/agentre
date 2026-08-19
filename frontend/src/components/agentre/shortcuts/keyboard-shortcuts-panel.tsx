import React from "react";
import { TriangleAlert } from "lucide-react";
import { useTranslation } from "react-i18next";

import { Button } from "@/components/ui/button";
import {
  Dialog,
  DialogBody,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { cn } from "@/lib/utils";

import { chordFromEvent, formatChord } from "./format";
import { REGISTRY, TAB_CHIP_IDS, TAB_CLOSE_ID, getDef } from "./registry";
import { useShortcutsContext } from "./shortcuts-provider";
import type { KeyChord, ShortcutDef } from "./types";

const CONFLICT_TOAST_MS = 4000;

type ConflictMessage = {
  text: string;
  highlightId?: string;
};

function ShortcutRow({
  def,
  chord,
  chordLabel,
  recording,
  highlighted,
  fixed,
  onStartRecord,
  onCancelRecord,
}: {
  def: ShortcutDef;
  chord: KeyChord | null;
  // chordLabel —— 直接渲染的按键文案，用于无法表达成 KeyChord 的固定快捷键
  // （如 Ctrl+Tab，"Tab" 不在 ACCEPTABLE_KEY 集里）。给出时优先于 formatChord。
  chordLabel?: string;
  recording: boolean;
  highlighted: boolean;
  fixed?: boolean;
  onStartRecord: () => void;
  onCancelRecord: () => void;
}) {
  const { t } = useTranslation();
  const { platform } = useShortcutsContext();
  return (
    <div
      data-slot="shortcut-row"
      className={cn(
        "flex items-center justify-between gap-4 rounded-lg border border-border bg-card px-4 py-3 transition-colors",
        recording && "bg-primary-soft/40",
        highlighted && "border-destructive bg-destructive/10",
      )}
    >
      <div className="flex min-w-0 flex-col gap-0.5">
        <span className="text-sm font-medium text-foreground">{def.label}</span>
        <span className="text-xs text-muted-foreground">{def.hint}</span>
      </div>
      <div className="flex items-center gap-3">
        <span
          className={cn(
            "inline-flex h-7 min-w-[64px] items-center justify-center rounded-md border border-border bg-muted px-2.5 font-mono text-2xs font-semibold tracking-wide text-foreground",
            recording &&
              "border-dashed border-primary/60 bg-background text-primary",
          )}
        >
          {recording
            ? t("shortcuts.recording")
            : chordLabel
              ? chordLabel
              : chord
                ? formatChord(chord, platform)
                : t("shortcuts.unbound")}
        </span>
        {fixed ? (
          <span className="rounded-md bg-muted px-2 py-1 text-2xs font-semibold uppercase tracking-wide text-muted-foreground">
            {t("shortcuts.fixed")}
          </span>
        ) : recording ? (
          <Button
            type="button"
            variant="destructive"
            size="sm"
            onClick={onCancelRecord}
          >
            {t("common.cancel")}
          </Button>
        ) : (
          <Button
            type="button"
            variant="outline"
            size="sm"
            onClick={onStartRecord}
          >
            {t("shortcuts.record")}
          </Button>
        )}
      </div>
    </div>
  );
}

function ConflictBanner({ message }: { message: ConflictMessage }) {
  return (
    <div
      role="alert"
      className="flex items-center gap-3 rounded-md border border-destructive bg-destructive/10 px-4 py-3 text-sm text-destructive shadow-sm"
    >
      <TriangleAlert className="size-4 shrink-0" aria-hidden="true" />
      <span className="font-medium">{message.text}</span>
    </div>
  );
}

export function KeyboardShortcutsPanel(): React.ReactElement {
  const { t } = useTranslation();
  const {
    bindings,
    platform,
    setBinding,
    setPaused,
    resetAll,
    findChordConflict,
  } = useShortcutsContext();

  const [recordingId, setRecordingId] = React.useState<string | null>(null);
  const [conflict, setConflict] = React.useState<ConflictMessage | null>(null);
  const [resetOpen, setResetOpen] = React.useState(false);

  const conflictTimerRef = React.useRef<number | null>(null);
  const showConflict = React.useCallback((msg: ConflictMessage) => {
    setConflict(msg);
    if (conflictTimerRef.current !== null) {
      window.clearTimeout(conflictTimerRef.current);
    }
    conflictTimerRef.current = window.setTimeout(
      () => setConflict(null),
      CONFLICT_TOAST_MS,
    );
  }, []);

  React.useEffect(() => {
    return () => {
      if (conflictTimerRef.current !== null) {
        window.clearTimeout(conflictTimerRef.current);
      }
    };
  }, []);

  // Pause the global state machine while recording so chords don't navigate.
  React.useEffect(() => {
    setPaused(recordingId !== null);
    return () => setPaused(false);
  }, [recordingId, setPaused]);

  // Listen for the new chord while recording.
  React.useEffect(() => {
    if (!recordingId) return;
    function handleKeyDown(event: KeyboardEvent) {
      if (event.key === "Escape") {
        event.preventDefault();
        setRecordingId(null);
        return;
      }
      const chord = chordFromEvent(event, platform);
      if (!chord) return;
      event.preventDefault();

      const conflictResult = findChordConflict(chord, recordingId!);
      if (conflictResult?.type === "system") {
        showConflict({ text: t("shortcuts.conflict.systemReserved") });
        return;
      }
      if (conflictResult?.type === "binding") {
        const other = getDef(conflictResult.id);
        showConflict({
          text: t("shortcuts.conflict.inUse", {
            label: other?.label ?? conflictResult.id,
          }),
          highlightId: conflictResult.id,
        });
        return;
      }
      setBinding(recordingId!, chord);
      setRecordingId(null);
    }
    window.addEventListener("keydown", handleKeyDown);
    return () => window.removeEventListener("keydown", handleKeyDown);
  }, [recordingId, platform, findChordConflict, setBinding, showConflict, t]);

  const globalDefs = REGISTRY.filter((def) => def.scope === "global");
  const chatTabDef = getDef(TAB_CHIP_IDS[0]!)!;
  const tabCloseDef = getDef(TAB_CLOSE_ID)!;

  // Ctrl+Tab 循环切换 —— 固定、不可重绑（Ctrl 在 macOS 不是主修饰键，"Tab" 也不是
  // 可绑定键，无法表达成 KeyChord），因此直接给出按键文案。macOS 用 ⌃ 控制键符号。
  const isMac = platform === "darwin";
  const cycleFwdLabel = isMac ? "⌃Tab" : "Ctrl+Tab";
  const cycleBackLabel = isMac ? "⌃⇧Tab" : "Ctrl+Shift+Tab";

  const performReset = () => {
    resetAll();
    setRecordingId(null);
    setConflict(null);
    setResetOpen(false);
  };

  return (
    <>
      <div className="flex max-w-3xl flex-col gap-1.5">
        <h1 className="text-2xl font-semibold tracking-normal">
          {t("shortcuts.title")}
        </h1>
        <p className="text-sm leading-relaxed text-muted-foreground">
          {t("shortcuts.description")}
        </p>
      </div>

      <section className="flex flex-col gap-3">
        <h2 className="text-2xs font-semibold uppercase tracking-[0.08em] text-muted-foreground">
          {t("shortcuts.sections.global")}
        </h2>
        {globalDefs.map((def) => (
          <ShortcutRow
            key={def.id}
            def={def}
            chord={bindings.get(def.id) ?? null}
            recording={recordingId === def.id}
            highlighted={conflict?.highlightId === def.id}
            onStartRecord={() => {
              setRecordingId(def.id);
              setConflict(null);
            }}
            onCancelRecord={() => setRecordingId(null)}
          />
        ))}
      </section>

      <section className="flex flex-col gap-3">
        <h2 className="text-2xs font-semibold uppercase tracking-[0.08em] text-muted-foreground">
          {t("shortcuts.sections.chat")}
        </h2>
        <ShortcutRow
          fixed
          def={{
            ...chatTabDef,
            label: t("shortcuts.chatTab.label"),
            hint: `${formatChord(
              { mod: "primary", key: "1" },
              platform,
            )} - ${formatChord(
              { mod: "primary", key: "9" },
              platform,
            )} · ${t("shortcuts.chatTab.hint")}`,
          }}
          chord={bindings.get(chatTabDef.id) ?? null}
          recording={false}
          highlighted={false}
          onStartRecord={() => {}}
          onCancelRecord={() => {}}
        />
        <ShortcutRow
          fixed
          def={{
            id: "chat.tab.cycle",
            label: t("shortcuts.chatTabCycle.label"),
            hint: `${cycleFwdLabel} / ${cycleBackLabel} · ${t(
              "shortcuts.chatTabCycle.hint",
            )}`,
            scope: "tabs",
            defaultBinding: { mod: "primary", key: "" },
            rebindable: false,
          }}
          chord={null}
          chordLabel={cycleFwdLabel}
          recording={false}
          highlighted={false}
          onStartRecord={() => {}}
          onCancelRecord={() => {}}
        />
        <ShortcutRow
          fixed
          def={tabCloseDef}
          chord={bindings.get(tabCloseDef.id) ?? null}
          recording={false}
          highlighted={false}
          onStartRecord={() => {}}
          onCancelRecord={() => {}}
        />
      </section>

      <div className="flex items-center justify-end gap-3">
        {conflict ? <ConflictBanner message={conflict} /> : null}
        <Button
          type="button"
          variant="outline"
          size="sm"
          onClick={() => setResetOpen(true)}
        >
          {t("shortcuts.resetAll")}
        </Button>
      </div>

      <Dialog open={resetOpen} onOpenChange={setResetOpen}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>{t("shortcuts.resetDialog.title")}</DialogTitle>
            <DialogDescription>
              {t("shortcuts.resetDialog.description")}
            </DialogDescription>
          </DialogHeader>
          <DialogBody>
            <p className="text-sm text-muted-foreground">
              {t("shortcuts.resetDialog.storagePrefix")}
              <code className="mx-1 rounded bg-muted px-1.5 py-0.5 font-mono text-2xs">
                agentre.shortcuts
              </code>
              {t("shortcuts.resetDialog.storageSuffix")}
            </p>
          </DialogBody>
          <DialogFooter>
            <Button
              type="button"
              variant="ghost"
              size="sm"
              onClick={() => setResetOpen(false)}
            >
              {t("common.cancel")}
            </Button>
            <Button
              type="button"
              variant="destructive"
              size="sm"
              onClick={performReset}
            >
              {t("shortcuts.reset")}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </>
  );
}
