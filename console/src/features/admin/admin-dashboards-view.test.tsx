import { fireEvent, render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import { TelemetryPanelError } from "./admin-dashboards-view";

describe("TelemetryPanelError", () => {
  it("announces the panel failure and provides recovery", () => {
    const retry = vi.fn();
    render(
      <TelemetryPanelError message="Metric data unavailable." retry={retry} />,
    );

    expect(screen.getByRole("alert")).toHaveTextContent(
      "Metric data unavailable.",
    );
    fireEvent.click(screen.getByRole("button", { name: "Retry" }));
    expect(retry).toHaveBeenCalledOnce();
  });
});
