"use client";

import type { ReactNode } from "react";
import { Search } from "lucide-react";
import { Input } from "@/components/ui/input";
import { Card } from "@/components/ui/card";
import { useMemo } from "react";
import { usePathname, useRouter, useSearchParams } from "next/navigation";

export function ResourceTableCard<T extends object>({ data, searchable, searchPlaceholder = "Search resources", children }: { data: T[]; searchable?: (item: T, term: string) => boolean; searchPlaceholder?: string; children: (filtered: T[]) => ReactNode }) {
  const router = useRouter();
  const pathname = usePathname();
  const searchParams = useSearchParams();
  const querySearch = searchParams.get("search") ?? "";
  const search = querySearch;
  const updateSearch = (value: string) => {
    const next = new URLSearchParams(searchParams.toString());
    if (value) next.set("search", value); else next.delete("search");
    next.delete("cursor");
    router.replace(`${pathname}${next.toString() ? `?${next.toString()}` : ""}`, { scroll: false });
  };
  const filtered = useMemo(() => searchable && search ? data.filter((item) => searchable(item, search.toLowerCase())) : data, [data, search, searchable]);
  return <Card>
    {searchable ? <div className="border-b border-stealth-border p-4"><div className="relative max-w-sm"><Search className="absolute left-3 top-1/2 size-4 -translate-y-1/2 text-slate-600" /><Input value={search} onChange={(event) => updateSearch(event.target.value)} placeholder={searchPlaceholder} className="pl-9" aria-label={searchPlaceholder} /></div></div> : null}
    {children(filtered)}
  </Card>;
}
