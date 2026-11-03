/**
 * 预设头像注册表：64 张贴纸风格 webp（职业 / 模型与工具 / 项目 / 部门各 16）。
 *
 * key 是持久化值（`avatar_icon` 里存 `preset:<group>-<name>`），发布后不能改名。
 * 显示名在包语言包的 `presets.<group>.<name>`，中英各一份（品牌组两边相同）。
 * 素材的生成与清理方法见 `scripts/preset-avatars/README.md`。
 *
 * 预设**不进** `icon-registry` 的 `searchIcons`：那张表是 lucide 线条图标词表，
 * 有自己的 30 条守卫；预设是整块位图，搜索走本文件的 `searchPresets`。
 */
import { AGENTRE_UI_NAMESPACE } from "../i18n";

import agent_assistant from "./assets/agent-assistant.webp";
import agent_architect from "./assets/agent-architect.webp";
import agent_reviewer from "./assets/agent-reviewer.webp";
import agent_tester from "./assets/agent-tester.webp";
import agent_debugger from "./assets/agent-debugger.webp";
import agent_frontend from "./assets/agent-frontend.webp";
import agent_backend from "./assets/agent-backend.webp";
import agent_ops from "./assets/agent-ops.webp";
import agent_security from "./assets/agent-security.webp";
import agent_writer from "./assets/agent-writer.webp";
import agent_analyst from "./assets/agent-analyst.webp";
import agent_refactor from "./assets/agent-refactor.webp";
import agent_performance from "./assets/agent-performance.webp";
import agent_planner from "./assets/agent-planner.webp";
import agent_translator from "./assets/agent-translator.webp";
import agent_shell from "./assets/agent-shell.webp";
import brand_claude from "./assets/brand-claude.webp";
import brand_anthropic from "./assets/brand-anthropic.webp";
import brand_openai from "./assets/brand-openai.webp";
import brand_codex from "./assets/brand-codex.webp";
import brand_pi from "./assets/brand-pi.webp";
import brand_openclaw from "./assets/brand-openclaw.webp";
import brand_hermes from "./assets/brand-hermes.webp";
import brand_deepseek from "./assets/brand-deepseek.webp";
import brand_glm from "./assets/brand-glm.webp";
import brand_gemini from "./assets/brand-gemini.webp";
import brand_qwen from "./assets/brand-qwen.webp";
import brand_kimi from "./assets/brand-kimi.webp";
import brand_minimax from "./assets/brand-minimax.webp";
import brand_mistral from "./assets/brand-mistral.webp";
import brand_xai from "./assets/brand-xai.webp";
import brand_meta from "./assets/brand-meta.webp";
import project_terminal from "./assets/project-terminal.webp";
import project_git from "./assets/project-git.webp";
import project_web from "./assets/project-web.webp";
import project_mobile from "./assets/project-mobile.webp";
import project_server from "./assets/project-server.webp";
import project_database from "./assets/project-database.webp";
import project_api from "./assets/project-api.webp";
import project_package from "./assets/project-package.webp";
import project_containers from "./assets/project-containers.webp";
import project_cloud from "./assets/project-cloud.webp";
import project_chip from "./assets/project-chip.webp";
import project_ai from "./assets/project-ai.webp";
import project_docs from "./assets/project-docs.webp";
import project_dashboard from "./assets/project-dashboard.webp";
import project_game from "./assets/project-game.webp";
import project_plugin from "./assets/project-plugin.webp";
import team_dev from "./assets/team-dev.webp";
import team_frontend from "./assets/team-frontend.webp";
import team_backend from "./assets/team-backend.webp";
import team_qa from "./assets/team-qa.webp";
import team_design from "./assets/team-design.webp";
import team_ops from "./assets/team-ops.webp";
import team_security from "./assets/team-security.webp";
import team_data from "./assets/team-data.webp";
import team_product from "./assets/team-product.webp";
import team_docs from "./assets/team-docs.webp";
import team_marketing from "./assets/team-marketing.webp";
import team_support from "./assets/team-support.webp";
import team_research from "./assets/team-research.webp";
import team_platform from "./assets/team-platform.webp";
import team_mobile from "./assets/team-mobile.webp";
import team_leadership from "./assets/team-leadership.webp";
export type PresetGroup = "agent" | "brand" | "project" | "team";

export interface PresetAvatar {
  /** 持久化值，形如 `preset:project-git`，≤32 字节。 */
  key: string;
  group: PresetGroup;
  /** 打包器解析后的 webp 地址。 */
  src: string;
  /** 包语言包里的显示名 key（不带 namespace）。 */
  labelKey: string;
}

const PRESET_PREFIX = "preset:";

