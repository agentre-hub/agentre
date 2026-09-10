import * as React from "react";
import { ArrowRight, Info } from "lucide-react";
import { useTranslation } from "react-i18next";
import { useNavigate } from "react-router-dom";

import {
  Alert,
  AlertTitle,
  Button,
  Dialog,
  DialogBody,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@agentre-hub/agentre-ui";
import type { AgentSlim } from "@/hooks/use-chat-agents";

import {
  blockReasonToCta,
  ORG_SELECTED_STORAGE_KEY,
  type BlockReason,
  type GuidanceTarget,
} from "./mapping";

type NotChattableAgent = Pick<AgentSlim, "id" | "name" | "blockReason">;

type NotChattableDialogProps = {
  agent: NotChattableAgent;
  onOpenChange: (open: boolean) => void;
  open: boolean;
};

type ChainState =
  | "configured"
  | "inactive"
  | "missing"
  | "possible"
  | "unknown"
  | "unavailable";

function getChainState(
  blockReason: BlockReason | string,
  node: "backend" | "provider",
): ChainState {
  if (node === "backend") {
    if (blockReason === "no-backend") return "missing";
    if (
      blockReason === "gateway-not-running" ||
      blockReason === "remote-openclaw-unavailable"
    ) {
      return "unavailable";
    }
    if (blockReason === "unknown-backend") return "unknown";
    return "configured";
  }

  if (blockReason === "no-backend") return "possible";
  if (blockReason === "backend-requires-provider") return "missing";
  if (blockReason === "provider-inactive") return "inactive";
  if (blockReason === "remote-provider-missing") return "missing";
  return "configured";
}

function navigateToTarget(
  navigate: ReturnType<typeof useNavigate>,
  target: GuidanceTarget,
  agentId: number,
) {
  if (target === "org-agent:<id>") {
    localStorage.setItem(
      ORG_SELECTED_STORAGE_KEY,
      JSON.stringify({ kind: "agent", id: agentId }),
    );
    navigate("/org", {
      state: { orgSelection: { kind: "agent", id: agentId } },
    });
    return;
  }

  navigate("/settings", {
    state: { settingsPage: target.slice("settings:".length) },
  });
}

function NotChattableDialog({
  agent,
  onOpenChange,
  open,
}: NotChattableDialogProps) {
  const { t } = useTranslation();
  const navigate = useNavigate();
  const cta = blockReasonToCta(agent.blockReason);
  const reasonKey = cta.copyKey;
  const blockReason = agent.blockReason as BlockReason;
  const backendState = getChainState(blockReason, "backend");
  const providerState = getChainState(blockReason, "provider");

  const closeAndNavigate = React.useCallback(
    (target: GuidanceTarget) => {
      onOpenChange(false);
      navigateToTarget(navigate, target, agent.id);
    },
    [agent.id, navigate, onOpenChange],
  );

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>
            {t(`${reasonKey}.title`, { name: agent.name })}
          </DialogTitle>
          <DialogDescription>
            {t(`${reasonKey}.description`, { name: agent.name })}
          </DialogDescription>
        </DialogHeader>
        <DialogBody className="space-y-4">
          <div
            aria-label={t("chatPage.notChattable.chain.aria")}
            className="flex items-center gap-2 rounded-lg border border-border bg-secondary/40 p-3 text-xs"
            data-testid="not-chattable-chain"
          >
            <ChainNode
              label={t("chatPage.notChattable.chain.backend")}
              state={backendState}
              stateLabel={t(
                `chatPage.notChattable.chain.states.${backendState}`,
              )}
            />
            <ArrowRight
              className="size-4 shrink-0 text-muted-foreground"
              aria-hidden="true"
            />
            <ChainNode
              label={t("chatPage.notChattable.chain.provider")}
              state={providerState}
              stateLabel={t(
                `chatPage.notChattable.chain.states.${providerState}`,
              )}
            />
          </div>
          {blockReason === "no-backend" ? (
            <Alert className="border-primary/30 bg-primary-soft/60">
              <Info aria-hidden="true" />
              <AlertTitle>{t("chatPage.notChattable.info.title")}</AlertTitle>
            </Alert>
          ) : null}
        </DialogBody>
        <DialogFooter>
          <Button
            type="button"
            variant="ghost"
            onClick={() => onOpenChange(false)}
          >
            {t("common.cancel")}
          </Button>
          <Button
            type="button"
            variant="outline"
            onClick={() => closeAndNavigate("settings:agent-backend")}
          >
            {t(cta.secondaryLabel)}
          </Button>
          <Button
            type="button"
            onClick={() => closeAndNavigate(cta.primaryTarget)}
          >
            {t(cta.primaryLabel)}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}

function ChainNode({
  label,
  state,
  stateLabel,
}: {
  label: string;
  state: ChainState;
  stateLabel: string;
}) {
  return (
    <span
      className={
        state === "configured"
          ? "rounded-md border border-status-running/30 bg-status-running-bg px-2 py-1 text-status-running"
          : state === "possible"
            ? "rounded-md border border-border bg-background px-2 py-1 text-muted-foreground"
            : "rounded-md border border-status-waiting/40 bg-status-waiting-bg px-2 py-1 font-medium text-foreground"
      }
      data-chain-state={state}
    >
      {label} · {stateLabel}
    </span>
  );
}

export { NotChattableDialog, navigateToTarget };
export type { NotChattableAgent, NotChattableDialogProps };
