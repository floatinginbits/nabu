import { createContext, use } from "react";

import type { components } from "@/api/schema";

type UserProfile = components["schemas"]["UserProfile"];

export interface AuthContextValue {
  /** null once we know there is no session; undefined never escapes isLoading. */
  user: UserProfile | null;
  isLoading: boolean;
  login: (email: string, password: string) => Promise<void>;
  logout: () => Promise<void>;
}

// Split from AuthProvider.tsx because react-refresh requires a component file
// to export only components.
export const AuthContext = createContext<AuthContextValue | null>(null);

export function useAuth(): AuthContextValue {
  const value = use(AuthContext);
  if (value === null) throw new Error("useAuth requires an AuthProvider");
  return value;
}
