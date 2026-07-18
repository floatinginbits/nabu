import { useQuery, useQueryClient } from "@tanstack/react-query";
import { useCallback, useEffect, useMemo } from "react";
import type { ReactNode } from "react";

import { client, resetAuthState, setOnUnauthenticated } from "@/api/client";
import { ApiError } from "@/api/errors";

import { AuthContext } from "./useAuth";
import type { AuthContextValue } from "./useAuth";

interface AuthProviderProps {
  children: ReactNode;
}

export function AuthProvider({ children }: AuthProviderProps) {
  const queryClient = useQueryClient();

  const me = useQuery({
    queryKey: ["me"],
    // A 401 is an answer, not a transient failure.
    retry: false,
    queryFn: async () => {
      const { data, error, response } = await client.GET("/api/v1/users/me");
      // Reached only after the client's silent refresh has also failed.
      if (response.status === 401) return null;
      if (error) throw new ApiError(error.error);
      return data;
    },
  });

  useEffect(() => {
    setOnUnauthenticated(() => queryClient.setQueryData(["me"], null));
    return () => setOnUnauthenticated(null);
  }, [queryClient]);

  const login = useCallback(
    async (email: string, password: string) => {
      const { data, error } = await client.POST("/api/v1/auth/login", {
        body: { email, password },
      });
      if (error) throw new ApiError(error.error);
      // Clears the client's sticky refresh-failure latch.
      resetAuthState();
      queryClient.setQueryData(["me"], data);
    },
    [queryClient],
  );

  const logout = useCallback(async () => {
    await client.POST("/api/v1/auth/logout");
    resetAuthState();
    // Everything cached belongs to the outgoing user; the next one must never
    // see it. "me" is set to null rather than removed: clearing it out from
    // under its live observer leaves this component rendering the stale user.
    queryClient.setQueryData(["me"], null);
    queryClient.removeQueries({
      predicate: (query) => query.queryKey[0] !== "me",
    });
  }, [queryClient]);

  const value = useMemo<AuthContextValue>(
    () => ({
      user: me.data ?? null,
      isLoading: me.isPending,
      login,
      logout,
    }),
    [me.data, me.isPending, login, logout],
  );

  return <AuthContext value={value}>{children}</AuthContext>;
}
