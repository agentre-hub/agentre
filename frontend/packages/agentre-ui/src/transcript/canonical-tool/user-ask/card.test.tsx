import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import type * as React from "react";
import { beforeEach, describe, it, expect, vi } from "vitest";

import type { TranscriptBlock } from "../../dto";
import type { TranscriptPorts } from "../../ports";
import { TranscriptPortsProvider } from "../../ports-context";
import { __resetChatPanelScrollStateForTesting } from "../../chat-panel-scroll-state";

import { UserAskCard } from "./card";

// 卡片不再直接调 Wails,而是从 TranscriptPortsProvider 取动作端口;这里注入
// 一份 mock 端口,断言打在端口上。
const answerUserQuestion = vi.fn().mockResolvedValue(undefined);
const ports: TranscriptPorts = {
  answerToolPermission: vi.fn().mockResolvedValue(undefined),
  answerUserQuestion,
  answerToolApproval: vi.fn().mockResolvedValue(undefined),
  resolveExecApproval: vi.fn().mockResolvedValue({ status: "resolved" }),
  resolvePlanAction: vi.fn().mockResolvedValue(undefined),
};

function renderCard(ui: React.ReactElement) {
  return render(
    <TranscriptPortsProvider ports={ports}>{ui}</TranscriptPortsProvider>,
  );
}

