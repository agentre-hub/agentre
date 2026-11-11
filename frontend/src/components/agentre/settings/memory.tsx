import * as React from "react";
import { useTranslation } from "react-i18next";

import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@agentre-hub/agentre-ui";

import {
  GetAppSetting,
  UpdateAppSettings,
} from "../../../../wailsjs/go/app/App";
import type { app_settings_svc } from "../../../../wailsjs/go/models";

// 只存本机（app_settings），不参与同步；缺省保留。
const KEY = "memory.claude_native";

type NativeMemory = "keep" | "off";

async function readNativeMemory(): Promise<NativeMemory> {
  try {
    const r = await GetAppSetting({ key: KEY });
    return r?.value === "false" ? "off" : "keep";
  } catch {
    // 键不存在（AppSettingNotFound）= 没设置过 = 保留。
    return "keep";
  }
}

/** 设置 → 记忆：Claude Code 自带记忆「保留 / 关闭」。 */
export function MemorySettings() {
  const { t } = useTranslation();
  const labelId = React.useId();
  const [value, setValue] = React.useState<NativeMemory>("keep");

  React.useEffect(() => {
    let alive = true;
    void readNativeMemory().then((v) => {
      if (alive) setValue(v);
    });
    return () => {
      alive = false;
    };
  }, []);

  const save = async (next: NativeMemory) => {
    await UpdateAppSettings({
      entries: [{ key: KEY, value: next === "keep" ? "true" : "false" }],
    } as app_settings_svc.UpdateRequest);
    setValue(next);
  };

  return (
    <div className="flex min-w-0 flex-col gap-4">
      <section className="overflow-hidden rounded-lg bg-field-fill">
        <header className="px-4 py-3">
          <h2 className="text-sm font-semibold">
            {t("settings.memory.nativeTitle")}
          </h2>
          <p className="text-2xs leading-relaxed text-muted-foreground">
            {t("settings.memory.nativeDesc")}
          </p>
        </header>
        <div className="flex items-center gap-4 px-4 py-3">
          <div className="min-w-0 flex-1">
            <div id={labelId} className="text-xs font-medium">
              {t("settings.memory.native.label")}
            </div>
            <div className="text-2xs leading-relaxed text-muted-foreground">
              {t("settings.memory.native.desc")}
            </div>
          </div>
          <div className="w-[220px] shrink-0">
            <Select
              value={value}
              onValueChange={(v) => void save(v as NativeMemory)}
            >
              <SelectTrigger
                aria-label={t("settings.memory.native.label")}
                aria-labelledby={labelId}
              >
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                <SelectItem value="keep">
                  {t("settings.memory.native.keep")}
                </SelectItem>
                <SelectItem value="off">
                  {t("settings.memory.native.off")}
                </SelectItem>
              </SelectContent>
            </Select>
          </div>
        </div>
      </section>
    </div>
  );
}
