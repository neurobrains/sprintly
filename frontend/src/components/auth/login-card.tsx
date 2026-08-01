"use client";

import * as React from "react";
import { useRouter, useSearchParams } from "next/navigation";
import { AlertCircle, CheckCircle2, MailCheck } from "lucide-react";
import { toast } from "sonner";

import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { supabase } from "@/lib/supabase/client";
import { SprintlyMark } from "@/components/brand";

const VALUE_PROPS = [
  "Boards, docs, chat and time tracking in one workspace",
  "Real-time updates — no refreshing to see what changed",
  "Self-host it, own your data",
];

const MIN_PASSWORD = 8;

type Mode = "signin" | "signup";

export function LoginCard() {
  const router = useRouter();
  const params = useSearchParams();

  const [mode, setMode] = React.useState<Mode>("signin");
  const [name, setName] = React.useState("");
  const [email, setEmail] = React.useState("");
  const [password, setPassword] = React.useState("");
  const [loading, setLoading] = React.useState(false);
  const [error, setError] = React.useState<string | null>(null);
  const [checkInbox, setCheckInbox] = React.useState(false);

  const next = params.get("next") ?? "/app";
  // Bounced back from the auth callback — an expired confirmation link.
  const callbackError = params.get("error");

  function switchMode(to: Mode) {
    setMode(to);
    setError(null);
    setPassword("");
  }

  async function onSubmit(event: React.FormEvent) {
    event.preventDefault();
    setError(null);

    const trimmedEmail = email.trim();
    const trimmedName = name.trim();

    if (!trimmedEmail || !password) {
      setError("Email and password are required.");
      return;
    }
    if (mode === "signup") {
      if (!trimmedName) {
        setError("What should we call you?");
        return;
      }
      if (password.length < MIN_PASSWORD) {
        setError(`Password must be at least ${MIN_PASSWORD} characters.`);
        return;
      }
    }

    setLoading(true);
    try {
      const client = supabase();

      if (mode === "signup") {
        const { data, error: signUpError } = await client.auth.signUp({
          email: trimmedEmail,
          password,
          options: {
            // Lands in raw_user_meta_data, which the handle_new_auth_user
            // trigger copies into profiles.full_name.
            data: { full_name: trimmedName },
            emailRedirectTo: new URL(
              `/auth/callback?next=${encodeURIComponent(next)}`,
              window.location.origin,
            ).toString(),
          },
        });
        if (signUpError) throw signUpError;

        // With email confirmation enabled, signUp returns a user but no
        // session — there is nothing to redirect to until they click the link.
        if (!data.session) {
          setCheckInbox(true);
          setLoading(false);
          return;
        }
      } else {
        const { error: signInError } = await client.auth.signInWithPassword({
          email: trimmedEmail,
          password,
        });
        if (signInError) throw signInError;
      }

      toast.success(mode === "signup" ? "Welcome to Sprintly" : "Welcome back");
      router.replace(next);
      // Re-runs middleware so the new session cookie is seen before the
      // destination renders.
      router.refresh();
    } catch (err) {
      setError(friendlyAuthError(err));
      setLoading(false);
    }
  }

  if (checkInbox) {
    return (
      <div className="w-full max-w-md animate-fade-in">
        <div className="rounded-2xl border bg-card/80 p-8 text-center shadow-xl backdrop-blur-xl">
          <MailCheck className="mx-auto h-11 w-11 text-primary" />
          <h1 className="mt-5 text-2xl font-semibold tracking-tight">Confirm your email</h1>
          <p className="mt-2 text-sm text-muted-foreground">
            We sent a link to <span className="font-medium text-foreground">{email.trim()}</span>.
            Open it to finish creating your account.
          </p>
          <Button
            variant="outline"
            className="mt-7 w-full"
            onClick={() => {
              setCheckInbox(false);
              switchMode("signin");
            }}
          >
            Back to sign in
          </Button>
        </div>
      </div>
    );
  }

  const isSignUp = mode === "signup";

  return (
    <div className="w-full max-w-md animate-fade-in">
      <div className="rounded-2xl border bg-card/80 p-8 shadow-xl backdrop-blur-xl">
        <div className="flex flex-col items-center text-center">
          <SprintlyMark className="h-11 w-11" />
          <h1 className="mt-5 text-2xl font-semibold tracking-tight">
            {isSignUp ? "Create your account" : "Welcome back"}
          </h1>
          <p className="mt-2 text-sm text-muted-foreground">
            {isSignUp
              ? "Then create a workspace or join your team."
              : "Sign in to pick up where you left off."}
          </p>
        </div>

        {error || callbackError ? (
          <div
            role="alert"
            className="mt-6 flex items-start gap-2 rounded-lg border border-destructive/30 bg-destructive/10 p-3 text-sm text-destructive"
          >
            <AlertCircle className="mt-0.5 h-4 w-4 shrink-0" />
            <span>{error ?? decodeURIComponent(callbackError!)}</span>
          </div>
        ) : null}

        <form onSubmit={onSubmit} className="mt-7 space-y-4">
          {isSignUp ? (
            <div className="space-y-1.5">
              <label htmlFor="name" className="text-sm font-medium">
                Full name
              </label>
              <Input
                id="name"
                autoComplete="name"
                placeholder="Ada Lovelace"
                value={name}
                onChange={(e) => setName(e.target.value)}
                disabled={loading}
                required
              />
            </div>
          ) : null}

          <div className="space-y-1.5">
            <label htmlFor="email" className="text-sm font-medium">
              Email
            </label>
            <Input
              id="email"
              type="email"
              autoComplete="email"
              placeholder="you@company.com"
              value={email}
              onChange={(e) => setEmail(e.target.value)}
              disabled={loading}
              required
            />
          </div>

          <div className="space-y-1.5">
            <label htmlFor="password" className="text-sm font-medium">
              Password
            </label>
            <Input
              id="password"
              type="password"
              autoComplete={isSignUp ? "new-password" : "current-password"}
              placeholder={isSignUp ? `At least ${MIN_PASSWORD} characters` : "••••••••"}
              value={password}
              onChange={(e) => setPassword(e.target.value)}
              disabled={loading}
              minLength={isSignUp ? MIN_PASSWORD : undefined}
              required
            />
          </div>

          <Button type="submit" size="lg" loading={loading} className="w-full">
            {isSignUp ? "Create account" : "Sign in"}
          </Button>
        </form>

        <p className="mt-6 text-center text-sm text-muted-foreground">
          {isSignUp ? "Already have an account?" : "New to Sprintly?"}{" "}
          <button
            type="button"
            onClick={() => switchMode(isSignUp ? "signin" : "signup")}
            disabled={loading}
            className="font-medium text-primary underline-offset-4 hover:underline disabled:opacity-50"
          >
            {isSignUp ? "Sign in" : "Create an account"}
          </button>
        </p>

        <ul className="mt-8 space-y-2.5 border-t pt-6">
          {VALUE_PROPS.map((item) => (
            <li key={item} className="flex items-start gap-2.5 text-sm text-muted-foreground">
              <CheckCircle2 className="mt-0.5 h-4 w-4 shrink-0 text-primary" />
              <span>{item}</span>
            </li>
          ))}
        </ul>
      </div>
    </div>
  );
}

/**
 * Supabase's auth messages are mostly fine as-is, but the common ones are worth
 * rewriting: "Invalid login credentials" is deliberately vague about which
 * field was wrong, and the signup collision is phrased for an API consumer
 * rather than a person.
 */
function friendlyAuthError(err: unknown): string {
  const message = err instanceof Error ? err.message : "";

  if (/invalid login credentials/i.test(message)) {
    return "That email and password don't match. Check both and try again.";
  }
  if (/already registered|already been registered|user already/i.test(message)) {
    return "An account with that email already exists — sign in instead.";
  }
  if (/email not confirmed/i.test(message)) {
    return "Confirm your email first — check your inbox for the link.";
  }
  if (/password should be at least/i.test(message)) {
    return `Password must be at least ${MIN_PASSWORD} characters.`;
  }
  if (/rate limit|too many requests|security purposes/i.test(message)) {
    return "Too many attempts. Wait a minute and try again.";
  }
  if (/signups not allowed|signup is disabled/i.test(message)) {
    return "New sign-ups are disabled for this project.";
  }
  return message || "Something went wrong. Please try again.";
}
