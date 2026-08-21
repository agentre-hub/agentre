import type { ComponentProps } from "react";

import { cn } from "../../lib/utils";

function Field({ className, ...props }: ComponentProps<"div"> & { orientation?: "horizontal" }) {
  return <div className={cn("grid gap-2", className)} {...props} />;
}
function FieldGroup({ className, ...props }: ComponentProps<"div">) {
  return <div className={cn("grid gap-4", className)} {...props} />;
}
function FieldLabel({ className, ...props }: ComponentProps<"label">) {
  return <label className={cn("text-sm font-medium", className)} {...props} />;
}
function FieldDescription({ className, ...props }: ComponentProps<"p">) {
  return <p className={cn("text-2xs text-muted-foreground", className)} {...props} />;
}
export { Field, FieldDescription, FieldGroup, FieldLabel };
