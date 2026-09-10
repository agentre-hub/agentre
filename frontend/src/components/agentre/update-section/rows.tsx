// 更新区块里的几行设置项(都长成「左边说明 + 右边控件」的样子):
// 标题行、仓库行、渠道行、镜像行、调试行。

import * as React from "react";
import { useTranslation } from "react-i18next";
import { ExternalLink } from "lucide-react";

import {
  Input,
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
  Switch,
} from "@agentre-hub/agentre-ui";

import { BrowserOpenURL } from "../../../../wailsjs/runtime/runtime";

import { type MirrorInfo, type UpdateChannel } from "../update-api";

import {
  CHANNEL_LABEL,
  CHANNEL_DESC,
  REPOSITORY_URL,
  MIRROR_CUSTOM_ID,
} from "./format";

export function SectionHeader() {
  const { t } = useTranslation();

  return (
    <div className="flex max-w-3xl flex-col gap-1.5">
      <h1 className="text-2xl font-semibold tracking-normal">
        {t("update.header.title")}
      </h1>
      <p className="text-sm leading-relaxed text-muted-foreground">
        {t("update.header.description")}
      </p>
    </div>
  );
}

export function RepositoryRow() {
  const { t } = useTranslation();
  const handleClick = React.useCallback(
    (event: React.MouseEvent<HTMLAnchorElement>) => {
      event.preventDefault();
      BrowserOpenURL(REPOSITORY_URL);
    },
    [],
  );

  return (
    <div className="flex flex-col gap-3 sm:flex-row sm:items-center sm:justify-between">
      <div className="flex min-w-0 flex-col gap-0.5">
        <span className="text-sm font-medium">
          {t("update.repository.title")}
        </span>
        <p className="text-xs leading-relaxed text-muted-foreground">
          {t("update.repository.description")}
        </p>
      </div>
      <a
        href={REPOSITORY_URL}
        target="_blank"
        rel="noreferrer"
        onClick={handleClick}
        className="inline-flex min-w-0 items-center gap-1.5 text-xs font-medium text-agent-1 underline-offset-4 hover:underline sm:max-w-[320px]"
      >
        <span className="truncate">{REPOSITORY_URL}</span>
        <ExternalLink className="size-3 shrink-0" aria-hidden="true" />
      </a>
    </div>
  );
}

export function DebugRow({
  enabled,
  onToggle,
}: {
  enabled: boolean;
  onToggle: (next: boolean) => void;
}) {
  const { t } = useTranslation();
  const labelId = React.useId();
  return (
    <div className="flex flex-col gap-3 sm:flex-row sm:items-center sm:justify-between">
      <div className="flex min-w-0 flex-col gap-0.5">
        <span id={labelId} className="text-sm font-medium">
          {t("update.debug.title")}
        </span>
        <p className="text-xs leading-relaxed text-muted-foreground">
          {t("update.debug.description")}
        </p>
      </div>
      <Switch
        checked={enabled}
        onCheckedChange={onToggle}
        aria-labelledby={labelId}
      />
    </div>
  );
}

export function ChannelRow({
  channel,
  onChange,
  disabled,
}: {
  channel: UpdateChannel;
  onChange: (next: string) => void;
  disabled: boolean;
}) {
  const { t } = useTranslation();
  const labelId = React.useId();
  return (
    <div className="flex flex-col gap-3 sm:flex-row sm:items-center sm:justify-between">
      <div className="flex min-w-0 flex-col gap-0.5">
        <span id={labelId} className="text-sm font-medium">
          {t("update.channel.title")}
        </span>
        <p className="text-xs leading-relaxed text-muted-foreground">
          {t(CHANNEL_DESC[channel])}
        </p>
      </div>
      <div className="w-full sm:w-[220px]">
        <Select value={channel} onValueChange={onChange} disabled={disabled}>
          <SelectTrigger aria-labelledby={labelId}>
            <SelectValue />
          </SelectTrigger>
          <SelectContent>
            {(["stable", "beta", "nightly"] as const).map((c) => (
              <SelectItem key={c} value={c}>
                {t(CHANNEL_LABEL[c])}
              </SelectItem>
            ))}
          </SelectContent>
        </Select>
      </div>
    </div>
  );
}

export function MirrorRow({
  mirrors,
  selectValue,
  customDraft,
  onSelectChange,
  onCustomChange,
  onCustomBlur,
  disabled,
}: {
  mirrors: MirrorInfo[];
  selectValue: string;
  customDraft: string;
  onSelectChange: (v: string) => void;
  onCustomChange: (v: string) => void;
  onCustomBlur: () => void;
  disabled: boolean;
}) {
  const { t } = useTranslation();
  const labelId = React.useId();
  return (
    <div className="flex flex-col gap-3 sm:flex-row sm:items-start sm:justify-between">
      <div className="flex min-w-0 flex-col gap-0.5 sm:max-w-[300px]">
        <span id={labelId} className="text-sm font-medium">
          {t("update.mirror.title")}
        </span>
        <p className="text-xs leading-relaxed text-muted-foreground">
          {t("update.mirror.description")}
        </p>
      </div>
      <div className="flex w-full flex-col gap-2 sm:w-[260px]">
        <Select
          value={selectValue}
          onValueChange={onSelectChange}
          disabled={disabled}
        >
          <SelectTrigger aria-labelledby={labelId}>
            <SelectValue />
          </SelectTrigger>
          <SelectContent>
            {mirrors.map((m) => (
              <SelectItem key={m.id} value={m.id}>
                {m.name}
              </SelectItem>
            ))}
            <SelectItem value={MIRROR_CUSTOM_ID}>
              {t("update.mirror.custom")}
            </SelectItem>
          </SelectContent>
        </Select>
        {selectValue === MIRROR_CUSTOM_ID ? (
          <Input
            type="url"
            value={customDraft}
            onChange={(e: React.ChangeEvent<HTMLInputElement>) =>
              onCustomChange(e.target.value)
            }
            onBlur={onCustomBlur}
            placeholder="https://your.mirror/"
            disabled={disabled}
          />
        ) : null}
      </div>
    </div>
  );
}
