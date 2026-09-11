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
  verify: {
    error: null as unknown,
    isPending: false,
    mutateAsync: vi.fn(),
    reset: vi.fn(),
  },
  start: {
    error: null as unknown,
    isPending: false,
    mutateAsync: vi.fn(),
    reset: vi.fn(),
  },
  poll: {
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
  useVerifyBootstrapCode: () => mocks.verify,
  useStartGitHubDeviceFlow: () => mocks.start,
  usePollGitHubDeviceFlow: () => mocks.poll,
}));

const setupCode = "STEALTH-ABCD-2345-EFGH";

describe("first-run GitHub setup view", () => {
  beforeEach(() => {
    mocks.status.data = { setup_required: true };
    mocks.status.error = null;
    mocks.status.isPending = false;
    mocks.status.refetch.mockReset();
    mocks.verify.error = null;
    mocks.verify.isPending = false;
    mocks.verify.mutateAsync.mockReset().mockResolvedValue({
      authorization_session_id: "019c0000-0000-7000-8000-000000000001",
      expires_at: new Date(Date.now() + 900_000).toISOString(),
    });
    mocks.verify.reset.mockReset();
    mocks.start.error = null;
    mocks.start.isPending = false;
    mocks.start.mutateAsync.mockReset().mockResolvedValue({
      authorization_session_id: "019c0000-0000-7000-8000-000000000001",
      user_code: "WDJB-MJHT",
      verification_uri: "https://github.com/login/device",
      expires_at: new Date(Date.now() + 900_000).toISOString(),
      interval_seconds: 5,
    });
    mocks.start.reset.mockReset();
    mocks.poll.error = null;
    mocks.poll.isPending = false;
    mocks.poll.mutateAsync.mockReset().mockResolvedValue({
      status: "pending",
      retry_after_seconds: 5,
    });
    mocks.replace.mockReset();
  });

  it("shows only the setup-code gate before GitHub verification", () => {
    render(<SetupView />);

    expect(screen.getByRole("textbox", { name: "Setup code" })).toBeVisible();
    expect(
      screen.getByRole("button", { name: "Verify setup code" }),
    ).toBeVisible();
    expect(screen.queryByRole("textbox", { name: "Email" })).toBeNull();
    expect(screen.queryByLabelText("Password")).toBeNull();
    expect(
      screen.queryByRole("button", { name: "Continue with GitHub" }),
    ).toBeNull();
  });

  it("refuses onboarding after the backend seals bootstrap", () => {
    mocks.status.data = { setup_required: false };
    render(<SetupView />);

    expect(screen.getByText("Instance setup is complete")).toBeVisible();
    expect(screen.queryByRole("textbox", { name: "Setup code" })).toBeNull();
  });

  it("validates the setup code before calling the API", async () => {
    render(<SetupView />);

    fireEvent.change(screen.getByRole("textbox", { name: "Setup code" }), {
      target: { value: "wrong" },
    });
    fireEvent.click(screen.getByRole("button", { name: "Verify setup code" }));

    await waitFor(() =>
      expect(screen.getByText(/Enter the setup code shown/i)).toBeVisible(),
    );
    expect(mocks.verify.mutateAsync).not.toHaveBeenCalled();
  });

  it("verifies the local code before enabling Continue with GitHub", async () => {
    render(<SetupView />);

    fireEvent.change(screen.getByRole("textbox", { name: "Setup code" }), {
      target: { value: setupCode.toLowerCase() },
    });
    fireEvent.click(screen.getByRole("button", { name: "Verify setup code" }));

    await waitFor(() =>
      expect(mocks.verify.mutateAsync).toHaveBeenCalledWith({
        setup_code: setupCode,
      }),
    );
    expect(
      screen.getByRole("button", { name: "Continue with GitHub" }),
    ).toBeVisible();
    expect(screen.queryByRole("textbox", { name: "Email" })).toBeNull();
  });

  it("starts server-owned Device Flow and renders a copyable GitHub code", async () => {
    render(<SetupView />);
    fireEvent.change(screen.getByRole("textbox", { name: "Setup code" }), {
      target: { value: setupCode },
    });
    fireEvent.click(screen.getByRole("button", { name: "Verify setup code" }));
    await screen.findByRole("button", { name: "Continue with GitHub" });

    fireEvent.click(
      screen.getByRole("button", { name: "Continue with GitHub" }),
    );
    await waitFor(() =>
      expect(mocks.start.mutateAsync).toHaveBeenCalledWith({
        authorization_session_id: "019c0000-0000-7000-8000-000000000001",
        setup_code: setupCode,
      }),
    );
    expect(screen.getByText("WDJB-MJHT")).toBeVisible();
    expect(screen.getByRole("link", { name: /Open GitHub/i })).toHaveAttribute(
      "href",
      "https://github.com/login/device",
    );
    expect(screen.queryByText("device-code-test-value")).toBeNull();
  });

  it("keeps provider errors generic and accessible", () => {
    mocks.verify.error = new ApiError(
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
