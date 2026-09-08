import type { ServerPagination } from "@/components/data-table";
import type { useCursorPagination } from "@/hooks/use-cursor-pagination";

export function pageControls(
  navigation: ReturnType<typeof useCursorPagination>,
  next: string | null | undefined,
  isFetching: boolean,
  label?: string,
): ServerPagination {
  return {
    canFirst: navigation.canFirst,
    canPrevious: navigation.canPrevious,
    canNext: Boolean(next) && next !== navigation.cursor,
    onFirst: navigation.goFirst,
    onPrevious: navigation.goPrevious,
    onNext: () => navigation.goNext(next),
    isFetching,
    label,
  };
}
