import { describe, it, expect, vi } from "vitest";
import { render, screen, fireEvent, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";

import { TLSTrustDialog } from "./tls-trust-dialog";

const PEM = "-----BEGIN CERTIFICATE-----\nMIIBxxxx\n-----END CERTIFICATE-----";

describe("TLSTrustDialog", () => {
  it("应用 with default mode passes ('','')", async () => {
    const onApply = vi.fn();
    render(
      <TLSTrustDialog
        open
        initialMode="default"
        initialPEM=""
        onClose={() => {}}
        onApply={onApply}
      />,
    );
    fireEvent.click(screen.getByRole("button", { name: "Apply" }));
    await waitFor(() => expect(onApply).toHaveBeenCalledWith("default", ""));
  });

  it("pin-cert without PEM shows inline error and does not call onApply", async () => {
    const user = userEvent.setup();
    const onApply = vi.fn();
    render(
      <TLSTrustDialog
        open
        initialMode="default"
        initialPEM=""
        onClose={() => {}}
        onApply={onApply}
      />,
    );
    await user.click(screen.getByText("Pin Certificate"));
    await user.click(screen.getByRole("button", { name: "Apply" }));
    await waitFor(() =>
      expect(screen.getByText("Paste PEM content")).toBeInTheDocument(),
    );
    expect(onApply).not.toHaveBeenCalled();
  });

  it("pin-cert with valid PEM calls onApply with mode+pem", async () => {
    const onApply = vi.fn();
    render(
      <TLSTrustDialog
        open
        initialMode="pin-cert"
        initialPEM=""
        onClose={() => {}}
        onApply={onApply}
      />,
    );
    const ta = screen.getByPlaceholderText(/BEGIN CERTIFICATE/);
    fireEvent.change(ta, { target: { value: PEM } });
    fireEvent.click(screen.getByRole("button", { name: "Apply" }));
    await waitFor(() => expect(onApply).toHaveBeenCalledWith("pin-cert", PEM));
  });

  it("skip-verify renders destructive label", () => {
    render(
      <TLSTrustDialog
        open
        initialMode="skip-verify"
        initialPEM=""
        onClose={() => {}}
        onApply={() => {}}
      />,
    );
    expect(screen.getByText("Not recommended")).toBeInTheDocument();
  });

  it("switching to ca-bundle without PEM blocks apply", async () => {
    const user = userEvent.setup();
    const onApply = vi.fn();
    render(
      <TLSTrustDialog
        open
        initialMode="default"
        initialPEM=""
        onClose={() => {}}
        onApply={onApply}
      />,
    );
    await user.click(screen.getByText("CA Bundle"));
    await user.click(screen.getByRole("button", { name: "Apply" }));
    await waitFor(() =>
      expect(screen.getByText("Paste PEM content")).toBeInTheDocument(),
    );
    expect(onApply).not.toHaveBeenCalled();
  });

  // 可及性:四个信任模式是 <button role="radio">,内容为空,包裹式 <label> 对 button
  // 不生效 —— 名称必须显式指到旁边那行标题(与登录对话框同一缺陷、同一改法)。
  describe("accessibility", () => {
    it("gives every trust mode an accessible name", () => {
      render(
        <TLSTrustDialog
          open
          initialMode="default"
          initialPEM=""
          onClose={() => {}}
          onApply={() => {}}
        />,
      );
      for (const name of [
        "OS Default",
        "Pin Certificate",
        "CA Bundle",
        "Skip Verification",
      ]) {
        expect(screen.getByRole("radio", { name })).toBeInTheDocument();
      }
      expect(screen.getByRole("radio", { name: "OS Default" })).toBeChecked();
    });

    it("labels the PEM field", async () => {
      const user = userEvent.setup();
      render(
        <TLSTrustDialog
          open
          initialMode="pin-cert"
          initialPEM=""
          onClose={() => {}}
          onApply={() => {}}
        />,
      );
      expect(
        screen.getByRole("textbox", { name: "Certificate (PEM)" }),
      ).toBeInTheDocument();
      await user.click(screen.getByRole("radio", { name: "CA Bundle" }));
      expect(
        screen.getByRole("textbox", { name: "CA Bundle (PEM)" }),
      ).toBeInTheDocument();
    });
  });
});
