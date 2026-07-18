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
