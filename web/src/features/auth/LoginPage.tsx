import { useMutation } from "@tanstack/react-query";
import { useState } from "react";
import type { FormEvent } from "react";

import { ApiError } from "@/api/errors";
import { Button } from "@/components/ui/button";
import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from "@/components/ui/card";
import { Input } from "@/components/ui/input";
import { useAuth } from "@/hooks/useAuth";

// Switch on the stable code, never the human-readable message (api-contract.md).
function messageFor(error: Error): string {
  if (!(error instanceof ApiError)) return "Could not reach the server.";
  switch (error.error.code) {
    case "UNAUTHORIZED":
      return "Invalid email or password";
    case "VALIDATION_ERROR":
      return "Enter your email address and password.";
    default:
      return "Something went wrong. Please try again.";
  }
}

export function LoginPage() {
  const { login } = useAuth();
  const [email, setEmail] = useState("");
  const [password, setPassword] = useState("");

  const submit = useMutation({
    mutationFn: () => login(email, password),
  });

  function onSubmit(event: FormEvent) {
    event.preventDefault();
    submit.mutate();
  }

  return (
    <main className="flex min-h-svh items-center justify-center p-8">
      <Card className="w-full max-w-sm">
        <CardHeader>
          <CardTitle>Sign in to Nabu</CardTitle>
          <CardDescription>Task tracking for software teams</CardDescription>
        </CardHeader>
        <CardContent>
          <form onSubmit={onSubmit} className="space-y-4">
            <div className="space-y-2">
              <label htmlFor="email" className="text-sm font-medium">
                Email
              </label>
              <Input
                id="email"
                type="email"
                autoComplete="username"
                value={email}
                onChange={(event) => setEmail(event.target.value)}
                required
              />
            </div>
            <div className="space-y-2">
              <label htmlFor="password" className="text-sm font-medium">
                Password
              </label>
              <Input
                id="password"
                type="password"
                autoComplete="current-password"
                value={password}
                onChange={(event) => setPassword(event.target.value)}
                required
              />
            </div>
            {submit.isError && (
              <p role="alert" className="text-destructive text-sm">
                {messageFor(submit.error)}
              </p>
            )}
            <Button
              type="submit"
              className="w-full"
              disabled={submit.isPending}
            >
              Sign in
            </Button>
          </form>
        </CardContent>
      </Card>
    </main>
  );
}
