import * as React from "react";

import {
  Dialog,
  DialogBody,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "../ui/dialog";
import { cn } from "../lib/utils";

type AgentreDialogProps = {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  title: React.ReactNode;
  description?: React.ReactNode;
  children?: React.ReactNode;
  footer?: React.ReactNode;
  contentClassName?: string;
  bodyClassName?: string;
  footerClassName?: string;
  onSubmit?: React.FormEventHandler<HTMLFormElement>;
};

export function AgentreDialog({
  bodyClassName,
  children,
  contentClassName,
  description,
  footer,
  footerClassName,
  onOpenChange,
  onSubmit,
  open,
  title,
}: AgentreDialogProps) {
  const descriptionProps: React.ComponentProps<typeof DialogContent> =
    description ? {} : { "aria-describedby": undefined };
  const shell = (
    <>
      <DialogHeader>
        <DialogTitle>{title}</DialogTitle>
        {description ? (
          <DialogDescription>{description}</DialogDescription>
        ) : null}
      </DialogHeader>
      {children ? (
        <DialogBody className={bodyClassName}>{children}</DialogBody>
      ) : null}
      {footer ? (
        <DialogFooter className={footerClassName}>{footer}</DialogFooter>
      ) : null}
    </>
  );

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent
        {...descriptionProps}
        className={cn("max-w-md", contentClassName)}
      >
        {onSubmit ? (
          <form className="contents" onSubmit={onSubmit}>
            {shell}
          </form>
        ) : (
          shell
        )}
      </DialogContent>
    </Dialog>
  );
}
