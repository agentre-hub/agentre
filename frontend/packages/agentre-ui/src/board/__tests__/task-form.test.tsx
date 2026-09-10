import { render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";

import { PickerTrigger } from "../../engine/model-target-picker/picker-trigger";
import { TaskFormShell } from "../task-form";
import { TASK_PILL_CLASS } from "../task-form-pills";
import { initialTaskFormValue } from "../use-task-form";
import type { ExecTargetPort, ModelTargetPort } from "../exec-ports";
import type { LabelUsageView, TaskFormValue } from "../query-types";

const LABELS: LabelUsageView[] = [
  { id: 1, name: "bug", tone: "red", usageCount: 3 },
  { id: 2, name: "docs", tone: "gray", usageCount: 1 },
];

const PROJECTS = [
  { id: 1, name: "alpha", depth: 0, unfinished: 2 },
  { id: 2, name: "beta", depth: 1, unfinished: 1 },
];

const AGENTS = [
  { id: 5, name: "Ada", color: "agent-1" },
  { id: 6, name: "Bob", color: "agent-2" },
];

function renderForm(
  over: Partial<React.ComponentProps<typeof TaskFormShell>> = {},
) {
  const onSave = vi.fn().mockResolvedValue(undefined);
  const onClose = vi.fn();
  const onDelete = vi.fn();
  const execTargetPort = vi.fn<ExecTargetPort>(() => (
    <button type="button" data-testid="exec-target-pill">
      local
    </button>
  ));
  const modelTargetPort = vi.fn<ModelTargetPort>(() => (
    <button type="button" data-testid="model-pill">
      opus
    </button>
  ));

  render(
    <TaskFormShell
      initial={initialTaskFormValue({
        stage: "review",
        scope: { kind: "project", projectId: 2 },
      })}
      projects={PROJECTS}
      labels={LABELS}
      agentOptions={AGENTS}
      execTargetPort={execTargetPort}
      modelTargetPort={modelTargetPort}
      onSave={onSave}
      onClose={onClose}
      onDelete={onDelete}
      {...over}
    />,
  );

  return { onSave, onClose, onDelete, execTargetPort, modelTargetPort };
}

describe("默认值跟随上下文", () => {
  it("Given the + of a column and a project scope, When the form opens, Then stage comes from the column and the project from the scope", () => {
    expect(
      initialTaskFormValue({
        stage: "doing",
        scope: { kind: "project", projectId: 4 },
      }),
    ).toMatchObject({ stage: "doing", projectId: 4 });
  });

  it("Given a scope that is not a single project, When the form opens, Then the task starts unassigned in the todo column", () => {
    expect(initialTaskFormValue({ scope: { kind: "all" } })).toMatchObject({
      stage: "todo",
      projectId: null,
    });
    expect(
      initialTaskFormValue({ scope: { kind: "unassigned" } }).projectId,
    ).toBeNull();
  });

  it("Given an existing task, When the form opens, Then its own values win over the context", () => {
    const issue: Partial<TaskFormValue> = {
      id: 12,
      title: "t",
      stage: "done",
      projectId: 1,
    };

    expect(
      initialTaskFormValue({ issue, stage: "todo", scope: { kind: "all" } }),
    ).toMatchObject({ id: 12, stage: "done", projectId: 1 });
  });
});

describe("表单外壳", () => {
  it("Given a new task, When the header renders, Then it is a breadcrumb of project › 新建任务", () => {
    renderForm();

    expect(screen.getByTestId("task-form-breadcrumb")).toHaveTextContent(
      "beta",
    );
    expect(screen.getByTestId("task-form-breadcrumb")).toHaveTextContent(
      "New task",
    );
  });

  it("Given an existing task, When the header renders, Then the second crumb is its number and the updated time shows", () => {
    renderForm({
      initial: initialTaskFormValue({
        issue: { id: 12, title: "t", projectId: 1, updatedAt: Date.now() },
      }),
    });

    expect(screen.getByTestId("task-form-breadcrumb")).toHaveTextContent("#12");
    expect(screen.getByTestId("task-form-updated")).toBeInTheDocument();
  });

  it("Given the body, When it renders, Then title and description carry no input frame and are told apart by size", () => {
    renderForm();

    const title = screen.getByTestId("task-title");
    const description = screen.getByTestId("task-description");

    expect(title.className).toContain("border-0");
    expect(description.className).toContain("border-0");
    expect(title.className).toContain("text-base");
    expect(description.className).toContain("text-sm");
  });

  it("Given the property pills, When they render, Then a divider separates them from the execution pills", () => {
    renderForm();

    const row = screen.getByTestId("task-form-pills");
    expect(within(row).getByTestId("task-pill-divider")).toBeInTheDocument();
  });

  it("Given no title, When submit is pressed, Then it does not save", async () => {
    const user = userEvent.setup();
    const { onSave } = renderForm();

    await user.click(screen.getByTestId("task-form-submit"));

    expect(onSave).not.toHaveBeenCalled();
  });
});

describe("阶段与项目两颗属性 pill", () => {
  it("Given an existing task, When its stage pill is used, Then the stage is still editable", async () => {
    const user = userEvent.setup();
    const { onSave } = renderForm({
      initial: initialTaskFormValue({
        issue: { id: 12, title: "t", stage: "todo", projectId: 1 },
      }),
    });

    await user.click(screen.getByTestId("task-pill-stage"));
    await user.click(screen.getByTestId("task-stage-doing"));
    await user.click(screen.getByTestId("task-form-submit"));

    expect(onSave).toHaveBeenCalledWith(
      expect.objectContaining({ id: 12, stage: "doing" }),
    );
  });

  it("Given a project on the task, When the pill's × is used, Then it goes back to 未归属", async () => {
    const user = userEvent.setup();
    const { onSave } = renderForm({
      initial: initialTaskFormValue({
        issue: { id: 12, title: "t", projectId: 1 },
      }),
    });

    await user.click(screen.getByTestId("task-pill-project-clear"));
    await user.click(screen.getByTestId("task-form-submit"));

    expect(onSave).toHaveBeenCalledWith(
      expect.objectContaining({ projectId: null }),
    );
  });
});

describe("执行归属三颗 pill", () => {
  it("Given no agent yet, When the row renders, Then machine and model are disabled and the host is never asked to resolve them", () => {
    const { execTargetPort, modelTargetPort } = renderForm();

    expect(screen.getByTestId("exec-target-pill")).toBeDisabled();
    expect(screen.getByTestId("model-pill")).toBeDisabled();
    expect(execTargetPort).not.toHaveBeenCalled();
    expect(modelTargetPort).not.toHaveBeenCalled();
  });

  it("Given an agent is chosen, When the row renders, Then both ports get the shared pill shape and the resolving context", async () => {
    const user = userEvent.setup();
    const { execTargetPort, modelTargetPort } = renderForm();

    await user.click(screen.getByTestId("task-pill-agent"));
    await user.click(screen.getByTestId("task-agent-5"));

    await waitFor(() => expect(execTargetPort).toHaveBeenCalled());
    expect(execTargetPort).toHaveBeenLastCalledWith(
      expect.objectContaining({ agentId: 5, projectId: 2 }),
    );
    expect(execTargetPort.mock.calls.at(-1)?.[0].className).toContain(
      "rounded-full",
    );
    expect(modelTargetPort).toHaveBeenLastCalledWith(
      expect.objectContaining({ agentId: 5 }),
    );
  });

  it("Given a save in flight, When the ports are asked again, Then machine and model are locked too, not just the agent", async () => {
    const user = userEvent.setup();
    let release = () => {};
    const { execTargetPort, modelTargetPort } = renderForm({
      initial: initialTaskFormValue({ issue: { title: "t" } }),
      onSave: vi.fn(
        () =>
          new Promise<void>((resolve) => {
            release = resolve;
          }),
      ),
    });

    await user.click(screen.getByTestId("task-pill-agent"));
    await user.click(screen.getByTestId("task-agent-5"));
    await waitFor(() => expect(execTargetPort).toHaveBeenCalled());
    await user.click(screen.getByTestId("task-form-submit"));

    // 「其余字段保持可读不可改」把这两颗也算在内 —— 提交在飞时还能换机器，
    // 存下去的就不是提交的那一份。
    await waitFor(() =>
      expect(execTargetPort).toHaveBeenLastCalledWith(
        expect.objectContaining({ disabled: true }),
      ),
    );
    expect(modelTargetPort).toHaveBeenLastCalledWith(
      expect.objectContaining({ disabled: true }),
    );

    release();
    await waitFor(() =>
      expect(screen.getByTestId("task-form-submit")).not.toBeDisabled(),
    );
  });

  it("Given the shared pill shape reaches ModelTargetPicker's own trigger, When the two class strings merge, Then the picker's full-width filled box loses to the pill", () => {
    render(
      <PickerTrigger
        open={false}
        disabled={false}
        invalid={false}
        compact
        selectedLabel="opus"
        triggerText="opus"
        testId="merged-trigger"
        className={TASK_PILL_CLASS}
      />,
    );

    // 三颗触发器统一成同一形状：模型那一颗自带 `w-full` / `justify-between` /
    // `bg-input-bg`，留着它们就是「三颗三个样子」那个被否掉的做法。
    const trigger = screen.getByTestId("merged-trigger");
    expect(trigger.className).not.toContain("w-full");
    expect(trigger.className).not.toContain("justify-between");
    expect(trigger.className).not.toContain("bg-input-bg");
    expect(trigger.className).toContain("rounded-full");
  });
});

describe("提交", () => {
  it("Given a failing save, When it comes back, Then the error block sits directly above the submit button", async () => {
    const user = userEvent.setup();
    renderForm({
      initial: initialTaskFormValue({ issue: { title: "t" } }),
      onSave: vi.fn().mockRejectedValue(new Error("没存上")),
    });

    await user.click(screen.getByTestId("task-form-submit"));

    const error = await screen.findByTestId("task-form-error");
    const footer = screen.getByTestId("task-form-footer");
    const submit = screen.getByTestId("task-form-submit");

    expect(error).toHaveTextContent("没存上");
    expect(footer.firstElementChild).toBe(error);
    expect(footer.contains(submit)).toBe(true);
    expect(screen.getByTestId("task-form-body").contains(error)).toBe(false);
  });

  it("Given a save in flight, When it has not come back, Then the button spins and the fields are readable but not editable", async () => {
    const user = userEvent.setup();
    let release = () => {};
    renderForm({
      initial: initialTaskFormValue({ issue: { title: "t" } }),
      onSave: vi.fn(
        () =>
          new Promise<void>((resolve) => {
            release = resolve;
          }),
      ),
    });

    await user.click(screen.getByTestId("task-form-submit"));

    expect(screen.getByTestId("task-form-submit")).toBeDisabled();
    expect(
      within(screen.getByTestId("task-form-submit")).getByRole("status"),
    ).toBeInTheDocument();
    expect(screen.getByTestId("task-title")).toHaveAttribute("readonly");

    release();
    await waitFor(() =>
      expect(screen.getByTestId("task-form-submit")).not.toBeDisabled(),
    );
  });
});

// 标签是任务表单上真正会被改的那一段，此前一条覆盖都没有：把选项按钮的 onChange
// 改成空函数、或者把 chosen 过滤在错的那一边，整套用例都照样绿。
describe("表单上的标签", () => {
  it("Given a label picked and one dropped, When the task is saved, Then only the remaining label travels", async () => {
    const user = userEvent.setup();
    const { onSave } = renderForm();

    await user.type(screen.getByTestId("task-title"), "接超时告警");

    await user.click(screen.getByTestId("task-label-add"));
    await user.click(screen.getByTestId("task-label-option-1"));
    await user.click(screen.getByTestId("task-label-option-2"));
    expect(screen.getByTestId("task-label-1")).toHaveTextContent("bug");
    expect(screen.getByTestId("task-label-2")).toHaveTextContent("docs");

    // 摘掉第一颗：留下来的那一颗才是保存时该带走的。
    await user.click(screen.getByTestId("task-label-remove-1"));
    expect(screen.queryByTestId("task-label-1")).toBeNull();

    await user.click(screen.getByTestId("task-form-submit"));

    await waitFor(() => expect(onSave).toHaveBeenCalled());
    expect(onSave.mock.calls.at(-1)?.[0]).toMatchObject({ labelIds: [2] });
  });

  it("Given a label already on the task, When the picker opens, Then that option reads as pressed", async () => {
    const user = userEvent.setup();
    renderForm({
      initial: initialTaskFormValue({
        issue: { labelIds: [2] },
        scope: { kind: "all" },
      }),
    });

    await user.click(screen.getByTestId("task-label-add"));

    expect(screen.getByTestId("task-label-option-2")).toHaveAttribute(
      "aria-pressed",
      "true",
    );
    expect(screen.getByTestId("task-label-option-1")).toHaveAttribute(
      "aria-pressed",
      "false",
    );
  });
});
