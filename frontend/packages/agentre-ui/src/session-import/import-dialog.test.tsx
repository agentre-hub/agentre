import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";

import { ImportSessionDialog } from "./import-dialog";
import type {
  ImportCandidateView,
  ImportCandidatesResult,
  ImportPreviewResult,
  ImportRunRequest,
  SessionImportPorts,
} from "./ports";
import type { TranscriptMessage } from "../transcript/dto";

/**
 * 对话框（规格「UI 与状态」）。这里钉的是**状态齐全**与**导入按钮的门控**：
 *
 * 扫描中 / 空 / 单后端失败 / 远端不支持 / 预览失败 / 导入中 / cwd 不存在，
 * 七种各有各的话；agent 与 cwd 都解析出来之后按钮才可用，而 cwd 没了就降级成
 * 「仅导入转录」而不是禁用（决策 16：转录照导，续跑关掉）。
 */
const NOW = Date.now();

function candidate(
  over: Partial<ImportCandidateView> = {},
): ImportCandidateView {
  return {
    backend: "claudecode",
    providerSessionId: "sess-1",
    title: "Refactor wire protocol",
    cwd: "/Code/agentre",
    startedAt: NOW - 3_600_000,
    endedAt: NOW - 3_600_000,
    turns: 42,
    origin: "terminal",
    locator: "loc-1",
    imported: false,
    importedSessionId: "",
    ...over,
  };
}

function message(over: Partial<TranscriptMessage>): TranscriptMessage {
  return {
    id: 1,
    sessionId: 0,
    role: "user",
    blocks: [{ type: "text", text: "hello" }],
    model: "",
    promptTokens: 0,
    completionTokens: 0,
    cachedTokens: 0,
    cacheCreationTokens: 0,
    reasoningTokens: 0,
    totalInputTokens: 0,
    durationMs: 0,
    errorText: "",
    seq: 1,
    createtime: NOW,
    ...over,
  };
}

function previewResult(cwdExists = true): ImportPreviewResult {
  return {
    meta: {
      backend: "claudecode",
      providerSessionId: "sess-1",
      title: "Refactor wire protocol",
      cwd: "/Code/agentre",
      model: "claude-opus-5",
      turns: 42,
      toolCalls: 402,
      compactions: 1,
      startedAt: NOW - 7_200_000,
      endedAt: NOW - 3_600_000,
      origin: "terminal",
      gaps: [],
      cwdExists,
      imported: false,
      importedSessionId: "",
    },
    messages: [message({})],
    previewedTurns: 1,
    remainingTurns: 41,
  };
}

function ports(over: Partial<SessionImportPorts> = {}): SessionImportPorts {
  return {
    devices: [{ id: "0", name: "This machine", online: true, local: true }],
    agents: [
      {
        id: "7",
        name: "Backend dev",
        backend: "claudecode",
        model: "claude-opus-5",
      },
    ],
    listCandidates: vi.fn(
      async (): Promise<ImportCandidatesResult> => ({
        candidates: [candidate()],
        issues: [],
      }),
    ),
    preview: vi.fn(async () => previewResult()),
    runImport: vi.fn(async () => ({
      sessionId: "99",
      alreadyImported: false,
      readOnly: false,
      cwd: "/Code/agentre",
      importedTurns: 42,
    })),
    openSession: vi.fn(),
    ...over,
  };
}

function renderDialog(
  p: SessionImportPorts,
  prefill: Record<string, string> = {},
) {
  const onImported = vi.fn();
  const onOpenChange = vi.fn();
  render(
    <ImportSessionDialog
      open
      onOpenChange={onOpenChange}
      ports={p}
      prefill={prefill}
      onImported={onImported}
    />,
  );
  return { onImported, onOpenChange };
}

