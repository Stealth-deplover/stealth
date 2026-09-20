import { fireEvent, render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import { AdminLogTailStatus, VirtualizedLogTable } from "./admin-logs-view";

vi.mock("@tanstack/react-virtual", () => ({
  useVirtualizer: ({ count }: { count: number }) => ({
    getTotalSize: () => count * 72,
    getVirtualItems: () =>
      Array.from({ length: count }, (_, index) => ({
        index,
        key: `row-${index}`,
        start: index * 72,
      })),
  }),
}));

describe("virtualized Admin log table", () => {
  it("keeps table cells semantic and provides an explicit detail action", () => {
    render(
      <VirtualizedLogTable
        items={[
          {
            timestamp: "2026-09-20T00:00:00.000Z",
            service: "api",
            level: "error",
            message: "request failed",
            trace_id: "trace-123",
          },
        ]}
      />,
    );

    expect(
      screen.getByRole("table", { name: "Structured logs" }),
    ).toBeInTheDocument();
    expect(
      screen.getByRole("columnheader", { name: "Detail" }),
    ).toBeInTheDocument();

    const open = screen.getByRole("button", { name: "Open log from api" });
    fireEvent.click(open);

    expect(
      screen.getByRole("dialog", { name: "Log detail" }),
    ).toBeInTheDocument();
    expect(
      screen.getByRole("button", { name: "Close dialog" }),
    ).toBeInTheDocument();
  });

  it("announces live-tail connection changes", () => {
    render(
      <AdminLogTailStatus
        connected={false}
        error="Live log stream disconnected. The browser will retry."
      />,
    );

    expect(screen.getByRole("status")).toHaveAttribute("aria-live", "polite");
    expect(screen.getByRole("status")).toHaveTextContent(
      "Connecting to live stream",
    );
    expect(screen.getByRole("status")).toHaveTextContent(
      "Live log stream disconnected",
    );
  });
});