describe("UserAskCard", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    __resetChatPanelScrollStateForTesting();
  });

  function draftBlock(): TranscriptBlock {
    return {
      type: "tool_use",
      toolName: "AskUserQuestion",
      canonical: {
        kind: "user.ask",
        userAsk: {
          requestId: "req-draft",
          questions: [
            {
              question: "First question?",
              header: "First",
              options: [{ label: "A", description: "" }],
            },
            {
              question: "Second question?",
              header: "Second",
              options: [{ label: "B", description: "" }],
            },
          ],
        },
      },
    } as unknown as TranscriptBlock;
  }

  it("renders nothing without canonical", () => {
    const block = {
      type: "tool_use",
      toolName: "AskUserQuestion",
    } as unknown as TranscriptBlock;
    const { container } = renderCard(
      <UserAskCard toolBlock={block} sessionId={1} />,
    );
    expect(container.firstChild).toBeNull();
  });

  it("renders question + options + WAITING pill", () => {
    const block = {
      type: "tool_use",
      toolName: "AskUserQuestion",
      canonical: {
        kind: "user.ask",
        userAsk: {
          requestId: "req-1",
          questions: [
            {
              question: "想用哪种方式?",
              header: "选项",
              options: [
                { label: "A", description: "" },
                { label: "B", description: "" },
              ],
            },
          ],
        },
      },
    } as unknown as TranscriptBlock;
    renderCard(<UserAskCard toolBlock={block} sessionId={1} />);
    expect(screen.getByText("想用哪种方式?")).toBeDefined();
    expect(screen.getByText("A")).toBeDefined();
    expect(screen.getByText(/WAITING/)).toBeDefined();
  });

  it("renders ANSWERED state when answered", () => {
    const block = {
      type: "tool_use",
      toolName: "AskUserQuestion",
      canonical: {
        kind: "user.ask",
        userAsk: {
          requestId: "req-1",
          questions: [
            {
              question: "?",
              header: "h",
              options: [{ label: "A", description: "" }],
            },
          ],
          answers: [{ questionIndex: 0, labels: ["A"] }],
          answered: true,
        },
      },
    } as unknown as TranscriptBlock;
    renderCard(<UserAskCard toolBlock={block} sessionId={1} />);
    expect(screen.getByText("ANSWERED")).toBeDefined();
  });

  it("Given an expanded card, When collapsed, Then the body stays mounted through the collapse transition and unmounts after it ends", () => {
    const block = {
      type: "tool_use",
      toolName: "AskUserQuestion",
      canonical: {
        kind: "user.ask",
        userAsk: {
          requestId: "req-1",
          questions: [
            {
              question: "想用哪种方式?",
              header: "选项",
              options: [{ label: "A", description: "" }],
            },
          ],
        },
      },
    } as unknown as TranscriptBlock;
    renderCard(<UserAskCard toolBlock={block} sessionId={1} />);
    const header = screen.getByRole("button", { expanded: true });
    expect(screen.getByText("想用哪种方式?")).toBeDefined();

    fireEvent.click(header);
    expect(header).toHaveAttribute("aria-expanded", "false");
    // 收缩动画期间内容仍挂载,过渡结束才卸载。
    expect(screen.getByText("想用哪种方式?")).toBeDefined();
    fireEvent.transitionEnd(header.nextElementSibling as HTMLElement);
    expect(screen.queryByText("想用哪种方式?")).toBeNull();
  });

  it("Given answered multiple questions, When switching question tabs, Then answers remain reviewable but locked", async () => {
    const user = userEvent.setup();
    const block = {
      type: "tool_use",
      toolName: "AskUserQuestion",
      canonical: {
        kind: "user.ask",
        userAsk: {
          requestId: "req-1",
          questions: [
            {
              question: "First question?",
              header: "First",
              options: [{ label: "A", description: "" }],
            },
            {
              question: "Second question?",
              header: "Second",
              options: [{ label: "B", description: "" }],
            },
          ],
          answers: [
            { questionIndex: 0, labels: ["A"] },
            { questionIndex: 1, labels: ["B"] },
          ],
          answered: true,
        },
      },
    } as unknown as TranscriptBlock;

    renderCard(<UserAskCard toolBlock={block} sessionId={1} />);

    await user.click(screen.getByRole("tab", { name: /Q2 · Second/ }));

    expect(screen.getByText("Second question?")).toBeDefined();
    expect(screen.getByRole("button", { name: /^B$/ })).toBeDisabled();
    expect(screen.getByRole("textbox")).toBeDisabled();
  });

  it("Given skipped multiple questions, When switching question tabs, Then questions remain reviewable without answer actions", async () => {
    const user = userEvent.setup();
    const block = {
      type: "tool_use",
      toolName: "AskUserQuestion",
      canonical: {
        kind: "user.ask",
        userAsk: {
          requestId: "req-1",
          questions: [
            {
              question: "First question?",
              header: "First",
              options: [{ label: "A", description: "" }],
            },
            {
              question: "Second question?",
              header: "Second",
              options: [{ label: "B", description: "" }],
            },
          ],
          skipped: true,
        },
      },
    } as unknown as TranscriptBlock;

    renderCard(<UserAskCard toolBlock={block} sessionId={1} />);

    await user.click(screen.getByRole("tab", { name: /Q2 · Second/ }));

    expect(screen.getByText("Second question?")).toBeDefined();
    expect(screen.getByRole("button", { name: /^B$/ })).toBeDisabled();
    expect(screen.queryByRole("button", { name: "Submit" })).toBeNull();
    expect(screen.queryByRole("button", { name: "Skip" })).toBeNull();
  });

  it("Given a draft answer, When the Ask User card remounts in the same tab, Then it restores selections, Other text, and active question", async () => {
    const user = userEvent.setup();
    const block = draftBlock();
    const view = renderCard(
      <UserAskCard
        toolBlock={block}
        sessionId={1}
        tabStateKey="tab-a"
        uiStateKey="ask:req-draft"
      />,
    );

    await user.click(screen.getByRole("button", { name: /^A$/ }));
    await user.click(screen.getByRole("tab", { name: /Q2 · Second/ }));
    await user.type(screen.getByRole("textbox"), "custom answer");

    view.unmount();
    renderCard(
      <UserAskCard
        toolBlock={block}
        sessionId={1}
        tabStateKey="tab-a"
        uiStateKey="ask:req-draft"
      />,
    );

    expect(screen.getByText("Second question?")).toBeDefined();
    expect(screen.getByRole("textbox")).toHaveValue("custom answer");
    await user.click(screen.getByRole("tab", { name: /Q1 · First/ }));
    expect(screen.getByRole("button", { name: /^A$/ })).toHaveClass(
      "bg-primary-soft",
    );
  });

  it("Given a draft answer in one tab, When the same request remounts in another tab, Then it does not restore the old draft", async () => {
    const user = userEvent.setup();
    const block = draftBlock();
    const view = renderCard(
      <UserAskCard
        toolBlock={block}
        sessionId={1}
        tabStateKey="tab-a"
        uiStateKey="ask:req-draft"
      />,
    );
    await user.type(screen.getByRole("textbox"), "tab A draft");

    view.unmount();
    renderCard(
      <UserAskCard
        toolBlock={block}
        sessionId={1}
        tabStateKey="tab-b"
        uiStateKey="ask:req-draft"
      />,
    );

    expect(screen.getByText("First question?")).toBeDefined();
    expect(screen.getByRole("textbox")).toHaveValue("");
  });

  it("renders EXPIRED state locked with no submit button", () => {
    const block = {
      type: "tool_use",
      toolName: "AskUserQuestion",
      canonical: {
        kind: "user.ask",
        userAsk: {
          requestId: "req-exp",
          questions: [
            {
              question: "?",
              header: "h",
              options: [{ label: "A", description: "" }],
            },
          ],
          expired: true,
        },
      },
    } as unknown as TranscriptBlock;
    renderCard(<UserAskCard toolBlock={block} sessionId={1} />);
    expect(screen.getByText(/已失效|EXPIRED/i)).toBeDefined();
    expect(screen.queryByText("提交回复")).toBeNull();
    expect(screen.queryByText("Submit reply")).toBeNull();
  });

  function singleBlock(): TranscriptBlock {
    return {
      type: "tool_use",
      toolName: "AskUserQuestion",
      canonical: {
        kind: "user.ask",
        userAsk: {
          requestId: "req-1",
          questions: [
            {
              question: "?",
              header: "h",
              options: [{ label: "A", description: "" }],
            },
          ],
        },
      },
    } as unknown as TranscriptBlock;
  }

  it("Given the question no longer exists, When submit fails with the runtime's no-waiter signal, Then the card shows expired and locks", async () => {
    const user = userEvent.setup();
    answerUserQuestion.mockRejectedValueOnce(
      "agentruntime/runtimes/claudecode: no waiting AskUserQuestion for requestID req-1: agentruntime: no waiting request for requestID",
    );
    renderCard(<UserAskCard toolBlock={singleBlock()} sessionId={1} />);
    await user.click(screen.getByText("A"));
    await user.click(screen.getByText("Submit Reply"));
    await waitFor(() => {
      expect(screen.getByText(/superseded/i)).toBeDefined();
    });
    expect(screen.getByText("EXPIRED")).toBeDefined();
    // 锁卡:提交按钮消失
    expect(screen.queryByText("Submit Reply")).toBeNull();
    // 已失效不是「没提交成功」，也不贴原文。
    expect(screen.queryByText("Couldn't submit")).toBeNull();
    expect(screen.queryByText(/no waiting/)).toBeNull();
  });

  it("Given any other submit failure, When it fails, Then the card stays unlocked with the selection kept, says it was not submitted with the raw text folded, and can be resubmitted", async () => {
    const user = userEvent.setup();
    answerUserQuestion.mockRejectedValueOnce(
      new Error("websocket: close 1006 (abnormal closure)"),
    );
    renderCard(<UserAskCard toolBlock={singleBlock()} sessionId={1} />);
    await user.type(screen.getByRole("textbox"), "my own answer");
    await user.click(screen.getByText("Submit Reply"));

    expect(await screen.findByText("Couldn't submit")).toBeDefined();
    // 原文只在默认折叠的详情里。
    const raw = screen.getByText("websocket: close 1006 (abnormal closure)");
    const details = raw.closest("details");
    expect(details).not.toBeNull();
    expect(details!.open).toBe(false);
    // 不锁卡、不判失效。
    expect(screen.queryByText("EXPIRED")).toBeNull();
    expect(screen.queryByText(/superseded/i)).toBeNull();
    expect(screen.getByText(/WAITING/)).toBeDefined();
    // 选择保留。
    expect(screen.getByRole("textbox")).toHaveValue("my own answer");
    expect(screen.getByRole("textbox")).not.toBeDisabled();

    // 可再次提交：带着同一份答案，成功后失败提示消失。
    await user.click(screen.getByText("Submit Reply"));
    await waitFor(() => expect(answerUserQuestion).toHaveBeenCalledTimes(2));
    expect(answerUserQuestion.mock.calls[1][0]).toEqual(
      answerUserQuestion.mock.calls[0][0],
    );
    expect(answerUserQuestion.mock.calls[1][0].answers).toEqual([
      {
        questionIndex: 0,
        labels: ["__other__"],
        otherText: "my own answer",
      },
    ]);
    await waitFor(() =>
      expect(screen.queryByText("Couldn't submit")).toBeNull(),
    );
  });

  it("Given several questions with a later one unanswered, When submitting, Then the card switches to the first unanswered question and shows the error there", async () => {
    const user = userEvent.setup();
    renderCard(<UserAskCard toolBlock={draftBlock()} sessionId={1} />);
    // 只答第一题，停在第一题上提交。
    await user.click(screen.getByText("A"));
    await user.click(screen.getByText("Submit Reply"));

    expect(await screen.findByText("Second question?")).toBeDefined();
    expect(screen.queryByText("First question?")).toBeNull();
    expect(
      screen.getByText("Select an option before submitting"),
    ).toBeDefined();
    expect(answerUserQuestion).not.toHaveBeenCalled();

    // 错误属于缺答的那一题：切回已答的题不跟着显示。
    await user.click(screen.getByRole("tab", { name: /Q1/ }));
    expect(screen.getByText("First question?")).toBeDefined();
    expect(screen.queryByText("Select an option before submitting")).toBeNull();
  });

  it("Given several questions with an earlier one unanswered, When submitting from the last one, Then the card switches back to the first unanswered question", async () => {
    const user = userEvent.setup();
    renderCard(<UserAskCard toolBlock={draftBlock()} sessionId={1} />);
    await user.click(screen.getByRole("tab", { name: /Q2/ }));
    await user.click(screen.getByText("B"));
    await user.click(screen.getByText("Submit Reply"));

    expect(await screen.findByText("First question?")).toBeDefined();
    expect(
      screen.getByText("Select an option before submitting"),
    ).toBeDefined();
    expect(answerUserQuestion).not.toHaveBeenCalled();
  });

  it("Given a saved draft, When the answer is submitted, Then remounting does not restore it", async () => {
    const user = userEvent.setup();
    const block = {
      type: "tool_use",
      toolName: "AskUserQuestion",
      canonical: {
        kind: "user.ask",
        userAsk: {
          requestId: "req-submit",
          questions: [
            {
              question: "Submit question?",
              header: "Submit",
              options: [{ label: "A", description: "" }],
            },
          ],
        },
      },
    } as unknown as TranscriptBlock;
    const view = renderCard(
      <UserAskCard
        toolBlock={block}
        sessionId={1}
        tabStateKey="tab-a"
        uiStateKey="ask:req-submit"
      />,
    );

    await user.type(screen.getByRole("textbox"), "draft before submit");
    await user.click(screen.getByRole("button", { name: /Submit Reply/ }));
    await waitFor(() => expect(answerUserQuestion).toHaveBeenCalled());

    view.unmount();
    renderCard(
      <UserAskCard
        toolBlock={block}
        sessionId={1}
        tabStateKey="tab-a"
        uiStateKey="ask:req-submit"
      />,
    );

    expect(screen.getByRole("textbox")).toHaveValue("");
  });

  describe("per-question answer contract", () => {
    function askBlock(
      questions: unknown[],
      extra: Record<string, unknown> = {},
    ): TranscriptBlock {
      return {
        type: "tool_use",
        toolName: "AskUserQuestion",
        canonical: {
          kind: "user.ask",
          userAsk: { requestId: "req-contract", questions, ...extra },
        },
      } as unknown as TranscriptBlock;
    }

    it("Given a Claude Code / Codex question without the backend switch, When it renders, Then Other stays offered even though isOther is false", () => {
      renderCard(
        <UserAskCard
          toolBlock={askBlock([
            {
              question: "Which?",
              header: "h",
              isOther: false,
              options: [{ label: "A", description: "" }],
            },
          ])}
          sessionId={1}
        />,
      );
      expect(screen.getByPlaceholderText(/Custom answer/)).toBeInTheDocument();
    });

    it("Given the backend disallows Other on a question with options, When it renders, Then only the options are answerable and their descriptions show", () => {
      renderCard(
        <UserAskCard
          toolBlock={askBlock([
            {
              question: "Which region?",
              header: "Region",
              disallowOther: true,
              options: [
                { label: "us", description: "Virginia data centre" },
                { label: "eu", description: "" },
              ],
            },
          ])}
          sessionId={1}
        />,
      );
      expect(screen.getByText("Virginia data centre")).toBeInTheDocument();
      expect(screen.queryByPlaceholderText(/Custom answer/)).toBeNull();
      expect(screen.queryByRole("textbox")).toBeNull();
    });

    it("Given the backend disallows Other on a question with no options, When it renders, Then the text input is still offered as the only way to answer", async () => {
      const user = userEvent.setup();
      renderCard(
        <UserAskCard
          toolBlock={askBlock([
            {
              question: "Name the branch",
              header: "Branch",
              disallowOther: true,
              options: [],
            },
          ])}
          sessionId={1}
        />,
      );
      await user.type(screen.getByRole("textbox"), "feature/x");
      await user.click(screen.getByRole("button", { name: /Submit Reply/ }));
      await waitFor(() =>
        expect(answerUserQuestion).toHaveBeenCalledWith(
          expect.objectContaining({
            answers: [
              {
                questionIndex: 0,
                labels: ["__other__"],
                otherText: "feature/x",
              },
            ],
          }),
        ),
      );
    });

    it("Given a multi-select question, When two options are picked, Then both are submitted", async () => {
      const user = userEvent.setup();
      renderCard(
        <UserAskCard
          toolBlock={askBlock([
            {
              question: "Which checks?",
              header: "Checks",
              multiSelect: true,
              disallowOther: true,
              options: [
                { label: "lint", description: "" },
                { label: "test", description: "" },
              ],
            },
          ])}
          sessionId={1}
        />,
      );
      await user.click(screen.getByRole("button", { name: /^lint$/ }));
      await user.click(screen.getByRole("button", { name: /^test$/ }));
      await user.click(screen.getByRole("button", { name: /Submit Reply/ }));
      await waitFor(() =>
        expect(answerUserQuestion).toHaveBeenCalledWith(
          expect.objectContaining({
            answers: [
              { questionIndex: 0, labels: ["lint", "test"], otherText: "" },
            ],
          }),
        ),
      );
    });

    it("Given a pending secret question, When it renders, Then the answer input is masked", () => {
      renderCard(
        <UserAskCard
          toolBlock={askBlock([
            {
              question: "API token?",
              header: "Token",
              isSecret: true,
              isOther: true,
              options: [],
            },
          ])}
          sessionId={1}
        />,
      );
      expect(screen.getByPlaceholderText(/Custom answer/)).toHaveAttribute(
        "type",
        "password",
      );
    });

    it("Given an answered secret question whose answer carries no content, When it renders, Then it shows answered without any answer input", () => {
      renderCard(
        <UserAskCard
          toolBlock={askBlock(
            [
              {
                question: "API token?",
                header: "Token",
                isSecret: true,
                isOther: true,
                options: [],
              },
            ],
            {
              answered: true,
              answers: [{ questionIndex: 0, labels: null }],
            },
          )}
          sessionId={1}
        />,
      );
      expect(screen.getByText("ANSWERED")).toBeInTheDocument();
      expect(
        screen.getByText("Answered · content not shown"),
      ).toBeInTheDocument();
      expect(screen.queryByPlaceholderText(/Custom answer/)).toBeNull();
    });

    it("Given an answered group whose secret answer carries no content, When it renders, Then the secret question's tab still reads as answered", () => {
      renderCard(
        <UserAskCard
          toolBlock={askBlock(
            [
              {
                question: "Region?",
                header: "Region",
                options: [{ label: "us", description: "" }],
              },
              {
                question: "API token?",
                header: "Token",
                isSecret: true,
                options: [],
              },
            ],
            {
              answered: true,
              answers: [
                { questionIndex: 0, labels: ["us"] },
                { questionIndex: 1, labels: null },
              ],
            },
          )}
          sessionId={1}
        />,
      );
      const tokenTab = screen.getByRole("tab", { name: /Q2 · Token/ });
      expect(tokenTab.querySelector("svg")?.getAttribute("class")).toMatch(
        /circle-check|check-circle/,
      );
    });
  });
});

