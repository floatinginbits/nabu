import { Button } from "@/components/ui/button";
import { LoginPage } from "@/features/auth/LoginPage";
import { SessionError } from "@/features/auth/SessionError";
import { TasksPage } from "@/features/tasks/TasksPage";
import { useAuth } from "@/hooks/useAuth";

export function AppShell() {
  const {
    user,
    isLoading,
    sessionError,
    isRetryingSession,
    retrySession,
    logout,
  } = useAuth();

  if (isLoading) {
    return (
      <main className="flex min-h-svh items-center justify-center">
        <p role="status" className="text-muted-foreground">
          Loading…
        </p>
      </main>
    );
  }

  // Ahead of the null check: an unreachable server leaves `user` null too, and
  // showing the login page then would sign out a user who never lost a session.
  if (sessionError !== null) {
    return (
      <SessionError onRetry={retrySession} isRetrying={isRetryingSession} />
    );
  }

  if (user === null) return <LoginPage />;

  return (
    <div className="min-h-svh">
      <header className="flex items-center justify-between border-b px-8 py-3">
        <span className="text-sm font-medium">{user.displayName}</span>
        <Button variant="outline" size="sm" onClick={logout}>
          Log out
        </Button>
      </header>
      <TasksPage />
    </div>
  );
}
