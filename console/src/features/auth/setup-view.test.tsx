import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { ApiError } from "@/api/client";
import { SetupView } from "@/features/auth/setup-view";

const mocks = vi.hoisted(() => ({
  status: {
    data: { setup_required: true },
    error: null as unknown,
    isPending: false,
    refetch: vi.fn(),
  },
  mutation: {
    error: null as unknown,
    isPending: false,
    mutateAsync: vi.fn(),
  },
  replace: vi.fn(),
}));

vi.mock("next/navigation", () => ({
  useRouter: () => ({ replace: mocks.replace }),
}));

vi.mock("@/api/queries", () => ({
  useBootstrapStatus: () => mocks.status,
}));

vi.mock("@/api/mutations", () => ({
  useCreateInstanceOwner: () => mocks.mutation,
}));

describe("first-run setup view", () => {
  beforeEach(() => {
    mocks.status.data = { setup_required: true };
    mocks.status.error = null;
    mocks.status.isPending = false;
    mocks.status.refetch.mockReset();
    mocks.mutation.error = null;
    mocks.mutation.isPending = false;
    mocks.mutation.mutateAsync.mockReset().mockResolvedValue({
      account: { instance_role: "instance_owner" },
    });
    mocks.replace.mockReset();
  });

  it("shows the owner form only while bootstrap is required", () => {
    render(<SetupView />);

    expect(screen.getByRole("textbox", { name: "Setup code" })).toBeVisible();
    expect(screen.getByRole("textbox", { name: "Email" })).toBeVisible();
    expect(screen.getByLabelText("Password")).toHaveAttribute(
      "autocomplete",
      "new-password",
    );
  });

  it("refuses onboarding after the backend seals bootstrap", () => {
    mocks.status.data = { setup_required: false };
    render(<SetupView />);

    expect(screen.getByText("Instance setup is complete")).toBeVisible();
    expect(screen.queryByRole("textbox", { name: "Setup code" })).toBeNull();
  });

  it("validates code and password before calling the API", async () => {
    render(<SetupView />);

    fireEvent.change(screen.getByRole("textbox", { name: "Setup code" }), {
      target: { value: "wrong" },
    });
    fireEvent.change(screen.getByRole("textbox", { name: "Email" }), {
      target: { value: "owner@example.test" },
    });
    fireEvent.change(screen.getByLabelText("Password"), {
      target: { value: "short" },
    });
    fireEvent.click(
      screen.getByRole("button", { name: "Create Instance Owner" }),
    );

    await waitFor(() =>
      expect(screen.getByText(/Enter the setup code shown/i)).toBeVisible(),
    );
    expect(screen.getByText("Use at least 12 characters.")).toBeVisible();
    expect(mocks.mutation.mutateAsync).not.toHaveBeenCalled();
  });

  it("submits the code in the request body and enters the Console", async () => {
    render(<SetupView />);

    fireEvent.change(screen.getByRole("textbox", { name: "Setup code" }), {
      target: { value: "stealth-abcd-2345-efgh" },
    });
    fireEvent.change(screen.getByRole("textbox", { name: "Email" }), {
      target: { value: "owner@example.test" },
    });
    fireEvent.change(screen.getByLabelText("Password"), {
      target: { value: "correct-horse-battery-staple" },
    });
    fireEvent.click(
      screen.getByRole("button", { name: "Create Instance Owner" }),
    );

    await waitFor(() =>
      expect(mocks.mutation.mutateAsync).toHaveBeenCalledWith({
        setup_code: "stealth-abcd-2345-efgh",
        email: "owner@example.test",
        password: "correct-horse-battery-staple",
      }),
    );
    expect(mocks.replace).toHaveBeenCalledWith("/organizations");
  });

  it("keeps bootstrap errors generic and accessible", () => {
    mocks.mutation.error = new ApiError(
      "invalid setup code",
      401,
      "invalid_bootstrap_code",
    );
    render(<SetupView />);

    expect(screen.getByRole("alert")).toHaveTextContent(
      /invalid, expired, or already used/i,
    );
  });
});
