import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import { AppDeploymentDialog } from "@/features/apps/app-deployment-dialog";

describe("AppDeploymentDialog", () => {
  it("maps a source archive and desired-image option to multipart fields", async () => {
    const onSubmit = vi.fn<(form: FormData) => Promise<void>>().mockResolvedValue(undefined);
    render(
      <AppDeploymentDialog
        open
        onOpenChange={vi.fn()}
        pending={false}
        onSubmit={onSubmit}
      />,
    );

    const file = new File(["payload"], "source.tar", { type: "application/x-tar" });
    fireEvent.change(screen.getByLabelText("Source archive"), {
      target: { files: [file] },
    });
    fireEvent.change(screen.getByLabelText("Dockerfile path"), {
      target: { value: "build/Dockerfile" },
    });
    fireEvent.change(screen.getByLabelText("Context directory"), {
      target: { value: "build" },
    });
    fireEvent.change(screen.getByLabelText("Dockerfile target (optional)"), {
      target: { value: "release" },
    });
    fireEvent.click(screen.getByRole("checkbox"));
    const queueButton = screen.getByRole("button", { name: "Queue build" });
    expect(queueButton).toBeEnabled();
    const formElement = queueButton.closest("form");
    if (!formElement) throw new Error("deployment dialog form is missing");
    fireEvent.submit(formElement);

    await waitFor(() => expect(onSubmit).toHaveBeenCalledOnce());
    const form = onSubmit.mock.calls[0][0];
    expect(form.get("source")).toBe(file);
    expect(form.get("dockerfile_path")).toBe("build/Dockerfile");
    expect(form.get("context_directory")).toBe("build");
    expect(form.get("target")).toBe("release");
    expect(form.get("select")).toBe("true");
  });

  it("rejects unsafe build paths before submitting", async () => {
    const onSubmit = vi.fn<(form: FormData) => Promise<void>>().mockResolvedValue(undefined);
    render(
      <AppDeploymentDialog
        open
        onOpenChange={vi.fn()}
        pending={false}
        onSubmit={onSubmit}
      />,
    );
    const file = new File(["payload"], "source.zip");
    fireEvent.change(screen.getByLabelText("Source archive"), {
      target: { files: [file] },
    });
    fireEvent.change(screen.getByLabelText("Dockerfile path"), {
      target: { value: "../Dockerfile" },
    });
    fireEvent.click(screen.getByRole("button", { name: "Queue build" }));

    await waitFor(() => expect(onSubmit).not.toHaveBeenCalled());
  });
});
