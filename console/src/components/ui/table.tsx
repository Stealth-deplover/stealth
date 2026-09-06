import * as React from "react";
import { cn } from "@/lib/utils";

export function Table({ className, ...props }: React.TableHTMLAttributes<HTMLTableElement>) { return <div className="scrollbar-thin overflow-x-auto"><table className={cn("w-full text-left text-sm", className)} {...props} /></div>; }
export function TableHeader({ className, ...props }: React.HTMLAttributes<HTMLTableSectionElement>) { return <thead className={cn("border-b border-stealth-border text-[11px] uppercase tracking-[0.14em] text-slate-500", className)} {...props} />; }
export function TableBody({ className, ...props }: React.HTMLAttributes<HTMLTableSectionElement>) { return <tbody className={cn("divide-y divide-stealth-border/70", className)} {...props} />; }
export function TableRow({ className, ...props }: React.HTMLAttributes<HTMLTableRowElement>) { return <tr className={cn("transition-colors hover:bg-white/[0.025]", className)} {...props} />; }
export function TableHead({ className, ...props }: React.ThHTMLAttributes<HTMLTableCellElement>) { return <th className={cn("px-4 py-3 font-medium", className)} {...props} />; }
export function TableCell({ className, ...props }: React.TdHTMLAttributes<HTMLTableCellElement>) { return <td className={cn("px-4 py-3.5 text-slate-300", className)} {...props} />; }
