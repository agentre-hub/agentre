import * as React from "react";
import { useTranslation } from "react-i18next";

import {
  LoadOrg,
  MemoryCountsByOwner,
  MemoryRecentChanges,
  ProjectListTree,
} from "../../../../wailsjs/go/app/App";
import type { memory_svc } from "../../../../wailsjs/go/models";

import {
  buildMemoryDirectory,
  ownerKey,
  type MemoryDirectory,
} from "./directory";

type State =
  | { status: "loading" }
  | { status: "error" }
  | {
      status: "ready";
      directory: MemoryDirectory;
      /** 各归属的正常条目数；没有条目的归属不在表里，读的时候当 0。 */
      counts: Map<string, number>;
      recent: memory_svc.MemoryItem[];
    };

/**
 * 左列要的一切：归属目录（组织 + 项目树）、各归属的计数、最近变更。
 *
 * `reload` 是后台重拉（写入成功之后、同步落地之后）：不回到加载态；已经加载出来过
 * 的页面，一次重拉失败不值得整页换成失败，留着上一份等下一次事件。
 */
export function useMemoryDirectory() {
  const { t } = useTranslation();
  const [state, setState] = React.useState<State>({ status: "loading" });
  const loaded = React.useRef(false);
  const globalLabel = t("memoryPage.sidebar.global");

  const reload = React.useCallback(async () => {
    try {
      const [org, projects, counts, recent] = await Promise.all([
        LoadOrg(),
        ProjectListTree(),
        MemoryCountsByOwner(),
        MemoryRecentChanges(),
      ]);
      loaded.current = true;
      setState({
        status: "ready",
        directory: buildMemoryDirectory({
          globalLabel,
          departments: org.departments ?? [],
          agents: org.agents ?? [],
          projects: projects ?? [],
        }),
        counts: new Map(
          (counts ?? []).map((c) => [ownerKey(c.owner), c.count]),
        ),
        recent: recent ?? [],
      });
    } catch {
      if (loaded.current) return;
      setState({ status: "error" });
    }
  }, [globalLabel]);

  const retry = React.useCallback(() => {
    setState({ status: "loading" });
    void reload();
  }, [reload]);

  React.useEffect(() => {
    void reload();
  }, [reload]);

  return { state, reload, retry };
}