describe("扫描中 / 空 / 单后端失败 / 远端不支持 五个非主态", () => {
  it("Given 扫描还没回来, When 对话框打开, Then 左栏是「正在扫描」而不是空态（说成「没有会话」是在撒谎）", async () => {
    let release: (r: ImportCandidatesResult) => void = () => {};
    const p = ports({
      listCandidates: vi.fn(
        () =>
          new Promise<ImportCandidatesResult>((resolve) => {
            release = resolve;
          }),
      ),
    });
    renderDialog(p);

    expect(await screen.findByTestId("import-scanning")).toBeTruthy();
    expect(screen.queryByTestId("import-empty")).toBeNull();

    release({ candidates: [], issues: [] });
    await screen.findByTestId("import-empty");
  });

  it("Given 这个目录下三个后端都没跑过, When 扫描完成, Then 空态给出「改为搜索全部目录」的出口", async () => {
    const p = ports({
      listCandidates: vi.fn(async () => ({ candidates: [], issues: [] })),
    });
    renderDialog(p, { cwdPrefix: "/Code/agentre" });

    const empty = await screen.findByTestId("import-empty");
    expect(empty.textContent).toContain("/Code/agentre");
    fireEvent.click(screen.getByTestId("import-relax-filters"));

    await waitFor(() =>
      expect(p.listCandidates).toHaveBeenLastCalledWith(
        expect.objectContaining({ cwdPrefix: "" }),
      ),
    );
  });

  it("Given codex 的目录读不动, When 扫描完成, Then 它自己那一档报出来，claude 的结果照常在列", async () => {
    const p = ports({
      listCandidates: vi.fn(async () => ({
        candidates: [candidate()],
        issues: [
          {
            backend: "codex",
            status: "unavailable" as const,
            reason: "~/.codex/sessions: permission denied",
          },
        ],
      })),
    });
    renderDialog(p);

    const issue = await screen.findByTestId("import-backend-issue-codex");
    expect(issue.textContent).toContain("permission denied");
    expect(screen.getByTestId("import-candidate-sess-1")).toBeTruthy();
  });

  it("Given 那台机器此刻拨不通, When 扫描完成, Then 给出拨不通的原因并给「切换到本机」，绝不显示成「这台机器没有会话」", async () => {
    const p = ports({
      devices: [
        { id: "0", name: "This machine", online: true, local: true },
        { id: "3", name: "Build box", online: true, local: false },
      ],
      listCandidates: vi.fn(async () => ({
        candidates: [],
        issues: [
          {
            backend: "",
            status: "unavailable" as const,
            reason: "dial tcp: connection refused",
          },
        ],
      })),
    });
    renderDialog(p, { deviceId: "3" });

    const issue = await screen.findByTestId("import-device-issue-unavailable");
    expect(issue.textContent).toContain("connection refused");

    fireEvent.click(screen.getByTestId("import-switch-local"));
    await waitFor(() =>
      expect(p.listCandidates).toHaveBeenLastCalledWith(
        expect.objectContaining({ deviceId: "0" }),
      ),
    );
  });
});

describe("预览失败 / 导入中 两个非主态", () => {
  it("Given 选中的那条转录打不开, When 预览返回失败, Then 右栏给出原因且导入按钮不可用", async () => {
    const p = ports({
      preview: vi.fn(async () => {
        throw new Error("transcript is corrupt");
      }),
    });
    renderDialog(p, { agentId: "7" });

    fireEvent.click(await screen.findByTestId("import-candidate-sess-1"));
    const err = await screen.findByTestId("import-preview-error");
    expect(err.textContent).toContain("transcript is corrupt");
    expect(screen.getByTestId("import-submit").hasAttribute("disabled")).toBe(
      true,
    );
  });

  it("Given 导入正在按轮写库, When 进度事件到达, Then 按轮报进度且是个 aria-live 区域（不打断朗读）", async () => {
    let emit: (done: number, total: number) => void = () => {};
    const p = ports({
      onImportProgress: (listener) => {
        emit = listener;
        return () => {};
      },
      runImport: vi.fn(() => new Promise<never>(() => {})),
    });
    renderDialog(p, { agentId: "7" });

    fireEvent.click(await screen.findByTestId("import-candidate-sess-1"));
    await waitFor(() =>
      expect(screen.getByTestId("import-submit").hasAttribute("disabled")).toBe(
        false,
      ),
    );
    fireEvent.click(screen.getByTestId("import-submit"));

    const progress = await screen.findByTestId("import-progress");
    expect(progress.getAttribute("aria-live")).toBe("polite");
    emit(26, 42);
    await waitFor(() =>
      expect(screen.getByTestId("import-progress").textContent).toContain(
        "26 / 42",
      ),
    );
  });

  it("Given 导入正在按轮写库, When 用户按取消, Then 宿主拿着发起时那个 requestId 停掉这一笔，并且不刷一条红字（规格：导入过程按轮计进度、可取消）", async () => {
    let reject: (err: unknown) => void = () => {};
    let requestId = "";
    const cancelImport = vi.fn();
    const runImport = vi.fn(
      (req: ImportRunRequest) =>
        new Promise<never>((_, r) => {
          requestId = req.requestId;
          reject = r;
        }),
    );
    const p = ports({ cancelImport, runImport });
    renderDialog(p, { agentId: "7" });

    fireEvent.click(await screen.findByTestId("import-candidate-sess-1"));
    await waitFor(() =>
      expect(screen.getByTestId("import-submit").hasAttribute("disabled")).toBe(
        false,
      ),
    );
    fireEvent.click(screen.getByTestId("import-submit"));
    await screen.findByTestId("import-progress");

    fireEvent.click(screen.getByTestId("import-cancel"));
    expect(requestId).toBeTruthy();
    expect(cancelImport).toHaveBeenCalledWith(requestId);

    // 后端把这一笔整个回滚之后调用才失败：那是用户自己按的，不该再刷一条报错。
    reject(new Error("context canceled"));
    await waitFor(() =>
      expect(screen.queryByTestId("import-progress")).toBeNull(),
    );
    expect(screen.queryByTestId("import-error")).toBeNull();
  });

  it("Given 宿主没有声明取消这件能力, When 导入正在写库, Then 不摆那颗取消键（不承诺宿主做不到的事）", async () => {
    const p = ports({ runImport: vi.fn(() => new Promise<never>(() => {})) });
    renderDialog(p, { agentId: "7" });

    fireEvent.click(await screen.findByTestId("import-candidate-sess-1"));
    await waitFor(() =>
      expect(screen.getByTestId("import-submit").hasAttribute("disabled")).toBe(
        false,
      ),
    );
    fireEvent.click(screen.getByTestId("import-submit"));
    await screen.findByTestId("import-progress");

    expect(screen.queryByTestId("import-cancel")).toBeNull();
  });
});