// labelKey 写成字面量而不是拼出来：包的 i18n 守卫靠源码里的字面量判定 key 有读者。
const PRESETS: readonly PresetAvatar[] = [
  {
    key: "preset:agent-assistant",
    group: "agent",
    src: agent_assistant,
    labelKey: "presets.agent.assistant",
  },
  {
    key: "preset:agent-architect",
    group: "agent",
    src: agent_architect,
    labelKey: "presets.agent.architect",
  },
  {
    key: "preset:agent-reviewer",
    group: "agent",
    src: agent_reviewer,
    labelKey: "presets.agent.reviewer",
  },
  {
    key: "preset:agent-tester",
    group: "agent",
    src: agent_tester,
    labelKey: "presets.agent.tester",
  },
  {
    key: "preset:agent-debugger",
    group: "agent",
    src: agent_debugger,
    labelKey: "presets.agent.debugger",
  },
  {
    key: "preset:agent-frontend",
    group: "agent",
    src: agent_frontend,
    labelKey: "presets.agent.frontend",
  },
  {
    key: "preset:agent-backend",
    group: "agent",
    src: agent_backend,
    labelKey: "presets.agent.backend",
  },
  {
    key: "preset:agent-ops",
    group: "agent",
    src: agent_ops,
    labelKey: "presets.agent.ops",
  },
  {
    key: "preset:agent-security",
    group: "agent",
    src: agent_security,
    labelKey: "presets.agent.security",
  },
  {
    key: "preset:agent-writer",
    group: "agent",
    src: agent_writer,
    labelKey: "presets.agent.writer",
  },
  {
    key: "preset:agent-analyst",
    group: "agent",
    src: agent_analyst,
    labelKey: "presets.agent.analyst",
  },
  {
    key: "preset:agent-refactor",
    group: "agent",
    src: agent_refactor,
    labelKey: "presets.agent.refactor",
  },
  {
    key: "preset:agent-performance",
    group: "agent",
    src: agent_performance,
    labelKey: "presets.agent.performance",
  },
  {
    key: "preset:agent-planner",
    group: "agent",
    src: agent_planner,
    labelKey: "presets.agent.planner",
  },
  {
    key: "preset:agent-translator",
    group: "agent",
    src: agent_translator,
    labelKey: "presets.agent.translator",
  },
  {
    key: "preset:agent-shell",
    group: "agent",
    src: agent_shell,
    labelKey: "presets.agent.shell",
  },
  {
    key: "preset:brand-claude",
    group: "brand",
    src: brand_claude,
    labelKey: "presets.brand.claude",
  },
  {
    key: "preset:brand-anthropic",
    group: "brand",
    src: brand_anthropic,
    labelKey: "presets.brand.anthropic",
  },
  {
    key: "preset:brand-openai",
    group: "brand",
    src: brand_openai,
    labelKey: "presets.brand.openai",
  },
  {
    key: "preset:brand-codex",
    group: "brand",
    src: brand_codex,
    labelKey: "presets.brand.codex",
  },
  {
    key: "preset:brand-pi",
    group: "brand",
    src: brand_pi,
    labelKey: "presets.brand.pi",
  },
  {
    key: "preset:brand-openclaw",
    group: "brand",
    src: brand_openclaw,
    labelKey: "presets.brand.openclaw",
  },
  {
    key: "preset:brand-hermes",
    group: "brand",
    src: brand_hermes,
    labelKey: "presets.brand.hermes",
  },
  {
    key: "preset:brand-deepseek",
    group: "brand",
    src: brand_deepseek,
    labelKey: "presets.brand.deepseek",
  },
  {
    key: "preset:brand-glm",
    group: "brand",
    src: brand_glm,
    labelKey: "presets.brand.glm",
  },
  {
    key: "preset:brand-gemini",
    group: "brand",
    src: brand_gemini,
    labelKey: "presets.brand.gemini",
  },
  {
    key: "preset:brand-qwen",
    group: "brand",
    src: brand_qwen,
    labelKey: "presets.brand.qwen",
  },
  {
    key: "preset:brand-kimi",
    group: "brand",
    src: brand_kimi,
    labelKey: "presets.brand.kimi",
  },
  {
    key: "preset:brand-minimax",
    group: "brand",
    src: brand_minimax,
    labelKey: "presets.brand.minimax",
  },
  {
    key: "preset:brand-mistral",
    group: "brand",
    src: brand_mistral,
    labelKey: "presets.brand.mistral",
  },
  {
    key: "preset:brand-xai",
    group: "brand",
    src: brand_xai,
    labelKey: "presets.brand.xai",
  },
  {
    key: "preset:brand-meta",
    group: "brand",
    src: brand_meta,
    labelKey: "presets.brand.meta",
  },
  {
    key: "preset:project-terminal",
    group: "project",
    src: project_terminal,
    labelKey: "presets.project.terminal",
  },
  {
    key: "preset:project-git",
    group: "project",
    src: project_git,
    labelKey: "presets.project.git",
  },
  {
    key: "preset:project-web",
    group: "project",
    src: project_web,
    labelKey: "presets.project.web",
  },
  {
    key: "preset:project-mobile",
    group: "project",
    src: project_mobile,
    labelKey: "presets.project.mobile",
  },
  {
    key: "preset:project-server",
    group: "project",
    src: project_server,
    labelKey: "presets.project.server",
  },
  {
    key: "preset:project-database",
    group: "project",
    src: project_database,
    labelKey: "presets.project.database",
  },
  {
    key: "preset:project-api",
    group: "project",
    src: project_api,
    labelKey: "presets.project.api",
  },
  {
    key: "preset:project-package",
    group: "project",
    src: project_package,
    labelKey: "presets.project.package",
  },
  {
    key: "preset:project-containers",
    group: "project",
    src: project_containers,
    labelKey: "presets.project.containers",
  },
  {
    key: "preset:project-cloud",
    group: "project",
    src: project_cloud,
    labelKey: "presets.project.cloud",
  },
  {
    key: "preset:project-chip",
    group: "project",
    src: project_chip,
    labelKey: "presets.project.chip",
  },
  {
    key: "preset:project-ai",
    group: "project",
    src: project_ai,
    labelKey: "presets.project.ai",
  },
  {
    key: "preset:project-docs",
    group: "project",
    src: project_docs,
    labelKey: "presets.project.docs",
  },
  {
    key: "preset:project-dashboard",
    group: "project",
    src: project_dashboard,
    labelKey: "presets.project.dashboard",
  },
  {
    key: "preset:project-game",
    group: "project",
    src: project_game,
    labelKey: "presets.project.game",
  },
  {
    key: "preset:project-plugin",
    group: "project",
    src: project_plugin,
    labelKey: "presets.project.plugin",
  },
  {
    key: "preset:team-dev",
    group: "team",
    src: team_dev,
    labelKey: "presets.team.dev",
  },
  {
    key: "preset:team-frontend",
    group: "team",
    src: team_frontend,
    labelKey: "presets.team.frontend",
  },
  {
    key: "preset:team-backend",
    group: "team",
    src: team_backend,
    labelKey: "presets.team.backend",
  },
  {
    key: "preset:team-qa",
    group: "team",
    src: team_qa,
    labelKey: "presets.team.qa",
  },
  {
    key: "preset:team-design",
    group: "team",
    src: team_design,
    labelKey: "presets.team.design",
  },
  {
    key: "preset:team-ops",
    group: "team",
    src: team_ops,
    labelKey: "presets.team.ops",
  },
  {
    key: "preset:team-security",
    group: "team",
    src: team_security,
    labelKey: "presets.team.security",
  },
  {
    key: "preset:team-data",
    group: "team",
    src: team_data,
    labelKey: "presets.team.data",
  },
  {
    key: "preset:team-product",
    group: "team",
    src: team_product,
    labelKey: "presets.team.product",
  },
  {
    key: "preset:team-docs",
    group: "team",
    src: team_docs,
    labelKey: "presets.team.docs",
  },
  {
    key: "preset:team-marketing",
    group: "team",
    src: team_marketing,
    labelKey: "presets.team.marketing",
  },
  {
    key: "preset:team-support",
    group: "team",
    src: team_support,
    labelKey: "presets.team.support",
  },
  {
    key: "preset:team-research",
    group: "team",
    src: team_research,
    labelKey: "presets.team.research",
  },
  {
    key: "preset:team-platform",
    group: "team",
    src: team_platform,
    labelKey: "presets.team.platform",
  },
  {
    key: "preset:team-mobile",
    group: "team",
    src: team_mobile,
    labelKey: "presets.team.mobile",
  },
  {
    key: "preset:team-leadership",
    group: "team",
    src: team_leadership,
    labelKey: "presets.team.leadership",
  },
];

