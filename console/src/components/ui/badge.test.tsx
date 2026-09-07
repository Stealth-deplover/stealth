import { render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import { HttpStatusBadge } from "@/components/ui/badge";

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