describe("导入按钮的门控：agent 与 cwd 都解析出来才可用", () => {
  it("Given 还没选中任何候选, When 对话框刚打开, Then 按钮不可用", async () => {
    renderDialog(ports(), { agentId: "7" });
    await screen.findByTestId("import-candidate-sess-1");
    expect(screen.getByTestId("import-submit").hasAttribute("disabled")).toBe(
      true,
    );
  });

  it("Given 选中了候选但没有可用的 agent, When 预览就绪, Then 按钮仍不可用（磁盘上没有 agent 绑定，那是 agentre 自己的概念）", async () => {
    const p = ports({ agents: [] });
    renderDialog(p);

    fireEvent.click(await screen.findByTestId("import-candidate-sess-1"));
    await screen.findByTestId("import-preview-transcript");
    expect(screen.getByTestId("import-submit").hasAttribute("disabled")).toBe(
      true,
    );
  });

  it("Given 预填的 agent 是 codex 的、候选却是 claude 的, When 预览就绪, Then 按钮不可用（CLI 那边不认识这个 id）", async () => {
    const p = ports({
      agents: [{ id: "7", name: "Codex dev", backend: "codex" }],
    });
    renderDialog(p, { agentId: "7" });

    fireEvent.click(await screen.findByTestId("import-candidate-sess-1"));
    await screen.findByTestId("import-preview-transcript");
    expect(screen.getByTestId("import-submit").hasAttribute("disabled")).toBe(
      true,
    );
  });

  it("Given agent 与 cwd 都齐了, When 按下按钮, Then 把这条候选连同 agent / 项目一起交回宿主，按钮说的是「导入并可继续对话」", async () => {
    const p = ports();
    const { onImported } = renderDialog(p, {
      agentId: "7",
      projectId: "12",
    });

    fireEvent.click(await screen.findByTestId("import-candidate-sess-1"));
    await waitFor(() =>
      expect(screen.getByTestId("import-submit").hasAttribute("disabled")).toBe(
        false,
      ),
    );
    expect(screen.getByTestId("import-submit").textContent).toContain(
      "keep chatting",
    );

    fireEvent.click(screen.getByTestId("import-submit"));
    await waitFor(() => expect(onImported).toHaveBeenCalled());
    expect(p.runImport).toHaveBeenCalledWith({
      deviceId: "0",
      backend: "claudecode",
      locator: "loc-1",
      agentId: "7",
      projectId: "12",
      // 没另选目录 → 交回空串，写入侧用转录里记的那个 cwd。
      cwd: "",
      // 取消要拿得住这一笔，所以发起时就带上标识（值本身是随机的）。
      requestId: expect.stringMatching(/^imp-/) as unknown as string,
    });
  });

  it("Given cwd 已不存在, When 预览就绪, Then 按钮照样可用但改说「仅导入转录」，且底部一行明说续跑关掉（决策 16）", async () => {
    const p = ports({ preview: vi.fn(async () => previewResult(false)) });
    renderDialog(p, { agentId: "7" });

    fireEvent.click(await screen.findByTestId("import-candidate-sess-1"));
    await waitFor(() =>
      expect(screen.getByTestId("import-submit").hasAttribute("disabled")).toBe(
        false,
      ),
    );
    expect(screen.getByTestId("import-submit").textContent).toContain(
      "transcript only",
    );
    expect(screen.getByTestId("import-cwd-line").textContent).toContain(
      "read-only",
    );
  });

  it("Given cwd 已不存在且宿主接得上目录选择器, When 用户另选一个目录, Then 那个目录成为这条会话的工作目录交回宿主（规格「续跑」的「选择新目录」出口）", async () => {
    const p = ports({
      preview: vi.fn(async () => previewResult(false)),
      pickDirectory: vi.fn(async () => "/Code/agentre-2"),
    });
    const { onImported } = renderDialog(p, { agentId: "7", projectId: "12" });

    fireEvent.click(await screen.findByTestId("import-candidate-sess-1"));
    await screen.findByTestId("import-cwd-line");
    fireEvent.click(screen.getByTestId("import-pick-directory"));

    // 底部那一行改口说「工作目录改成了这个」——它是这条会话去哪跑，不是筛选条件，
    // 所以候选列表不该因此重扫。
    await waitFor(() =>
      expect(screen.getByTestId("import-cwd-line").textContent).toContain(
        "/Code/agentre-2",
      ),
    );
    const scans = (p.listCandidates as ReturnType<typeof vi.fn>).mock.calls
      .length;

    fireEvent.click(screen.getByTestId("import-submit"));
    await waitFor(() => expect(onImported).toHaveBeenCalled());
    expect(p.runImport).toHaveBeenCalledWith(
      expect.objectContaining({ cwd: "/Code/agentre-2" }),
    );
    expect(
      (p.listCandidates as ReturnType<typeof vi.fn>).mock.calls.length,
    ).toBe(scans);
  });

  it("Given 用户选目录时按了取消, When 目录选择器返回空, Then 什么都不变（仍是转录里那个已不存在的目录）", async () => {
    const p = ports({
      preview: vi.fn(async () => previewResult(false)),
      pickDirectory: vi.fn(async () => null),
    });
    renderDialog(p, { agentId: "7" });

    fireEvent.click(await screen.findByTestId("import-candidate-sess-1"));
    await screen.findByTestId("import-cwd-line");
    fireEvent.click(screen.getByTestId("import-pick-directory"));

    await waitFor(() => expect(p.pickDirectory).toHaveBeenCalled());
    expect(screen.getByTestId("import-cwd-line").textContent).toContain(
      "read-only",
    );
  });

  it("Given 宿主声明不了「另选目录」这件事, When cwd 不存在, Then 那个按钮整条不出现（不承诺后端不会做的事）", async () => {
    const p = ports({ preview: vi.fn(async () => previewResult(false)) });
    renderDialog(p, { agentId: "7" });

    fireEvent.click(await screen.findByTestId("import-candidate-sess-1"));
    await screen.findByTestId("import-cwd-line");
    expect(screen.queryByTestId("import-pick-directory")).toBeNull();
  });

  it("Given 选中的是一条已导入过的候选, When 右栏渲染, Then 不预览、直接给「打开」，且导入按钮不可用", async () => {
    const p = ports({
      listCandidates: vi.fn(async () => ({
        candidates: [candidate({ imported: true, importedSessionId: "55" })],
        issues: [],
      })),
    });
    const { onOpenChange } = renderDialog(p, { agentId: "7" });

    fireEvent.click(await screen.findByTestId("import-candidate-sess-1"));
    await screen.findByTestId("import-preview-imported");
    expect(p.preview).not.toHaveBeenCalled();
    expect(screen.getByTestId("import-submit").hasAttribute("disabled")).toBe(
      true,
    );

    fireEvent.click(screen.getByTestId("import-open-sess-1"));
    expect(p.openSession).toHaveBeenCalledWith("55");
    expect(onOpenChange).toHaveBeenCalledWith(false);
  });
});