const PRESET_BY_KEY = new Map(PRESETS.map((preset) => [preset.key, preset]));

/** 认不出的 key（含未知的 `preset:x`、非预设 key）返回 undefined，由调用方退回首字母。 */
export function presetForKey(key: string): PresetAvatar | undefined {
  return PRESET_BY_KEY.get(key);
}

/** 是否是已登记的预设 key。 */
export function isPresetKey(key: string): boolean {
  return PRESET_BY_KEY.has(key);
}

/** 某一组的全部预设，按 spec 顺序。 */
export function presetsByGroup(group: PresetGroup): PresetAvatar[] {
  return PRESETS.filter((preset) => preset.group === group);
}

/**
 * 按当前语言的显示名（以及 key 后半段，如 `git`）模糊搜索，不区分大小写；
 * 空查询返回 `groups` 范围内的全部。
 */
export function searchPresets(
  query: string,
  t: (key: string) => string,
  groups?: readonly PresetGroup[],
): PresetAvatar[] {
  const needle = query.trim().toLowerCase();
  return PRESETS.filter((preset) => {
    if (groups && !groups.includes(preset.group)) return false;
    if (!needle) return true;
    if (preset.key.slice(PRESET_PREFIX.length).toLowerCase().includes(needle)) {
      return true;
    }
    return t(`${AGENTRE_UI_NAMESPACE}:${preset.labelKey}`)
      .toLowerCase()
      .includes(needle);
  });
}
