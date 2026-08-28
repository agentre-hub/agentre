import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import {
  render,
  screen,
  fireEvent,
  waitFor,
  act,
} from "@testing-library/react";

const browserOpenURLMock = vi.fn();
vi.mock("../../../../wailsjs/runtime/runtime", () => ({
  BrowserOpenURL: (url: string) => browserOpenURLMock(url),
}));

const sonnerMocks = vi.hoisted(() => ({
  toast: { error: vi.fn(), success: vi.fn() },
}));
vi.mock("sonner", () => sonnerMocks);

import type { server_svc } from "../../../../wailsjs/go/models";
import { LoginDialog, DEFAULT_SERVER_URL } from "./login-dialog";

const RESULT: server_svc.StartLoginResult = {
  deviceCode: "device-abc",
  userCode: "ABCD-1234",
  verificationURI: "https://hub.example.com/device",
  verificationURIComplete: "https://hub.example.com/device?code=ABCD-1234",
  interval: 5,
  expiresIn: 900,
};

function renderDialog(
  overrides: Partial<Parameters<typeof LoginDialog>[0]> = {},
) {
  const props = {
    open: true,
    initialUrl: "",
    onClose: vi.fn(),
    onLoggedIn: vi.fn(),
    checkURL: vi.fn().mockResolvedValue("0.3.0"),
    startLogin: vi.fn().mockResolvedValue(RESULT),
    pollLoginToken: vi.fn().mockResolvedValue(false),
    cancelLogin: vi.fn().mockResolvedValue(undefined),
    ...overrides,
  };
  render(<LoginDialog {...props} />);
  return props;
}

// 默认官方云:直接点 Continue 就用官方地址开始登录,无需输入。
async function startFlow(props: ReturnType<typeof renderDialog>) {
  fireEvent.click(screen.getByRole("button", { name: "Continue" }));
  await waitFor(() => expect(props.startLogin).toHaveBeenCalled());
}

function mockClipboard() {
  const writeText = vi.fn().mockResolvedValue(undefined);
  Object.defineProperty(navigator, "clipboard", {
    configurable: true,
    value: { writeText },
  });
  return writeText;
}

