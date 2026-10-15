import * as React from "react";

import { cn } from "../lib/utils";

function Table({ className, ...props }: React.ComponentProps<"table">) {
  return (
    <div
      data-slot="table-container"
      className="relative w-full min-w-0 overflow-x-auto"
    >
      <table
        data-slot="table"
        className={cn("w-full caption-bottom text-sm", className)}
        {...props}
      />
    </div>
  );
}

/**
 * 这一行是不是坐在表头里。
 *
 * 悬停 / 展开 / 选中的反馈是给「可以停在上面、可以选中」的表体行的；表头行两样都
 * 不是。判断放在行自己身上，而不是让每个调用点去抵消：要抵消就得写「悬停时仍是
 * 静止底色」，那正是 lint 禁掉的写法（docs/design.md §9）。
 */
const TableHeaderRowContext = React.createContext(false);

function TableHeader({ className, ...props }: React.ComponentProps<"thead">) {
  return (
    <TableHeaderRowContext.Provider value>
      <thead data-slot="table-header" className={cn(className)} {...props} />
    </TableHeaderRowContext.Provider>
  );
}

function TableBody({ className, ...props }: React.ComponentProps<"tbody">) {
  return <tbody data-slot="table-body" className={cn(className)} {...props} />;
}

function TableFooter({ className, ...props }: React.ComponentProps<"tfoot">) {
  return (
    <tfoot
      data-slot="table-footer"
      className={cn("bg-muted/50 font-medium", className)}
      {...props}
    />
  );
}

function TableRow({ className, ...props }: React.ComponentProps<"tr">) {
  const inHeader = React.useContext(TableHeaderRowContext);
  return (
    <tr
      data-slot="table-row"
      className={cn(
        "transition-colors",
        !inHeader &&
          "hover:bg-accent/60 hover:on-accent has-aria-expanded:bg-accent/60 has-aria-expanded:on-accent data-[state=selected]:bg-accent",
        className,
      )}
      {...props}
    />
  );
}

function TableHead({ className, ...props }: React.ComponentProps<"th">) {
  return (
    <th
      data-slot="table-head"
      className={cn(
        "h-10 px-2 text-left align-middle font-medium whitespace-nowrap text-foreground [&:has([role=checkbox])]:pr-0 [&>[role=checkbox]]:translate-y-[2px]",
        className,
      )}
      {...props}
    />
  );
}

function TableCell({ className, ...props }: React.ComponentProps<"td">) {
  return (
    <td
      data-slot="table-cell"
      data-selectable-text="true"
      className={cn(
        "p-2 align-middle whitespace-nowrap [&:has([role=checkbox])]:pr-0 [&>[role=checkbox]]:translate-y-[2px]",
        className,
      )}
      {...props}
    />
  );
}

function TableCaption({
  className,
  ...props
}: React.ComponentProps<"caption">) {
  return (
    <caption
      data-slot="table-caption"
      className={cn("mt-4 text-sm text-muted-foreground", className)}
      {...props}
    />
  );
}

export {
  Table,
  TableHeader,
  TableBody,
  TableFooter,
  TableHead,
  TableRow,
  TableCell,
  TableCaption,
};
