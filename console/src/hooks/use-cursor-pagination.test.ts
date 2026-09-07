import { act, renderHook } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { useCursorPagination } from "@/hooks/use-cursor-pagination";

const navigation = vi.hoisted(() => ({
  pathname: "/functions",
  search: new URLSearchParams(),
  replace: vi.fn(),
}));

vi.mock("next/navigation", () => ({
  usePathname: () => navigation.pathname,
  useRouter: () => ({ replace: navigation.replace }),
  useSearchParams: () => navigation.search,
}));

describe("useCursorPagination", () => {
  beforeEach(() => {
    navigation.pathname = "/functions";
    navigation.search = new URLSearchParams();
    navigation.replace.mockReset();
  });

  it("navigates next and previous using known cursor history", () => {
    const { result, rerender } = renderHook(() => useCursorPagination());

    expect(result.current.canFirst).toBe(false);
    expect(result.current.canPrevious).toBe(false);

    act(() => result.current.goNext("page-two"));
    expect(navigation.replace).toHaveBeenLastCalledWith(
      "/functions?cursor=page-two",
      { scroll: false },
    );

    navigation.search = new URLSearchParams("cursor=page-two");
    rerender();
    expect(result.current.canFirst).toBe(true);
    expect(result.current.canPrevious).toBe(true);

    act(() => result.current.goPrevious());
    expect(navigation.replace).toHaveBeenLastCalledWith("/functions", {
      scroll: false,
    });
  });

  it("does not invent a previous cursor for a deep link and supports First", () => {
    navigation.search = new URLSearchParams("status=active&cursor=page-two");
    const { result } = renderHook(() => useCursorPagination());

    expect(result.current.canPrevious).toBe(false);
    expect(result.current.canFirst).toBe(true);

    act(() => result.current.goPrevious());
    expect(navigation.replace).not.toHaveBeenCalled();

    act(() => result.current.goFirst());
    expect(navigation.replace).toHaveBeenCalledWith(
      "/functions?status=active",
      { scroll: false },
    );
  });

  it("resets previous history when the filter context changes", () => {
    const { result, rerender } = renderHook(() => useCursorPagination());

    act(() => result.current.goNext("page-two"));
    navigation.search = new URLSearchParams("cursor=page-two");
    rerender();
    expect(result.current.canPrevious).toBe(true);

    navigation.search = new URLSearchParams("status=active&cursor=page-two");
    rerender();
    expect(result.current.canPrevious).toBe(false);

    navigation.search = new URLSearchParams("status=disabled&cursor=page-two");
    rerender();
    expect(result.current.canPrevious).toBe(false);

    act(() => result.current.goPrevious());
    expect(navigation.replace).toHaveBeenCalledOnce();
  });
});
