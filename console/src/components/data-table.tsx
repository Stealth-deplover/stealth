"use client";

import { flexRender, getCoreRowModel, getPaginationRowModel, getSortedRowModel, useReactTable, type ColumnDef, type PaginationState, type SortingState } from "@tanstack/react-table";
import { ArrowDown, ArrowUp, ChevronsUpDown } from "lucide-react";
import { useState } from "react";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { Skeleton } from "@/components/ui/skeleton";

export function DataTable<T>({ columns, data, loading, empty = "No records yet." }: { columns: ColumnDef<T, unknown>[]; data: T[]; loading?: boolean; empty?: string }) {
  const [sorting, setSorting] = useState<SortingState>([]);
  const [pagination, setPagination] = useState<PaginationState>({ pageIndex: 0, pageSize: 25 });
  // TanStack Table exposes an intentionally imperative table instance; React Compiler cannot memoize it safely.
  // eslint-disable-next-line react-hooks/incompatible-library
  const table = useReactTable({ data, columns, state: { sorting, pagination }, onSortingChange: setSorting, onPaginationChange: setPagination, getCoreRowModel: getCoreRowModel(), getSortedRowModel: getSortedRowModel(), getPaginationRowModel: getPaginationRowModel() });
  return <div>
    <Table>
    <TableHeader><TableRow>{table.getHeaderGroups().map((group) => group.headers.map((header) => { const sorted = header.column.getIsSorted(); const label = <span className="inline-flex items-center gap-1">{flexRender(header.column.columnDef.header, header.getContext())}{header.column.getCanSort() ? sorted === "asc" ? <ArrowUp className="size-3" /> : sorted === "desc" ? <ArrowDown className="size-3" /> : <ChevronsUpDown className="size-3 opacity-50" /> : null}</span>; return <TableHead key={header.id} aria-sort={sorted === "asc" ? "ascending" : sorted === "desc" ? "descending" : "none"}>{header.isPlaceholder ? null : header.column.getCanSort() ? <button type="button" className="rounded-sm text-left hover:text-slate-200 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-cyan-300/40" onClick={header.column.getToggleSortingHandler()}>{label}</button> : label}</TableHead>; }))}</TableRow></TableHeader>
    <TableBody>
      {loading ? Array.from({ length: 5 }, (_, index) => <TableRow key={index}>{columns.map((_, cell) => <TableCell key={cell}><Skeleton className="h-4 w-3/4" /></TableCell>)}</TableRow>) : table.getRowModel().rows.length ? table.getRowModel().rows.map((row) => <TableRow key={row.id}>{row.getVisibleCells().map((cell) => <TableCell key={cell.id}>{flexRender(cell.column.columnDef.cell, cell.getContext())}</TableCell>)}</TableRow>) : <TableRow><TableCell colSpan={columns.length} className="py-14 text-center text-sm text-stealth-muted">{empty}</TableCell></TableRow>}
    </TableBody>
    </Table>
    {!loading && table.getPageCount() > 1 ? <div className="flex flex-wrap items-center justify-between gap-3 border-t border-stealth-border px-4 py-3 text-xs text-slate-500"><span>Showing {pagination.pageIndex * pagination.pageSize + 1}–{Math.min((pagination.pageIndex + 1) * pagination.pageSize, data.length)} of {data.length}</span><div className="flex items-center gap-2"><button type="button" className="rounded-md border border-stealth-border px-2.5 py-1.5 transition hover:bg-white/[0.05] disabled:cursor-not-allowed disabled:opacity-40" disabled={!table.getCanPreviousPage()} onClick={() => table.previousPage()}>Previous</button><span>Page {pagination.pageIndex + 1} / {table.getPageCount()}</span><button type="button" className="rounded-md border border-stealth-border px-2.5 py-1.5 transition hover:bg-white/[0.05] disabled:cursor-not-allowed disabled:opacity-40" disabled={!table.getCanNextPage()} onClick={() => table.nextPage()}>Next</button></div></div> : null}
  </div>;
}
