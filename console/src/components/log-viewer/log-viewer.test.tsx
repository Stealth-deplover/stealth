import { render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import { LogViewer } from "./log-viewer";

describe("LogViewer", () => {
  it("shows telemetry failures as errors instead of an empty stream", async () => {
    Object.defineProperty(window, "matchMedia", {
      configurable: true,
      value: vi.fn().mockReturnValue({ matches: true }),
    });
    Object.defineProperty(Element.prototype, "scrollIntoView", {
      configurable: true,
      value: vi.fn(),
    });
    const source = {
      key: "app-runtime:project-1:app-1",
      fetchPage: vi.fn(async () => {
        throw new Error("App runtime logs are temporarily unavailable.");
      }),
    };
    render(
      <LogViewer
        title="Runtime logs"
        source={source}
        emptyMessage="No retained runtime log lines were returned for this App."
      />,
    );

    expect(await screen.findByRole("alert")).toHaveTextContent(
      "App runtime logs are temporarily unavailable.",
    );
    expect(
      screen.queryByText(
        "No retained runtime log lines were returned for this App.",
      ),
    ).not.toBeInTheDocument();
  });
});