describe("UserAskCard borderless", () => {
  const multi = {
    type: "tool_use",
    toolName: "AskUserQuestion",
    canonical: {
      kind: "user.ask",
      userAsk: {
        requestId: "req-bl",
        questions: [
          { question: "First?", header: "First", options: [{ label: "A" }] },
          { question: "Second?", header: "Second", options: [{ label: "B" }] },
        ],
      },
    },
  } as unknown as TranscriptBlock;

  it("Given several questions, When rendered, Then the tabs are a SegmentedControl tablist and the card has no border or divider", () => {
    renderCard(<UserAskCard toolBlock={multi} sessionId={1} />);
    const list = screen.getByRole("tablist");
    expect(list).toHaveAttribute("data-slot", "segmented-control");
    const tabs = screen.getAllByRole("tab");
    expect(tabs).toHaveLength(2);
    expect(tabs[0]).toHaveAttribute("aria-selected", "true");
    const card = screen.getByTestId("user-ask-card");
    expect(card.className).not.toMatch(/(^|\s)border(-|\s|$)/);
    const edged = Array.from(card.querySelectorAll("*")).filter((el) =>
      /(^|\s)(border|border-[tblrxy]|divide-\S+)(\s|$)/.test(
        el.getAttribute("class") ?? "",
      ),
    );
    expect(edged).toHaveLength(0);
  });

  it("Given several questions, When ArrowRight is pressed on a tab, Then the next question becomes active", async () => {
    const user = userEvent.setup();
    renderCard(<UserAskCard toolBlock={multi} sessionId={1} />);
    screen.getAllByRole("tab")[0].focus();
    await user.keyboard("{ArrowRight}");
    expect(screen.getAllByRole("tab")[1]).toHaveAttribute(
      "aria-selected",
      "true",
    );
    expect(screen.getByText("Second?")).toBeDefined();
  });
});

/**
 * 控制台手机形态（agentre-server 规格 2026-11-05「对话页 · 触屏输入」：所有输入框字号不低于
 * 16px）：提问照旧在转录里作答，那一格自填答案若是 14px，iOS 聚焦时会把整个视口放大。
 * 口径同包内 `<Input>`：移动端 16px、md 及以上回到原字号（桌面最小窗宽 860，逐像素不变）。
 */
describe("UserAskCard 自填答案的字号", () => {
  it("移动端 16px、md 以上回到 14px，不留裸 text-sm", () => {
    const block = {
      type: "tool_use",
      toolName: "AskUserQuestion",
      canonical: {
        kind: "user.ask",
        userAsk: {
          requestId: "req-font",
          questions: [{ question: "Q?", header: "Q", options: [] }],
        },
      },
    } as unknown as TranscriptBlock;
    renderCard(<UserAskCard toolBlock={block} sessionId={1} />);
    const classes = screen
      .getByPlaceholderText("Custom answer - enter your own response...")
      .className.split(/\s+/);

    expect(classes).toContain("text-base");
    expect(classes).toContain("md:text-sm");
    expect(classes).not.toContain("text-sm");
  });
});
