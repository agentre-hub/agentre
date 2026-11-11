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

  // B4：「应用」进行中为进行态、窗关不掉；失败留窗，事实句 + 默认折叠的详情。
  describe("apply in flight / failure", () => {
    it("Given apply is in flight, Then Apply and Cancel are disabled and a second click does not apply again", async () => {
      let resolve!: () => void;
      const onApply = vi.fn(() => new Promise<void>((res) => (resolve = res)));
      const onClose = vi.fn();
      render(
        <TLSTrustDialog
          open
          initialMode="default"
          initialPEM=""
          onClose={onClose}
          onApply={onApply}
        />,
      );
      fireEvent.click(screen.getByRole("button", { name: "Apply" }));
      await waitFor(() =>
        expect(screen.getByRole("button", { name: "Apply" })).toBeDisabled(),
      );
      expect(screen.getByRole("button", { name: "Cancel" })).toBeDisabled();
      expect(screen.getByRole("button", { name: "Close" })).toBeDisabled();
      fireEvent.click(screen.getByRole("button", { name: "Apply" }));
      expect(onApply).toHaveBeenCalledTimes(1);
      fireEvent.keyDown(document.activeElement ?? document.body, {
        key: "Escape",
      });
      expect(onClose).not.toHaveBeenCalled();

      resolve();
      await waitFor(() =>
        expect(screen.getByRole("button", { name: "Apply" })).toBeEnabled(),
      );
    });

    it("Given apply fails, Then the dialog stays open with a factual sentence and the raw text folded", async () => {
      const onApply = vi.fn().mockRejectedValue(new Error("x509: bad cert"));
      const onClose = vi.fn();
      render(
        <TLSTrustDialog
          open
          initialMode="default"
          initialPEM=""
          onClose={onClose}
          onApply={onApply}
        />,
      );
      fireEvent.click(screen.getByRole("button", { name: "Apply" }));

      const alert = await screen.findByRole("alert");
      expect(alert).toHaveTextContent("Couldn't apply the TLS trust setting");
      const raw = screen.getByText("x509: bad cert");
      expect(raw.closest("details")).not.toBeNull();
      expect(raw.closest("details")).not.toHaveAttribute("open");
      expect(onClose).not.toHaveBeenCalled();
      expect(screen.getByRole("button", { name: "Apply" })).toBeEnabled();
    });
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
