import { describe, expect, it } from "vitest";
import { ApiError, getApiError, unwrap } from "@/api/client";

describe("API error normalization", () => {
  it("preserves the backend error envelope", () => {
    const error = getApiError({ error: { code: "conflict", message: "Project already exists." } }, 409);

    expect(error).toBeInstanceOf(ApiError);
    expect(error.status).toBe(409);
    expect(error.code).toBe("conflict");
    expect(error.message).toBe("Project already exists.");
  });

  it("turns a non-2xx OpenAPI response into an ApiError", async () => {
    const result = {
      response: new Response(JSON.stringify({ error: { code: "validation_error", message: "Invalid slug." } }), { status: 422 }),
      error: { error: { code: "validation_error", message: "Invalid slug." } },
    };

    await expect(unwrap(result)).rejects.toMatchObject({ name: "ApiError", status: 422, code: "validation_error", message: "Invalid slug." });
  });

  it("uses a safe fallback when the backend does not return an envelope", () => {
    const error = getApiError(undefined, 500);

    expect(error).toMatchObject({ name: "ApiError", status: 500, code: "request_failed" });
  });
});
