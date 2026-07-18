import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useCallback, useEffect, useMemo } from "react";
import type { ReactNode } from "react";

import { client, resetAuthState, setOnUnauthenticated } from "@/api/client";
import { toApiError } from "@/api/errors";
import { AuthContext } from "@/hooks/useAuth";
import type { AuthContextValue } from "@/hooks/useAuth";

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
      if (error) throw toApiError(error);
      return data;
    },
  });

  useEffect(() => {
    setOnUnauthenticated(() => queryClient.setQueryData(["me"], null));
    return () => setOnUnauthenticated(null);
  }, [queryClient]);

  const logout = useMutation({
    mutationFn: async () => {
      try {
        const { error } = await client.POST("/api/v1/auth/logout");
        if (error) throw toApiError(error);
      } finally {
        // Unconditional: the teardown is client-side and equally correct when
        // the call fails. Skipping it on a network error would leave the
        // outgoing user's cache on screen with the session apparently intact.
        resetAuthState();
        // Everything cached belongs to the outgoing user; the next one must
        // never see it. "me" is set to null rather than removed: clearing it
        // out from under its live observer leaves this component rendering the
        // stale user. Only the exact key is spared — a future ["me", ...]
        // subkey holds outgoing-user data like any other query.
        queryClient.setQueryData(["me"], null);
        queryClient.removeQueries({
          predicate: (query) =>
            !(query.queryKey.length === 1 && query.queryKey[0] === "me"),
        });
      }
    },
  });

  const { mutate: startLogout, reset: resetLogout } = logout;
  const { refetch: refetchMe } = me;

  const login = useCallback(
    async (email: string, password: string) => {
      const { data, error } = await client.POST("/api/v1/auth/login", {
        body: { email, password },
      });
      if (error) throw toApiError(error);
      // Clears the client's sticky refresh-failure latch.
      resetAuthState();
      resetLogout();
      queryClient.setQueryData(["me"], data);
    },
    [queryClient, resetLogout],
  );

  const retrySession = useCallback(() => void refetchMe(), [refetchMe]);

  const value = useMemo<AuthContextValue>(
    () => ({
      user: me.data ?? null,
      isLoading: me.isPending,
      // A non-401 failure means the session is unknown, not absent — rendering
      // the login page here would log out a user whose server blipped.
      sessionError: me.error,
      isRetryingSession: me.isFetching,
      retrySession,
      login,
      logout: startLogout,
      logoutFailed: logout.isError,
    }),
    [
      me.data,
      me.isPending,
      me.error,
      me.isFetching,
      retrySession,
      login,
      startLogout,
      logout.isError,
    ],
  );

  return <AuthContext value={value}>{children}</AuthContext>;
}
