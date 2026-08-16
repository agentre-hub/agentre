import {
  render as rtlRender,
  type RenderOptions,
} from "@testing-library/react";
import type * as React from "react";

import { TranscriptPortsProvider } from "../ports-context";
import type { TranscriptPorts } from "../ports";

/**
 * 转录里的卡片与富文本(RichLink / MarkdownImage / 各类审批卡)从
 * TranscriptPortsProvider 取宿主能力端口，而 Provider 由宿主在应用根挂载。
 * 任何直接渲染子树的测试都得自己补一个，否则 useTranscriptPorts 会如实抛错。
 *
 * 默认端口全是 no-op：只验渲染的用例不该被迫关心动作。要断言某个端口被调用，
 * 就把那一个换成 vi.fn() 传进 overrides，其余保持 no-op。
 *
 * 与宿主 `src/__tests__/helpers/transcript-ports.tsx` 是同形的两份：端口契约
 * (`ports.ts` / `ports-context.tsx`) 住在包里，包自己的用例不能跨边界去 import
 * 宿主的测试工具(boundary.test.ts 会如实拦下 `@/`)。宿主那份仍服务尚未搬迁的
 * 宿主侧转录测试，等转录组件搬完可以整体收敛掉。
 *
 * 放在 `__testing__/` 而不是 `.test.tsx`：它是被用例 import 的工具而非用例本身。
 * tsconfig.build.json 已把这个目录排除在 dist 之外——测试脚手架不该进产物。
 */
export function makeTestPorts(
  overrides: Partial<TranscriptPorts> = {},
): TranscriptPorts {
  return {
    answerToolPermission: async () => {},
    answerUserQuestion: async () => {},
    answerToolApproval: async () => {},
    resolveExecApproval: async () => ({ status: "resolved" }),
    resolvePlanAction: async () => ({}),
    openPath: async () => {},
    openExternalURL: () => {},
    readWorkspaceFile: async () => ({ content: "", contentType: "text/plain" }),
    openMention: () => {},
    ...overrides,
  };
}

export function renderWithPorts(
  ui: React.ReactElement,
  options?: Omit<RenderOptions, "wrapper"> & { ports?: TranscriptPorts },
) {
  const { ports = makeTestPorts(), ...rest } = options ?? {};

  return rtlRender(ui, {
    wrapper: ({ children }) => (
      <TranscriptPortsProvider ports={ports}>
        {children}
      </TranscriptPortsProvider>
    ),
    ...rest,
  });
}
