import { Button } from "@/components/ui/button";

interface SessionErrorProps {
  onRetry: () => void;
  isRetrying: boolean;
}

export function SessionError({ onRetry, isRetrying }: SessionErrorProps) {
  return (
    <main className="flex min-h-svh flex-col items-center justify-center gap-4 p-8 text-center">
      <div className="space-y-1">
        <h1 className="text-lg font-semibold">Can’t reach Nabu</h1>
        <p role="alert" className="text-muted-foreground text-sm">
          We couldn’t check your session. You are still signed in — this is a
          connection problem, not a sign-out.
        </p>
      </div>
      <Button variant="outline" onClick={onRetry} disabled={isRetrying}>
        {isRetrying ? "Retrying…" : "Try again"}
      </Button>
    </main>
  );
}
