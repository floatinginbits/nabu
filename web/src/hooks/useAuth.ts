import { createContext, use } from "react";

import type { components } from "@/api/schema";

type UserProfile = components["schemas"]["UserProfile"];

export interface AuthContextValue {
  /** null once we know there is no session; undefined never escapes isLoading. */
  user: UserProfile | null;
  isLoading: boolean;
  /**
   * Set only when the session probe failed for a reason other than a 401 —
   * "we don't know" rather than "you're signed out", which must not render as
   * the login page.
   */
  sessionError: Error | null;
  isRetryingSession: boolean;
  retrySession: () => void;
  login: (email: string, password: string) => Promise<void>;
  /**
   * Fire-and-forget: local teardown always runs, so failure is reported via
   * `logoutFailed` rather than a rejected promise no caller could act on.
   */
  logout: () => void;
  logoutFailed: boolean;
}

// Split from AuthProvider.tsx because react-refresh requires a component file
// to export only components.
export const AuthContext = createContext<AuthContextValue | null>(null);

export function useAuth(): AuthContextValue {
  const value = use(AuthContext);
  if (value === null) throw new Error("useAuth requires an AuthProvider");
  return value;
}
