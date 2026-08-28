import { TranscriptPortsProvider } from "@agentre-hub/agentre-ui";
import type { TranscriptPorts } from "@agentre-hub/agentre-ui";
import {
  render as rtlRender,
  type RenderOptions,
} from "@testing-library/react";
import type * as React from "react";

/**
 * 转录里的卡片与富文本(RichLink / MarkdownImage / 各类审批卡)从
 * TranscriptPortsProvider 取宿主能力端口，而 Provider 由 App.tsx 在应用根挂载。
 * 任何直接渲染子树的测试都得自己补一个，否则 useTranscriptPorts 会如实抛错。
 *
 * 默认端口全是 no-op：只验渲染的用例不该被迫关心动作。要断言某个端口被调用，
 * 就把那一个换成 vi.fn() 传进 overrides，其余保持 no-op。
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