describe("LoginDialog", () => {
  beforeEach(() => {
    browserOpenURLMock.mockReset();
    sonnerMocks.toast.success.mockReset();
    sonnerMocks.toast.error.mockReset();
  });

  afterEach(() => {
    vi.useRealTimers();
  });

  describe("server selection", () => {
    // 新用户(无历史服务器)默认官方云:不显示 URL 输入框,直接继续即用官方地址。
    it("defaults to the official server when no URL is remembered", async () => {
      const props = renderDialog();
      expect(
        screen.queryByPlaceholderText(/hub\.example\.com/),
      ).not.toBeInTheDocument();
      fireEvent.click(screen.getByRole("button", { name: "Continue" }));
      await waitFor(() =>
        expect(props.startLogin).toHaveBeenCalledWith(DEFAULT_SERVER_URL),
      );
    });

    // URL 记忆:上次连过自建服务器 → 重开对话框记住并选中「自定义」。
    it("remembers a previously used custom server and pre-selects it", async () => {
      const props = renderDialog({ initialUrl: "https://myhub.example.com" });
      const input = screen.getByPlaceholderText(
        /hub\.example\.com/,
      ) as HTMLInputElement;
      expect(input.value).toBe("https://myhub.example.com");
      fireEvent.click(screen.getByRole("button", { name: "Continue" }));
      await waitFor(() =>
        expect(props.startLogin).toHaveBeenCalledWith(
          "https://myhub.example.com",
        ),
      );
    });

    // 用户曾经手动连过官方地址 → 仍归为「官方」,不显示自定义输入框。
    it("treats the official URL as official regardless of trailing slash", async () => {
      const props = renderDialog({ initialUrl: "https://app.agentrehub.com" });
      expect(
        screen.queryByPlaceholderText(/hub\.example\.com/),
      ).not.toBeInTheDocument();
      fireEvent.click(screen.getByRole("button", { name: "Continue" }));
      await waitFor(() =>
        expect(props.startLogin).toHaveBeenCalledWith(DEFAULT_SERVER_URL),
      );
    });

    // 选「自定义服务器」展开地址输入,继续用输入的地址。
    it("switching to custom server reveals the URL field and signs in with it", async () => {
      const props = renderDialog();
      fireEvent.click(screen.getByText("Custom server"));
      const input = screen.getByPlaceholderText(
        /hub\.example\.com/,
      ) as HTMLInputElement;
      fireEvent.change(input, {
        target: { value: "https://selfhost.example.com" },
      });
      fireEvent.click(screen.getByRole("button", { name: "Continue" }));
      await waitFor(() =>
        expect(props.startLogin).toHaveBeenCalledWith(
          "https://selfhost.example.com",
        ),
      );
    });

    // 自定义模式下未填地址时 Continue 不可用。
    it("keeps Continue disabled for custom server until a URL is entered", () => {
      renderDialog();
      fireEvent.click(screen.getByText("Custom server"));
      expect(screen.getByRole("button", { name: "Continue" })).toBeDisabled();
      fireEvent.change(screen.getByPlaceholderText(/hub\.example\.com/), {
        target: { value: "https://selfhost.example.com" },
      });
      expect(
        screen.getByRole("button", { name: "Continue" }),
      ).not.toBeDisabled();
    });
  });

  describe("waiting phase", () => {
    // a) starting login shows the user code + verification URL, and offers an
    // action that opens the browser.
    it("shows the user code and verification URL after starting, and opens the browser on demand", async () => {
      const props = renderDialog();
      await startFlow(props);

      await waitFor(() =>
        expect(screen.getByText("ABCD-1234")).toBeInTheDocument(),
      );
      expect(
        screen.getByText("https://hub.example.com/device"),
      ).toBeInTheDocument();

      fireEvent.click(screen.getByRole("button", { name: "Open Browser" }));
      expect(browserOpenURLMock).toHaveBeenCalledWith(
        "https://hub.example.com/device?code=ABCD-1234",
      );
    });

    // 复制按钮把验证码写进剪贴板,并给出「已复制」反馈。
    it("copies the user code to the clipboard and confirms", async () => {
      const writeText = mockClipboard();
      const props = renderDialog();
      await startFlow(props);
      await waitFor(() =>
        expect(screen.getByText("ABCD-1234")).toBeInTheDocument(),
      );

      fireEvent.click(screen.getByRole("button", { name: "Copy" }));
      await waitFor(() => expect(writeText).toHaveBeenCalledWith("ABCD-1234"));
      expect(sonnerMocks.toast.success).toHaveBeenCalled();
      expect(screen.getByText("Copied")).toBeInTheDocument();
    });

    // 倒计时从 expiresIn 起,随时间递减显示剩余时间。
    it("shows a live countdown until the code expires", async () => {
      vi.useFakeTimers();
      const props = renderDialog();

      await act(async () => {
        fireEvent.click(screen.getByRole("button", { name: "Continue" }));
        await Promise.resolve();
        await Promise.resolve();
      });
      expect(props.startLogin).toHaveBeenCalled();
      expect(screen.getByText("Code expires in 15:00")).toBeInTheDocument();

      await act(async () => {
        await vi.advanceTimersByTimeAsync(60_000);
      });
      expect(screen.getByText("Code expires in 14:00")).toBeInTheDocument();
    });
  });

  describe("polling lifecycle", () => {
    // b) polling runs at the returned Interval and, on success, the UI moves
    // to the logged-in state without further user action.
    it("polls at the returned Interval and auto-completes on success", async () => {
      vi.useFakeTimers();
      const pollLoginToken = vi
        .fn()
        .mockResolvedValueOnce(false)
        .mockResolvedValueOnce(false)
        .mockResolvedValueOnce(true);
      const onLoggedIn = vi.fn();
      const onClose = vi.fn();
      const props = renderDialog({ pollLoginToken, onLoggedIn, onClose });

      await act(async () => {
        fireEvent.click(screen.getByRole("button", { name: "Continue" }));
        await Promise.resolve();
        await Promise.resolve();
      });
      expect(props.startLogin).toHaveBeenCalled();

      // No poll before a full interval has elapsed.
      expect(pollLoginToken).not.toHaveBeenCalled();

      await act(async () => {
        await vi.advanceTimersByTimeAsync(5_000);
      });
      expect(pollLoginToken).toHaveBeenCalledTimes(1);

      await act(async () => {
        await vi.advanceTimersByTimeAsync(5_000);
      });
      expect(pollLoginToken).toHaveBeenCalledTimes(2);

      await act(async () => {
        await vi.advanceTimersByTimeAsync(5_000);
      });
      expect(pollLoginToken).toHaveBeenCalledTimes(3);
      expect(onLoggedIn).toHaveBeenCalled();
      expect(onClose).toHaveBeenCalled();

      // No further polling once logged in.
      pollLoginToken.mockClear();
      await act(async () => {
        await vi.advanceTimersByTimeAsync(20_000);
      });
      expect(pollLoginToken).not.toHaveBeenCalled();
    });

    // b2) 卸载发生在 startLogin 还在飞的时候:迟到的那次 start 不得再装一个
    // 谁也清不掉的 interval。stopPolling 只清**已经存在**的 timer,而 start 过去
    // 是在 await 之后才读 attemptRef —— 读到的是卸载清理刚 +1 过的那个值,于是
    // 身份校验反而放行,轮询就此每 5s 跑到进程结束(还会把 onLoggedIn 打进已死的父组件)。
    it("does not leave a polling interval behind when it unmounts mid-start", async () => {
      vi.useFakeTimers();
      let releaseStart: (r: server_svc.StartLoginResult) => void = () => {};
      const startLogin = vi.fn(
        () =>
          new Promise<server_svc.StartLoginResult>((resolve) => {
            releaseStart = resolve;
          }),
      );
      const pollLoginToken = vi.fn().mockResolvedValue(false);
      const onLoggedIn = vi.fn();
      const { unmount } = render(
        <LoginDialog
          open
          initialUrl=""
          onClose={vi.fn()}
          onLoggedIn={onLoggedIn}
          checkURL={vi.fn().mockResolvedValue("0.3.0")}
          startLogin={startLogin}
          pollLoginToken={pollLoginToken}
          cancelLogin={vi.fn().mockResolvedValue(undefined)}
        />,
      );

      await act(async () => {
        fireEvent.click(screen.getByRole("button", { name: "Continue" }));
        await Promise.resolve();
        await Promise.resolve();
      });
      expect(startLogin).toHaveBeenCalled();

      // 用户在这一次网络往返还没回来的时候离开了设置页。
      unmount();
      await act(async () => {
        releaseStart(RESULT);
        await Promise.resolve();
        await Promise.resolve();
      });

      await act(async () => {
        await vi.advanceTimersByTimeAsync(30_000);
      });
      expect(pollLoginToken).not.toHaveBeenCalled();
      expect(onLoggedIn).not.toHaveBeenCalled();
    });

    // c) cancelling stops the polling and calls ServerCancelLogin.
    it("cancelling stops polling and calls cancelLogin", async () => {
      vi.useFakeTimers();
      const pollLoginToken = vi.fn().mockResolvedValue(false);
      const cancelLogin = vi.fn().mockResolvedValue(undefined);
      const props = renderDialog({ pollLoginToken, cancelLogin });

      await act(async () => {
        fireEvent.click(screen.getByRole("button", { name: "Continue" }));
        await Promise.resolve();
        await Promise.resolve();
      });
      expect(props.startLogin).toHaveBeenCalled();

      await act(async () => {
        await vi.advanceTimersByTimeAsync(5_000);
      });
      expect(pollLoginToken).toHaveBeenCalledTimes(1);

      fireEvent.click(screen.getByRole("button", { name: "Cancel" }));
      // cancelLogin() is invoked synchronously inside the click handler (only
      // its resolution is async) — asserting immediately avoids waitFor's
      // setTimeout-based polling, which fake timers would otherwise stall.
      expect(cancelLogin).toHaveBeenCalled();

      pollLoginToken.mockClear();
      await act(async () => {
        await vi.advanceTimersByTimeAsync(30_000);
      });
      expect(pollLoginToken).not.toHaveBeenCalled();
    });

    // 取消只清了 interval,在飞的那一次 pollLoginToken 还挂着。它随后 resolve(true)
    // ——用户在浏览器里已经批准过、只是又按了取消——旧代码照样跑 onLoggedIn()+onClose(),
    // 于是一次被明确取消(且已经发过 cancelLogin)的登录把桌面端标成已登录。
    it("ignores an in-flight poll that settles after the user cancelled", async () => {
      let releasePoll: (done: boolean) => void = () => {};
      const pollLoginToken = vi.fn().mockImplementation(
        () =>
          new Promise<boolean>((resolve) => {
            releasePoll = resolve;
          }),
      );
      const cancelLogin = vi.fn().mockResolvedValue(undefined);
      const props = renderDialog({ pollLoginToken, cancelLogin });

      vi.useFakeTimers();
      await act(async () => {
        fireEvent.click(screen.getByRole("button", { name: "Continue" }));
        await Promise.resolve();
        await Promise.resolve();
      });
      await act(async () => {
        await vi.advanceTimersByTimeAsync(5_000);
      });
      expect(pollLoginToken).toHaveBeenCalledTimes(1);

      fireEvent.click(screen.getByRole("button", { name: "Cancel" }));
      expect(cancelLogin).toHaveBeenCalled();
      expect(props.onLoggedIn).not.toHaveBeenCalled();

      // 取消之后那一次在飞的轮询才落定,并且带回「已批准」。
      await act(async () => {
        releasePoll(true);
        await Promise.resolve();
        await Promise.resolve();
      });
      expect(props.onLoggedIn).not.toHaveBeenCalled();
    });

    // e) an error from start surfaces to the user rather than a silent spinner.
    it("surfaces a start error and lets the user retry", async () => {
      const startLogin = vi
        .fn()
        .mockRejectedValue(new Error("server: unreachable"));
      renderDialog({ startLogin });

      fireEvent.click(screen.getByRole("button", { name: "Continue" }));

      await waitFor(() =>
        expect(
          screen.getByText(
            "Can't reach the server. Check the URL and try again.",
          ),
        ).toBeInTheDocument(),
      );
      // Not stuck showing "Connecting..." — the action is available again.
      expect(
        screen.getByRole("button", { name: "Continue" }),
      ).not.toBeDisabled();
    });

    // e) an error from poll surfaces to the user rather than a silent spinner.
    it("surfaces a poll error", async () => {
      vi.useFakeTimers();
      const pollLoginToken = vi
        .fn()
        .mockRejectedValue(new Error("server: access denied"));
      const props = renderDialog({ pollLoginToken });

      await act(async () => {
        fireEvent.click(screen.getByRole("button", { name: "Continue" }));
        await Promise.resolve();
        await Promise.resolve();
      });
      expect(props.startLogin).toHaveBeenCalled();

      await act(async () => {
        await vi.advanceTimersByTimeAsync(5_000);
      });

      expect(screen.getByText("Sign-in was declined.")).toBeInTheDocument();

      pollLoginToken.mockClear();
      await act(async () => {
        await vi.advanceTimersByTimeAsync(30_000);
      });
      expect(pollLoginToken).not.toHaveBeenCalled();
    });

    // grounding fact: expiry uses expiresIn — stop polling locally once the
    // device code's lifetime has elapsed, even if the server never says so.
    it("stops polling once expiresIn elapses without a successful poll", async () => {
      vi.useFakeTimers();
      const pollLoginToken = vi.fn().mockResolvedValue(false);
      const startLogin = vi.fn().mockResolvedValue({
        ...RESULT,
        interval: 5,
        expiresIn: 12,
      });
      const props = renderDialog({ pollLoginToken, startLogin });

      await act(async () => {
        fireEvent.click(screen.getByRole("button", { name: "Continue" }));
        await Promise.resolve();
        await Promise.resolve();
      });
      expect(props.startLogin).toHaveBeenCalled();

      await act(async () => {
        await vi.advanceTimersByTimeAsync(5_000);
      });
      expect(pollLoginToken).toHaveBeenCalledTimes(1);

      await act(async () => {
        await vi.advanceTimersByTimeAsync(5_000); // t=10s, still < 12s expiry
      });
      expect(pollLoginToken).toHaveBeenCalledTimes(2);

      await act(async () => {
        await vi.advanceTimersByTimeAsync(5_000); // t=15s, past 12s expiry
      });
      expect(
        screen.getByText("The code expired. Sign in again."),
      ).toBeInTheDocument();

      pollLoginToken.mockClear();
      await act(async () => {
        await vi.advanceTimersByTimeAsync(30_000);
      });
      expect(pollLoginToken).not.toHaveBeenCalled();
    });
  });
});