/**
 * 往上找第一个自己滚的祖先。happy-dom 不跑布局，滚动容器只能从类名上认 —— 这几条
 * 用例钉的正是「谁负责滚」这件结构上的事，而不是某个具体像素值。
 */
function scrollParent(el: Element | null): Element | null {
  for (let n = el?.parentElement ?? null; n; n = n.parentElement) {
    if (n.className.includes("overflow-y-auto")) return n;
  }
  return null;
}

function longPreview(): ImportPreviewResult {
  return {
    ...previewResult(),
    messages: Array.from({ length: 40 }, (_, i) =>
      message({ id: i + 1, seq: i + 1 }),
    ),
  };
}

describe("对话框的高度：撑不破屏幕，两栏各滚各的", () => {
  it("Given 选中一条几十轮的会话, When 转录渲染出来, Then 对话框自己带高度上限、转录在它自己的滚动容器里滚（不是把对话框顶出屏幕）", async () => {
    const p = ports({ preview: vi.fn(async () => longPreview()) });
    renderDialog(p, { agentId: "7" });

    fireEvent.click(await screen.findByTestId("import-candidate-sess-1"));
    const transcript = await screen.findByTestId("import-preview-transcript");

    expect(screen.getByTestId("import-session-dialog").className).toMatch(
      /max-h-/,
    );
    const scroller = scrollParent(transcript);
    expect(scroller).not.toBeNull();
    expect(screen.getByTestId("import-preview").contains(scroller)).toBe(true);
  });

  it("Given 候选多到一屏放不下, When 看左栏, Then 滚的是候选列表自己，筛选栏留在原地", async () => {
    const many = Array.from({ length: 30 }, (_, i) =>
      candidate({ providerSessionId: `sess-${i}`, locator: `loc-${i}` }),
    );
    const p = ports({
      listCandidates: vi.fn(async () => ({ candidates: many, issues: [] })),
    });
    renderDialog(p);

    const row = await screen.findByTestId("import-candidate-sess-1");
    const scroller = scrollParent(row);
    expect(scroller).not.toBeNull();
    expect(
      scroller!.contains(screen.getByTestId("import-backend-filter")),
    ).toBe(false);
  });

  it("Given 转录长到要滚, When 往下翻, Then 「这是哪条会话」的元信息不跟着滚走（它就是做决定的那点依据）", async () => {
    const p = ports({ preview: vi.fn(async () => longPreview()) });
    renderDialog(p, { agentId: "7" });

    fireEvent.click(await screen.findByTestId("import-candidate-sess-1"));
    const transcript = await screen.findByTestId("import-preview-transcript");
    const meta = screen.getByTestId("import-preview-meta");

    expect(scrollParent(transcript)!.contains(meta)).toBe(false);
  });
});

