"use client";

import * as React from "react";
import { useSearchParams } from "next/navigation";
import { AlertCircle, CheckCircle2 } from "lucide-react";
import { toast } from "sonner";

import { Button } from "@/components/ui/button";
import { supabase } from "@/lib/supabase/client";
import { GoogleIcon, SprintlyMark } from "@/components/brand";

const VALUE_PROPS = [
  "Boards, docs, chat and time tracking in one workspace",
  "Real-time updates — no refreshing to see what changed",
  "Self-host it, own your data",
];

export function LoginCard() {
  const params = useSearchParams();
  const [loading, setLoading] = React.useState(false);

  const next = params.get("next") ?? "/app";
  const authError = params.get("error");

  async function signInWithGoogle() {
    setLoading(true);
    try {
      const redirectTo = new URL("/auth/callback", window.location.origin);
      // Carried through the OAuth round trip so we land where the user meant to go.
      redirectTo.searchParams.set("next", next);

      const { error } = await supabase().auth.signInWithOAuth({
        provider: "google",
        options: {
          redirectTo: redirectTo.toString(),
          queryParams: {
            // Ask for a refresh token and let the user pick the account.
            access_type: "offline",
            prompt: "select_account",
          },
        },
      });

      if (error) throw error;
      // On success the browser navigates to Google; keep the spinner running.
    } catch (err) {
      setLoading(false);
      toast.error("Could not start Google sign-in", {
        description: err instanceof Error ? err.message : "Please try again.",
      });
    }
  }

  return (
    <div className="w-full max-w-md animate-fade-in">
      <div className="rounded-2xl border bg-card/80 p-8 shadow-xl backdrop-blur-xl">
        <div className="flex flex-col items-center text-center">
          <SprintlyMark className="h-11 w-11" />
          <h1 className="mt-5 text-2xl font-semibold tracking-tight">Welcome to Sprintly</h1>
          <p className="mt-2 text-sm text-muted-foreground">
            Sign in to create a workspace or join your team.
          </p>
        </div>

        {authError ? (
          <div className="mt-6 flex items-start gap-2 rounded-lg border border-destructive/30 bg-destructive/10 p-3 text-sm text-destructive">
            <AlertCircle className="mt-0.5 h-4 w-4 shrink-0" />
            <span>{decodeURIComponent(authError)}</span>
          </div>
        ) : null}

        <Button
          onClick={signInWithGoogle}
          loading={loading}
          size="lg"
          variant="outline"
          className="mt-7 w-full gap-3 border-2 font-medium"
        >
          {loading ? null : <GoogleIcon className="h-5 w-5" />}
          {loading ? "Redirecting…" : "Continue with Google"}
        </Button>

        <ul className="mt-8 space-y-2.5">
          {VALUE_PROPS.map((item) => (
            <li key={item} className="flex items-start gap-2.5 text-sm text-muted-foreground">
              <CheckCircle2 className="mt-0.5 h-4 w-4 shrink-0 text-primary" />
              <span>{item}</span>
            </li>
          ))}
        </ul>

        <p className="mt-8 text-center text-xs leading-relaxed text-muted-foreground">
          By continuing you agree to the Terms of Service and Privacy Policy.
        </p>
      </div>
    </div>
  );
}
