import { useUiTranslation as useTranslation } from "../i18n";
import { RadioTower, ShieldCheck } from "lucide-react";

import { Alert, AlertDescription, AlertTitle } from "../ui/alert";
import { Badge } from "../ui/badge";
import { Field, FieldDescription, FieldGroup, FieldLabel } from "../ui/field";
import { Input } from "../ui/input";
import {
  Select,
  SelectContent,
  SelectGroup,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "../ui/select";
import { Switch } from "../ui/switch";

import type { agent_backend_svc } from "./port-bridge";

export const OPENCLAW_DEFAULT_GATEWAY_URL = "ws://127.0.0.1:18789";
export const OPENCLAW_SESSION_MODE = "per-agentre-session";

export function OpenClawBackendFields({
  gatewayURL,
  onGatewayURLChange,
  canEditToken,
  token,
  onTokenChange,
  hasToken,
  clearToken,
  onClearTokenChange,
  agentID,
  onAgentIDChange,
  defaultModel,
  onDefaultModelChange,
  probe,
}: {
  gatewayURL: string;
  onGatewayURLChange: (value: string) => void;
  /** token 只落在那台机器的安全存储里；宿主写不进去时整块都不渲染。 */
  canEditToken: boolean;
  token: string;
  onTokenChange: (value: string) => void;
  hasToken: boolean;
  clearToken: boolean;
  onClearTokenChange: (value: boolean) => void;
  agentID: string;
  onAgentIDChange: (value: string) => void;
  defaultModel: string;
  onDefaultModelChange: (value: string) => void;
  probe: agent_backend_svc.TestBackendResponse | null;
}) {
  const { t } = useTranslation();
  const agents = probe?.openClawAgents ?? [];
  const models = probe?.openClawModels ?? [];
  // 网关只对 operator.admin 连接放行 provider/model override(实测非 admin 调用会被
  // 直接拒:"provider/model overrides are not authorized for this caller"),运行时也
  // 因此只在拿到 admin scope 时才下发 model。探测已经带回授予的 scope,这里据此
  // 说明清楚,免得用户选了一个永远不生效的模型。
  const modelOverrideAllowed =
    probe?.ok === true &&
    (probe.grantedScopes ?? []).includes("operator.admin");
  const modelOverrideBlocked = probe?.ok === true && !modelOverrideAllowed;
  const selectedAgent = agents.some((agent) => agent.id === agentID)
    ? agentID
    : "";
  const selectedModel = models.some((model) => model.id === defaultModel)
    ? defaultModel
    : "";

  return (
    <FieldGroup className="gap-4">
      <Alert>
        <ShieldCheck aria-hidden="true" />
        <AlertTitle>{t("agentBackends.openclaw.localOnlyTitle")}</AlertTitle>
        <AlertDescription>
          {t("agentBackends.openclaw.remoteUnavailable")}
        </AlertDescription>
      </Alert>

      <Field>
        <FieldLabel htmlFor="openclaw-gateway-url">
          {t("agentBackends.openclaw.gatewayURL")}
        </FieldLabel>
        <Input
          id="openclaw-gateway-url"
          value={gatewayURL}
          onChange={(event) => onGatewayURLChange(event.target.value)}
          placeholder={OPENCLAW_DEFAULT_GATEWAY_URL}
          required
          spellCheck={false}
        />
        <FieldDescription>
          {t("agentBackends.openclaw.gatewayURLHint")}
        </FieldDescription>
      </Field>

      {canEditToken ? (
        <Field data-disabled={clearToken || undefined}>
          <FieldLabel htmlFor="openclaw-token">
            {t("agentBackends.openclaw.token")}
          </FieldLabel>
          <Input
            id="openclaw-token"
            type="password"
            autoComplete="new-password"
            value={token}
            disabled={clearToken}
            onChange={(event) => onTokenChange(event.target.value)}
            placeholder={
              hasToken
                ? t("agentBackends.openclaw.tokenSavedPlaceholder")
                : t("agentBackends.openclaw.tokenPlaceholder")
            }
          />
          <FieldDescription>
            {hasToken
              ? t("agentBackends.openclaw.tokenSavedHint")
              : t("agentBackends.openclaw.tokenHint")}
          </FieldDescription>
        </Field>
      ) : null}

      {canEditToken && hasToken ? (
        <Field orientation="horizontal">
          <FieldLabel htmlFor="openclaw-clear-token">
            {t("agentBackends.openclaw.clearToken")}
          </FieldLabel>
          <Switch
            id="openclaw-clear-token"
            checked={clearToken}
            onCheckedChange={onClearTokenChange}
            aria-label={t("agentBackends.openclaw.clearToken")}
          />
        </Field>
      ) : null}

      <Field>
        <FieldLabel>{t("agentBackends.openclaw.sessionMode")}</FieldLabel>
        <Badge variant="secondary" className="self-start">
          {t("agentBackends.openclaw.sessionPerAgentRE")}
        </Badge>
        <FieldDescription>
          {t("agentBackends.openclaw.sessionModeHint")}
        </FieldDescription>
      </Field>

      <Field>
        <FieldLabel htmlFor="openclaw-agent">
          {t("agentBackends.openclaw.agent")}
        </FieldLabel>
        {agents.length > 0 ? (
          <Select value={selectedAgent} onValueChange={onAgentIDChange}>
            <SelectTrigger
              id="openclaw-agent"
              aria-label={t("agentBackends.openclaw.agent")}
            >
              <SelectValue
                placeholder={t("agentBackends.openclaw.agentPlaceholder")}
              />
            </SelectTrigger>
            <SelectContent>
              <SelectGroup>
                {agents.map((agent) => (
                  <SelectItem key={agent.id} value={agent.id}>
                    {agent.name || agent.id} · {agent.id}
                  </SelectItem>
                ))}
              </SelectGroup>
            </SelectContent>
          </Select>
        ) : (
          <Input
            id="openclaw-agent"
            value={agentID}
            onChange={(event) => onAgentIDChange(event.target.value)}
            placeholder={t("agentBackends.openclaw.agentPlaceholder")}
          />
        )}
      </Field>

      <Field>
        <FieldLabel htmlFor="openclaw-model">
          {t("agentBackends.openclaw.model")}
        </FieldLabel>
        {models.length > 0 ? (
          <Select
            value={selectedModel}
            onValueChange={onDefaultModelChange}
            disabled={modelOverrideBlocked}
          >
            <SelectTrigger
              id="openclaw-model"
              aria-label={t("agentBackends.openclaw.model")}
            >
              <SelectValue
                placeholder={t("agentBackends.openclaw.modelPlaceholder")}
              />
            </SelectTrigger>
            <SelectContent>
              <SelectGroup>
                {models.map((model) => (
                  <SelectItem
                    key={model.id}
                    value={model.id}
                    disabled={!model.available}
                  >
                    {model.name || model.id} · {model.id}
                  </SelectItem>
                ))}
              </SelectGroup>
            </SelectContent>
          </Select>
        ) : (
          <Input
            id="openclaw-model"
            value={defaultModel}
            onChange={(event) => onDefaultModelChange(event.target.value)}
            placeholder={t("agentBackends.openclaw.modelPlaceholder")}
            disabled={modelOverrideBlocked}
          />
        )}
        {modelOverrideBlocked ? (
          <FieldDescription>
            {t("agentBackends.openclaw.modelOverrideUnauthorized")}
          </FieldDescription>
        ) : null}
      </Field>

      {probe?.ok ? (
        <Alert>
          <RadioTower aria-hidden="true" />
          <AlertTitle>{t("agentBackends.openclaw.probeVerified")}</AlertTitle>
          <AlertDescription className="flex flex-col gap-2">
            <div className="flex flex-wrap items-center gap-2">
              <Badge variant="secondary">{probe.gatewayVersion}</Badge>
              <Badge variant="outline">
                {t("agentBackends.openclaw.protocol", {
                  protocol: probe.protocol,
                })}
              </Badge>
            </div>
            <div className="flex flex-wrap items-center gap-1.5">
              {(probe.grantedScopes ?? []).map((scope) => (
                <Badge key={scope} variant="outline">
                  {scope}
                </Badge>
              ))}
            </div>
          </AlertDescription>
        </Alert>
      ) : null}
    </FieldGroup>
  );
}
