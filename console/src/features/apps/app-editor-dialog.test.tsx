import { render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import { AppEditorDialog } from "@/features/apps/app-editor-dialog";

describe("AppEditorDialog", () => {
  it("exposes workload controls and the fixed restart policy without secret fields", () => {
    render(
      <AppEditorDialog
        open
        onOpenChange={vi.fn()}
        onSubmit={vi.fn().mockResolvedValue(undefined)}
      />,
    );

    expect(screen.getByRole("heading", { name: "Create an App" })).toBeTruthy();
    expect(screen.getByLabelText("Name")).toBeTruthy();
    expect(screen.getByLabelText("Desired state")).toBeTruthy();
    expect(screen.getByLabelText("Internal HTTP port")).toBeTruthy();
    expect(screen.getByLabelText("Command arguments")).toBeTruthy();
    expect(screen.getByLabelText("Working directory")).toBeTruthy();
    expect(screen.getByLabelText("Health protocol")).toBeTruthy();
    expect(screen.getByLabelText("CPU (millicores)")).toBeTruthy();
    expect(screen.getByLabelText("Memory (bytes)")).toBeTruthy();
    expect(screen.getByLabelText("PIDs limit")).toBeTruthy();
    expect(screen.getByLabelText("Stop grace period (seconds)")).toBeTruthy();
    expect(screen.getByLabelText("Restart policy")).toHaveProperty("readOnly", true);
    expect(screen.queryByLabelText(/secret|environment variable/i)).toBeNull();
  });
});
