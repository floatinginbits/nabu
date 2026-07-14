import "@testing-library/jest-dom/vitest";
import { cleanup } from "@testing-library/react";
import { afterEach } from "vitest";

// RTL's automatic cleanup needs vitest globals, which we keep disabled.
afterEach(() => {
  cleanup();
});
