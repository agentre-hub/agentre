// 「汇报给」的单一事实源在共享包里：索引投影（buildOrgIndex 的「汇报给」筛选）
// 与详情的推导行读的必须是同一张表，而投影已经随组织面的呈现件一起进包了。
// 这里保留旧名字，是因为桌面端的调用点按 org/ 域内的命名读起来更顺。
export {
  buildOrgReportToMap as buildReportToMap,
  resolveOrgReportTo as resolveReportTo,
} from "@agentre-ai/agentre-ui";
