import createClient from "openapi-fetch";
import type { paths } from "@/api/generated/schema";
import { isRecord } from "@/lib/utils";

const apiBaseUrl = process.env.NEXT_PUBLIC_API_BASE_URL ?? "";

export const api = createClient<paths>({
  baseUrl: apiBaseUrl,
  credentials: "include",
});

export class ApiError extends Error {
  readonly status: number;
  readonly code: string;

  constructor(message: string, status: number, code = "request_failed") {
    super(message);
    this.name = "ApiError";
    this.status = status;
    this.code = code;
  }
}

export function getApiError(error: unknown, status = 500) {
  if (error instanceof ApiError) return error;
  if (isRecord(error) && isRecord(error.error)) {
    const message =
      typeof error.error.message === "string"
        ? error.error.message
        : "The API request failed.";
    const code =
      typeof error.error.code === "string"
        ? error.error.code
        : "request_failed";
    return new ApiError(message, status, code);
  }
  return new ApiError(
    error instanceof Error ? error.message : "The API request failed.",
    status,
  );
}

export async function unwrap<T>(result: {
  data?: T;
  error?: unknown;
  response: Response;
}) {
  if (!result.response.ok)
    throw getApiError(result.error, result.response.status);
  return result.data as T | undefined;
}

export async function uploadMultipart<T>(
  path: string,
  formData: FormData,
  method = "POST",
) {
  const response = await fetch(`${apiBaseUrl}${path}`, {
    method,
    body: formData,
    credentials: "include",
  });
  const contentType = response.headers.get("content-type") ?? "";
  const raw = await response.text();
  let body: unknown;
  if (raw && contentType.includes("json")) {
    try {
      body = JSON.parse(raw) as unknown;
    } catch {
      body = undefined;
    }
  }
  if (!response.ok) throw getApiError(body, response.status);
  return body as T | undefined;
}

export function apiUrl(path: string) {
  return `${apiBaseUrl}${path}`;
}