describe("两颗「停」各自待在它该待的地方", () => {
  it("Given 扫描还在跑, When 看左栏, Then 「停止」就在「正在扫描」那一行上（不吊在骨架屏底下等人往下找）", async () => {
    const p = ports({
      listCandidates: vi.fn(
        () => new Promise<ImportCandidatesResult>(() => {}),
      ),
    });
    renderDialog(p);

    const stop = await screen.findByTestId("import-scan-stop");
    expect(screen.getByTestId("import-scan-status").contains(stop)).toBe(true);
  });

  it("Given 导入正在写库且宿主接得住取消, When 看底栏, Then 主操作位上就是那颗「停止导入」，不再同时摆一颗按不动的「导入」", async () => {
    const p = ports({
      cancelImport: vi.fn(),
      runImport: vi.fn(() => new Promise<never>(() => {})),
    });
    renderDialog(p, { agentId: "7" });

    fireEvent.click(await screen.findByTestId("import-candidate-sess-1"));
    await waitFor(() =>
      expect(screen.getByTestId("import-submit").hasAttribute("disabled")).toBe(
        false,
      ),
    );
    fireEvent.click(screen.getByTestId("import-submit"));
    await screen.findByTestId("import-progress");

    expect(screen.queryByTestId("import-submit")).toBeNull();
    expect(
      screen
        .getByTestId("import-actions")
        .contains(screen.getByTestId("import-cancel")),
    ).toBe(true);
  });
});
