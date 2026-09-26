import { render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import { HttpStatusBadge, StatusBadge } from "@/components/ui/badge";

describe("StatusBadge", () => {
  it("keeps pending status text visible and disables its pulse for reduced motion", () => {
    render(<StatusBadge status="pending" />);

    const indicator = screen
      .getByText("Pending")
      .parentElement?.querySelector('[aria-hidden="true"]');
    expect(indicator).toHaveClass(
      "animate-pulse",
      "motion-reduce:animate-none",
    );
  });
});

describe("HttpStatusBadge", () => {
  it("keeps the HTTP code visible while applying semantic status colors", () => {
    render(
      <>
        <HttpStatusBadge status={200} />
        <HttpStatusBadge status={404} />
        <HttpStatusBadge status={500} />
      </>,
    );

    expect(screen.getByText("200")).toHaveClass("text-emerald-200");
    expect(screen.getByText("404")).toHaveClass("text-amber-200");
    expect(screen.getByText("500")).toHaveClass("text-rose-200");
  });
});
