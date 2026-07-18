import type { components } from "./schema";

type ErrorDetail = components["schemas"]["ErrorDetail"];

/**
 * Carries the API's error envelope so callers can switch on `error.code`.
 * `message` is human-readable and its wording may change — never parse it
 * (api-contract.md).
 */
export class ApiError extends Error {
  readonly error: ErrorDetail;

  constructor(error: ErrorDetail) {
    super(error.message);
    this.name = "ApiError";
    this.error = error;
  }
}

function isErrorEnvelope(body: unknown): body is { error: ErrorDetail } {
  if (typeof body !== "object" || body === null || !("error" in body)) {
    return false;
  }
  const detail: unknown = (body as { error: unknown }).error;
  return (
    typeof detail === "object" &&
    detail !== null &&
    typeof (detail as ErrorDetail).code === "string" &&
    typeof (detail as ErrorDetail).message === "string"
  );
}

/**
 * openapi-fetch types the error branch as our envelope but only *attempts* a
 * JSON.parse, handing back whatever it got — a proxy's HTML 502 page arrives as
 * a bare string. Synthesizing INTERNAL keeps every call site on the
 * switch-on-code contract (api-contract.md) instead of re-checking the shape.
 */
export function toApiError(body: unknown): ApiError {
  if (isErrorEnvelope(body)) return new ApiError(body.error);
  return new ApiError({
    code: "INTERNAL",
    message: "The server returned an unreadable response.",
  });
}
