"use client";

import * as React from "react";
import { useRouter } from "next/navigation";
import { useQuery } from "@tanstack/react-query";

import { api } from "@/lib/api";
import { SprintlyMark } from "@/components/brand";

/**
 * Post-login router. Everyone lands here; where they go next depends on what
 * they already have:
 *
 *   no workspaces  -> /onboarding  (create or join)
 *   one or more    -> the last one they used, else the first
 */
export default function AppEntryPage() {
  const router = useRouter();

  const { data, error } = useQuery({
    queryKey: ["my-workspaces"],
    // /me upserts the profile row, so it must run before anything reads it.
    queryFn: async () => {
      await api.me();
      return api.myWorkspaces();
    },
    retry: 1,
  });

  React.useEffect(() => {
    if (!data) return;

    if (data.workspaces.length === 0) {
      router.replace("/onboarding");
      return;
    }

    const lastUsed =
      typeof window !== "undefined" ? window.localStorage.getItem("sprintly:workspace") : null;

    const target =
      data.workspaces.find((w) => w.slug === lastUsed) ?? data.workspaces[0];

    router.replace(`/w/${target.slug}`);
  }, [data, router]);

  React.useEffect(() => {
    if (error) router.replace("/onboarding");
  }, [error, router]);

  return (
    <div className="flex min-h-dvh flex-col items-center justify-center gap-4">
      <SprintlyMark className="h-10 w-10 animate-pulse" />
      <p className="text-sm text-muted-foreground">Loading your workspaces…</p>
    </div>
  );
}
