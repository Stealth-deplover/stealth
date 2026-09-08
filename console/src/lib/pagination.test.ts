import { describe, expect, it, vi } from "vitest";
import { pageControls } from "@/lib/pagination";

describe("server page controls", () => {
  it("disables Next for missing or repeated server cursors", () => {
    const navigation = {
      cursor: "current-page",
      canFirst: true,
      canPrevious: false,
      goFirst: vi.fn(),
      goPrevious: vi.fn(),
      goNext: vi.fn(),
    };
    expect(pageControls(navigation, null, false).canNext).toBe(false);
    expect(pageControls(navigation, "current-page", false).canNext).toBe(false);
    const controls = pageControls(navigation, "next-page", false);
    expect(controls.canNext).toBe(true);
    controls.onNext();
    expect(navigation.goNext).toHaveBeenCalledWith("next-page");
  });
});
