import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";

import { DevicePairingForm } from "./device-pairing-form";

function renderForm(onSubmit = vi.fn().mockResolvedValue(undefined)) {
  render(
    <DevicePairingForm
      cancelLabel="Back to service"
      onCancel={() => {}}
      onSubmit={onSubmit}
      submitLabel="Pair and verify"
    />,
  );
  return onSubmit;
}

describe("DevicePairingForm", () => {
  it("reuses the existing URL, code, derived-name, and TLS request contract", async () => {
    const user = userEvent.setup();
    const onSubmit = renderForm();
    const submit = screen.getByRole("button", { name: "Pair and verify" });

    expect(submit).toBeDisabled();
    await user.type(screen.getByLabelText("Address"), "ws://host:7456/rpc");
    await user.type(screen.getByLabelText("Pairing Code"), "abc2def");
    expect(screen.getByLabelText("Pairing Code")).toHaveValue("ABC2DE");
    expect(submit).toBeEnabled();

    await user.click(submit);

    await waitFor(() =>
      expect(onSubmit).toHaveBeenCalledWith({
        url: "ws://host:7456/rpc",
        pairingCode: "ABC2DE",
        displayName: "host",
        tlsMode: "default",
        tlsCertPEM: "",
      }),
    );
  });

  // 以下三条随 AddDeviceDialog 一并迁来:配对表单现在是这些校验规则的唯一宿主。
  it("keeps the submit action disabled until the address and the code are both valid", async () => {
    const user = userEvent.setup();
    renderForm();
    const submit = screen.getByRole("button", { name: "Pair and verify" });

    expect(submit).toBeDisabled();
    await user.type(screen.getByLabelText("Address"), "ws://h/rpc");
    expect(submit).toBeDisabled();
    await user.type(screen.getByLabelText("Pairing Code"), "ABC2DE");
    expect(submit).toBeEnabled();
  });

  it("shows the address and code errors only after blur, then revalidates live while typing", async () => {
    const user = userEvent.setup();
    renderForm();
    const urlError =
      "Address must end with /rpc, e.g. ws://192.168.1.100:7456/rpc";
    const codeError = "Pairing code must be exactly 6 characters";

    expect(screen.getByText("6-character pairing code")).toBeInTheDocument();
    await user.type(
      screen.getByLabelText("Address"),
      "ws://192.168.1.100:7456",
    );
    expect(screen.queryByText(urlError)).not.toBeInTheDocument();
    await user.type(screen.getByLabelText("Pairing Code"), "ABCDE");
    expect(screen.getByText(urlError)).toBeInTheDocument();
    expect(screen.queryByText(codeError)).not.toBeInTheDocument();

    await user.click(screen.getByLabelText("Display Name (optional)"));
    expect(screen.getByText(codeError)).toBeInTheDocument();

    await user.type(screen.getByLabelText("Pairing Code"), "F");
    expect(screen.queryByText(codeError)).not.toBeInTheDocument();
    await user.type(screen.getByLabelText("Address"), "/rpc");
    expect(screen.queryByText(urlError)).not.toBeInTheDocument();
  });

  it("shows the localized TLS mode name instead of the internal value", async () => {
    const user = userEvent.setup();
    renderForm();

    await user.click(screen.getByRole("button", { name: /Advanced/ }));
    await user.click(screen.getByLabelText("Skip Verification"));
    await user.click(screen.getByRole("button", { name: "Apply" }));

    const entry = screen.getByRole("button", { name: /Advanced/ });
    expect(entry).toHaveTextContent("Skip Verification");
    expect(entry).not.toHaveTextContent("skip-verify");
  });

  it("accepts any six characters as the pairing code, without alphabet validation", async () => {
    const user = userEvent.setup();
    renderForm();

    await user.type(screen.getByLabelText("Address"), "ws://h/rpc");
    await user.type(screen.getByLabelText("Pairing Code"), "o01i89");

    expect(screen.getByLabelText("Pairing Code")).toHaveValue("O01I89");
    expect(
      screen.getByRole("button", { name: "Pair and verify" }),
    ).toBeEnabled();
  });

  it("shows the concrete submit error and retains every entered value", async () => {
    const user = userEvent.setup();
    const onSubmit = vi
      .fn()
      .mockRejectedValue(new Error("Pairing code expired"));
    renderForm(onSubmit);

    await user.type(screen.getByLabelText("Address"), "wss://host:7456/rpc");
    await user.type(screen.getByLabelText("Pairing Code"), "ABC2DE");
    await user.type(
      screen.getByLabelText("Display Name (optional)"),
      "Build box",
    );
    await user.click(screen.getByRole("button", { name: "Pair and verify" }));

    expect(await screen.findByText("Pairing code expired")).toBeInTheDocument();
    expect(screen.getByLabelText("Address")).toHaveValue("wss://host:7456/rpc");
    expect(screen.getByLabelText("Pairing Code")).toHaveValue("ABC2DE");
    expect(screen.getByLabelText("Display Name (optional)")).toHaveValue(
      "Build box",
    );
  });
});
