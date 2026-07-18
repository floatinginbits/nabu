import { AuthProvider } from "@/features/auth/AuthProvider";
import { AppShell } from "@/routes/AppShell";

// No router in M2: authenticated vs not is the whole navigation surface.
function App() {
  return (
    <AuthProvider>
      <AppShell />
    </AuthProvider>
  );
}

export default App;
