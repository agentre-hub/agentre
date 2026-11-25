import * as React from "react";
import { useUiTranslation as useTranslation } from "../i18n";
import { Dialog as DialogPrimitive } from "radix-ui";
import { X } from "lucide-react";

import { cn } from "../lib/utils";
import { useTouch } from "./touch";

// 浮层关闭后焦点要回到打开它的控件。Radix 只在有 <Trigger> 时才知道回哪儿；这里的
// 对话框都是受控打开、没有 Trigger，只能退回「打开那一刻的 activeElement」——而 iOS
// Safari 点按钮不会让它拿焦点，那一刻 activeElement 是 body。所以再记一份最近一次
// 按下的可聚焦控件，activeElement 落空时用它。
const FOCUSABLE_TAGS = new Set([
  "BUTTON",
  "A",
  "INPUT",
  "SELECT",
  "TEXTAREA",
  "SUMMARY",
]);
const FOCUSABLE_ROLES = new Set(["button", "menuitem"]);

function closestFocusable(start: Element): Element | null {
  for (let el: Element | null = start; el; el = el.parentElement) {
    if (
      FOCUSABLE_TAGS.has(el.tagName) ||
      FOCUSABLE_ROLES.has(el.getAttribute("role") ?? "")
    ) {
      return el;
    }
  }
  return null;
}
let lastPressed: Element | null = null;
if (typeof document !== "undefined") {
  document.addEventListener(
    "pointerdown",
    (event) => {
      lastPressed =
        event.target instanceof Element ? closestFocusable(event.target) : null;
    },
    true,
  );
}

function currentOpener(): HTMLElement | null {
  const active = document.activeElement;
  if (active instanceof HTMLElement && active !== document.body) return active;
  return lastPressed instanceof HTMLElement ? lastPressed : null;
}

// 只在浮层真正打开时才挂载（Portal 关着不渲染子树），并且在 Radix 的 FocusScope 把焦点
// 移进浮层之前（布局副作用先于它的 useEffect）记下打开者。
function OpenerKeeper({
  openerRef,
}: {
  openerRef: React.RefObject<HTMLElement | null>;
}) {
  React.useLayoutEffect(() => {
    openerRef.current = currentOpener();
  }, [openerRef]);
  return null;
}

// 关闭时把焦点还给记下的打开者。宿主 preventDefault 就是接管焦点，不再插手。
// DialogShell 共用这一份。
function focusOpenerOnClose(
  event: Event,
  openerRef: React.RefObject<HTMLElement | null>,
) {
  if (event.defaultPrevented) return;
  const opener = openerRef.current;
  if (opener && document.contains(opener)) {
    event.preventDefault();
    opener.focus();
  }
}

const Dialog = DialogPrimitive.Root;
const DialogTrigger = DialogPrimitive.Trigger;
const DialogPortal = DialogPrimitive.Portal;
const DialogClose = DialogPrimitive.Close;

function DialogOverlay({
  className,
  ...props
}: React.ComponentProps<typeof DialogPrimitive.Overlay>) {
  return (
    <DialogPrimitive.Overlay
      data-slot="dialog-overlay"
      className={cn(
        "fixed inset-0 z-50 bg-scrim backdrop-blur-[3px] backdrop-saturate-150 motion-safe:data-[state=open]:animate-in motion-safe:data-[state=closed]:animate-out motion-safe:data-[state=closed]:fade-out-0 motion-safe:data-[state=open]:fade-in-0",
        className,
      )}
      {...props}
    />
  );
}

function DialogContent({
  className,
  children,
  showCloseButton = true,
  onCloseAutoFocus,
  touch,
  ...props
}: React.ComponentProps<typeof DialogPrimitive.Content> & {
  showCloseButton?: boolean;
  /**
   * 触屏档：点按钮不会让它拿焦点时（iOS Safari），关闭后把焦点还给最近按下的控件。
   * 不传则跟随最近的 TouchProvider；桌面（含 WKWebView）不开，行为与 Radix 默认一致。
   */
  touch?: boolean;
}) {
  const { t } = useTranslation();
  const isTouch = useTouch(touch);
  const openerRef = React.useRef<HTMLElement | null>(null);

  return (
    <DialogPortal>
      {isTouch ? <OpenerKeeper openerRef={openerRef} /> : null}
      <DialogOverlay />
      <DialogPrimitive.Content
        data-slot="dialog-content"
        onCloseAutoFocus={(event) => {
          onCloseAutoFocus?.(event);
          focusOpenerOnClose(event, openerRef);
        }}
        className={cn(
          "fixed left-[50%] top-[50%] z-50 grid w-[calc(100%-2rem)] max-w-[520px] translate-x-[-50%] translate-y-[-50%] gap-0 rounded-xl bg-card text-card-foreground shadow-overlay outline-none motion-safe:data-[state=open]:animate-in motion-safe:data-[state=closed]:animate-out motion-safe:data-[state=closed]:fade-out-0 motion-safe:data-[state=open]:fade-in-0 motion-safe:data-[state=closed]:zoom-out-95 motion-safe:data-[state=open]:zoom-in-95 motion-safe:data-[state=closed]:slide-out-to-top-[2%] motion-safe:data-[state=open]:slide-in-from-top-[2%]",
          className,
        )}
        {...props}
      >
        {children}
        {showCloseButton ? (
          <DialogPrimitive.Close
            aria-label={t("common.close")}
            className="absolute right-3 top-3 inline-flex size-7 items-center justify-center rounded-md text-muted-foreground transition-colors hover:bg-accent hover:text-foreground focus-visible:focus-outline"
          >
            <X className="size-4" aria-hidden="true" />
          </DialogPrimitive.Close>
        ) : null}
      </DialogPrimitive.Content>
    </DialogPortal>
  );
}

function DialogHeader({ className, ...props }: React.ComponentProps<"div">) {
  return (
    <div
      data-slot="dialog-header"
      className={cn("flex flex-col gap-1 px-5 py-4", className)}
      {...props}
    />
  );
}

function DialogBody({ className, ...props }: React.ComponentProps<"div">) {
  return (
    <div
      data-slot="dialog-body"
      className={cn("max-h-[70vh] overflow-y-auto px-5 py-4", className)}
      {...props}
    />
  );
}

function DialogFooter({ className, ...props }: React.ComponentProps<"div">) {
  return (
    <div
      data-slot="dialog-footer"
      className={cn(
        "flex items-center justify-end gap-2 bg-secondary/40 px-5 py-3",
        className,
      )}
      {...props}
    />
  );
}

function DialogTitle({
  className,
  ...props
}: React.ComponentProps<typeof DialogPrimitive.Title>) {
  return (
    <DialogPrimitive.Title
      data-slot="dialog-title"
      className={cn(
        "text-sm font-semibold tracking-tight text-foreground",
        className,
      )}
      {...props}
    />
  );
}

function DialogDescription({
  className,
  ...props
}: React.ComponentProps<typeof DialogPrimitive.Description>) {
  return (
    <DialogPrimitive.Description
      data-slot="dialog-description"
      className={cn("text-2xs text-muted-foreground", className)}
      {...props}
    />
  );
}

export {
  Dialog,
  DialogTrigger,
  DialogPortal,
  DialogOverlay,
  DialogClose,
  DialogContent,
  DialogHeader,
  DialogBody,
  DialogFooter,
  DialogTitle,
  DialogDescription,
};

// 包内共用（DialogShell），不进公共入口。
export { OpenerKeeper, focusOpenerOnClose };
