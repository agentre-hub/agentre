import { useEffect, useState } from "react";

import { Info } from "../../../../wailsjs/go/app/App";
import { useUpdateStore } from "@/stores/update-store";
import { latestKnownVersion, type DesktopBuild } from "./agentred-version";

/**
 * 「桌面端已知的最新 agentred 发布版本」,空串表示不知道。
 *
 * 两个来源都是本机已有的事实,这里不另开一条发布信息来源:
 * - update store 里 update_svc 最近一次检查的结果(它查的就是同一条发布流);
 * - 桌面端自身的构建标识 —— 检查说「已是最新」时结果里不再带版本号,而桌面端
 *   自己正跑在一个已发布的版本上,这是「确实存在这么新的一版」的直接证据。
 *
 * 判定与取舍在 latestKnownVersion 里,这里只负责把两个来源接上宿主。
 */
export function useLatestAgentredVersion(): string {
  const checked = useUpdateStore((s) =>
    "info" in s.phase ? s.phase.info.latestVersion : "",
  );
  const [desktop, setDesktop] = useState<DesktopBuild>({
    version: "",
    commit: "",
  });

  useEffect(() => {
    let alive = true;
    void (async () => {
      try {
        const info = await Info();
        if (!alive) return;
        setDesktop({
          version: info?.version ?? "",
          commit: info?.commit ?? "",
        });
      } catch {
        // 浏览器预览模式下 Wails 绑定不存在:当作不知道,而不是编一个版本。
      }
    })();
    return () => {
      alive = false;
    };
  }, []);

  return latestKnownVersion(checked, desktop);
}
