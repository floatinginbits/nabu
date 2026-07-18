import { Button } from "@/components/ui/button";
import { LoginPage } from "@/features/auth/LoginPage";
import { TasksPage } from "@/features/tasks/TasksPage";
import { AuthProvider } from "@/hooks/AuthProvider";
import { useAuth } from "@/hooks/useAuth";

function AppShell() {
  const { user, isLoading, logout } = useAuth();

  if (isLoading) {
    return (
      <main className="flex min-h-svh items-center justify-center">
        <p role="status" className="text-muted-foreground">
          Loading…
        </p>
      </main>
    );
  }

  if (user === null) return <LoginPage />;

  return (
    <div className="min-h-svh">
      <header className="flex items-center justify-between border-b px-8 py-3">
        <span className="text-sm font-medium">{user.displayName}</span>
        <Button variant="outline" size="sm" onClick={() => void logout()}>
          Log out
        </Button>
      </header>
      <TasksPage />
    </div>
  );
}

// No router in M2: authenticated vs not is the whole navigation surface.
function App() {
  return (
    <AuthProvider>
      <AppShell />
    </AuthProvider>
  );
}

export default App;
