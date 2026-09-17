import createClient from "openapi-fetch";
import type { QueryFunctionContext } from "@tanstack/react-query";
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

export type ApiResult<T = unknown> = {
  data?: T;
  error?: unknown;
  response: Response;
};

type QueryData<TResult extends ApiResult> = TResult extends { data?: infer T }
  ? T
  : never;

/**
 * Adapts an OpenAPI operation to TanStack Query while preserving cancellation
 * and the shared API error envelope.
 */
export function cancellableQuery<TResult extends ApiResult>(
  operation: (signal: AbortSignal) => Promise<TResult>,
): (
  context: Pick<QueryFunctionContext, "signal"> &
    Partial<Omit<QueryFunctionContext, "signal">>,
) => Promise<QueryData<TResult> | undefined> {
  return async ({ signal }) =>
    unwrap<QueryData<TResult>>(
      (await operation(signal)) as {
        data?: QueryData<TResult>;
        error?: unknown;
        response: Response;
      },
    );
}

export async function execute<T>(operation: Promise<ApiResult<T>>) {
  return unwrap(await operation);
}

type MultipartOperation<T> = (
  formData: FormData,
  signal?: AbortSignal,
) => Promise<ApiResult<T>>;

/**
 * Adapts a typed OpenAPI multipart operation to the shared response/error
 * normalizer. The operation owns the generated path and request schema; this
 * helper deliberately does not accept arbitrary URLs or HTTP methods.
 */
export async function uploadMultipart<T>(
  operation: MultipartOperation<T>,
  formData: FormData,
  signal?: AbortSignal,
) {
  return unwrap(await operation(formData, signal));
}

export function apiUrl(path: string) {
  return `${apiBaseUrl}${path}`;
}
