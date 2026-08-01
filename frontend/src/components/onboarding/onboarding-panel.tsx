"use client";

import * as React from "react";
import { useRouter, useSearchParams } from "next/navigation";
import { useMutation, useQuery } from "@tanstack/react-query";
import { ArrowRight, Building2, Clock3, LogIn, Plus, Search, Users } from "lucide-react";
import { toast } from "sonner";

import { api, ApiError } from "@/lib/api";
import type { JoinPolicy, WorkspacePreview } from "@/lib/types";
import { Button } from "@/components/ui/button";
import { Input, Textarea } from "@/components/ui/input";
import { Badge } from "@/components/ui/misc";
import { SprintlyMark } from "@/components/brand";
import { supabase } from "@/lib/supabase/client";
import { cn } from "@/lib/utils";

type Tab = "create" | "join";

export function OnboardingPanel() {
  const params = useSearchParams();
  const [tab, setTab] = React.useState<Tab>(params.get("tab") === "join" ? "join" : "create");

  // Surface any request still awaiting approval, so a user who already applied
  // isn't left staring at a form with no idea what happened.
  const { data: mine } = useQuery({
    queryKey: ["my-workspaces"],
    queryFn: api.myWorkspaces,
  });

  const pending = mine?.pending_requests ?? [];

  return (
    <div className="w-full max-w-xl animate-fade-in">
      <div className="mb-8 flex flex-col items-center text-center">
        <SprintlyMark className="h-11 w-11" />
        <h1 className="mt-5 text-2xl font-semibold tracking-tight">Set up your workspace</h1>
        <p className="mt-2 text-sm text-muted-foreground">
          Start something new, or join a team that already uses Sprintly.
        </p>
      </div>

      {pending.length > 0 ? (
        <div className="mb-6 rounded-xl border border-amber-500/30 bg-amber-500/10 p-4">
          <div className="flex items-start gap-3">
            <Clock3 className="mt-0.5 h-4 w-4 shrink-0 text-amber-600 dark:text-amber-400" />
            <div className="text-sm">
              <p className="font-medium text-amber-900 dark:text-amber-200">
                Waiting for approval
              </p>
              <p className="mt-1 text-amber-800/80 dark:text-amber-200/70">
                Your request to join{" "}
                <strong>{pending.map((p) => p.name).join(", ")}</strong> is with an admin. You&apos;ll
                get in as soon as they approve it.
              </p>
            </div>
          </div>
        </div>
      ) : null}

      <div className="overflow-hidden rounded-2xl border bg-card/80 shadow-xl backdrop-blur-xl">
        <div className="grid grid-cols-2 gap-1 border-b bg-muted/40 p-1.5">
          <TabButton active={tab === "create"} onClick={() => setTab("create")} icon={Plus}>
            Create new
          </TabButton>
          <TabButton active={tab === "join"} onClick={() => setTab("join")} icon={LogIn}>
            Join existing
          </TabButton>
        </div>

        <div className="p-7">{tab === "create" ? <CreateWorkspaceForm /> : <JoinWorkspaceForm />}</div>
      </div>

      <div className="mt-6 text-center">
        <button
          onClick={async () => {
            await supabase().auth.signOut();
            window.location.href = "/login";
          }}
          className="text-xs text-muted-foreground underline-offset-4 hover:underline"
        >
          Sign out
        </button>
      </div>
    </div>
  );
}

function TabButton({
  active,
  onClick,
  icon: Icon,
  children,
}: {
  active: boolean;
  onClick: () => void;
  icon: React.ComponentType<{ className?: string }>;
  children: React.ReactNode;
}) {
  return (
    <button
      onClick={onClick}
      className={cn(
        "flex items-center justify-center gap-2 rounded-lg px-4 py-2.5 text-sm font-medium transition-all",
        active
          ? "bg-background text-foreground shadow-sm"
          : "text-muted-foreground hover:text-foreground",
      )}
    >
      <Icon className="h-4 w-4" />
      {children}
    </button>
  );
}

/* ------------------------------------------------------------------ create */

const POLICIES: { id: JoinPolicy; label: string; hint: string }[] = [
  { id: "request", label: "Ask to join", hint: "Anyone with the code requests access; you approve." },
  { id: "open", label: "Open", hint: "Anyone with the code joins instantly." },
  { id: "invite_only", label: "Invite only", hint: "Only people you invite by email can join." },
];

function CreateWorkspaceForm() {
  const router = useRouter();
  const [name, setName] = React.useState("");
  const [policy, setPolicy] = React.useState<JoinPolicy>("request");

  const create = useMutation({
    mutationFn: () => api.createWorkspace({ name: name.trim(), join_policy: policy }),
    onSuccess: (workspace) => {
      window.localStorage.setItem("sprintly:workspace", workspace.slug);
      toast.success(`${workspace.name} is ready`, {
        description: `Share the join code ${workspace.join_code} with your team.`,
      });
      router.push(`/w/${workspace.slug}`);
    },
    onError: (error) => {
      toast.error("Could not create the workspace", {
        description: error instanceof ApiError ? error.message : "Please try again.",
      });
    },
  });

  const valid = name.trim().length >= 2;

  return (
    <form
      onSubmit={(e) => {
        e.preventDefault();
        if (valid) create.mutate();
      }}
      className="space-y-6"
    >
      <div className="space-y-2">
        <label htmlFor="ws-name" className="text-sm font-medium">
          Workspace name
        </label>
        <div className="relative">
          <Building2 className="absolute left-3 top-1/2 h-4 w-4 -translate-y-1/2 text-muted-foreground" />
          <Input
            id="ws-name"
            value={name}
            onChange={(e) => setName(e.target.value)}
            placeholder="Acme Inc"
            autoFocus
            maxLength={80}
            className="pl-9"
          />
        </div>
        <p className="text-xs text-muted-foreground">
          Usually your company or team name. You can change it later.
        </p>
      </div>

      <fieldset className="space-y-2">
        <legend className="mb-2 text-sm font-medium">Who can join?</legend>
        <div className="space-y-2">
          {POLICIES.map((p) => (
            <label
              key={p.id}
              className={cn(
                "flex cursor-pointer items-start gap-3 rounded-lg border p-3 transition-colors",
                policy === p.id ? "border-primary bg-primary/5" : "hover:bg-accent",
              )}
            >
              <input
                type="radio"
                name="join_policy"
                value={p.id}
                checked={policy === p.id}
                onChange={() => setPolicy(p.id)}
                className="mt-1 accent-[hsl(var(--primary))]"
              />
              <span className="text-sm">
                <span className="font-medium">{p.label}</span>
                <span className="mt-0.5 block text-xs text-muted-foreground">{p.hint}</span>
              </span>
            </label>
          ))}
        </div>
      </fieldset>

      <Button type="submit" size="lg" className="w-full" disabled={!valid} loading={create.isPending}>
        Create workspace <ArrowRight className="h-4 w-4" />
      </Button>
    </form>
  );
}

/* -------------------------------------------------------------------- join */

function JoinWorkspaceForm() {
  const router = useRouter();
  const [reference, setReference] = React.useState("");
  const [message, setMessage] = React.useState("");
  const [preview, setPreview] = React.useState<WorkspacePreview | null>(null);

  const lookup = useMutation({
    mutationFn: (ref: string) => api.lookupWorkspace(ref),
    onSuccess: setPreview,
    onError: () => setPreview(null),
  });

  // Debounced preview: confirms the workspace exists before the user commits,
  // so a mistyped code fails here rather than after they hit Join.
  React.useEffect(() => {
    const ref = reference.trim();
    setPreview(null);
    if (ref.length < 6) return;

    const timer = setTimeout(() => lookup.mutate(ref), 400);
    return () => clearTimeout(timer);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [reference]);

  const join = useMutation({
    mutationFn: () =>
      api.joinWorkspace({ reference: reference.trim(), message: message.trim() || undefined }),
    onSuccess: (result) => {
      if (result.status === "pending") {
        toast.success("Request sent", {
          description: `An admin of ${result.name} will review it shortly.`,
        });
        router.refresh();
        return;
      }

      window.localStorage.setItem("sprintly:workspace", result.slug);
      toast.success(
        result.status === "already_member" ? `Welcome back to ${result.name}` : `You joined ${result.name}`,
      );
      router.push(`/w/${result.slug}`);
    },
    onError: (error) => {
      toast.error("Could not join", {
        description:
          error instanceof ApiError ? error.message : "Check the workspace ID and try again.",
      });
    },
  });

  const notFound = reference.trim().length >= 6 && !lookup.isPending && lookup.isError;
  const needsApproval = preview?.join_policy === "request";

  return (
    <form
      onSubmit={(e) => {
        e.preventDefault();
        if (reference.trim()) join.mutate();
      }}
      className="space-y-6"
    >
      <div className="space-y-2">
        <label htmlFor="ws-ref" className="text-sm font-medium">
          Workspace ID or join code
        </label>
        <div className="relative">
          <Search className="absolute left-3 top-1/2 h-4 w-4 -translate-y-1/2 text-muted-foreground" />
          <Input
            id="ws-ref"
            value={reference}
            onChange={(e) => setReference(e.target.value)}
            placeholder="SPRNT-7QK2XM  or  a UUID"
            autoFocus
            autoComplete="off"
            spellCheck={false}
            className="pl-9 font-mono"
          />
        </div>
        <p className="text-xs text-muted-foreground">
          Ask a teammate for the code in <strong>Settings → Members</strong>.
        </p>
      </div>

      {preview ? (
        <div className="flex items-center gap-3 rounded-lg border border-primary/30 bg-primary/5 p-3 animate-fade-in">
          <div className="flex h-10 w-10 shrink-0 items-center justify-center rounded-lg bg-primary/10 font-semibold text-primary">
            {preview.name.slice(0, 2).toUpperCase()}
          </div>
          <div className="min-w-0 flex-1">
            <p className="truncate text-sm font-medium">{preview.name}</p>
            <p className="flex items-center gap-1.5 text-xs text-muted-foreground">
              <Users className="h-3 w-3" />
              {preview.member_count} member{preview.member_count === 1 ? "" : "s"}
            </p>
          </div>
          {needsApproval ? <Badge variant="warning">Approval needed</Badge> : null}
          {preview.join_policy === "invite_only" ? <Badge variant="danger">Invite only</Badge> : null}
          {preview.join_policy === "open" ? <Badge variant="success">Open</Badge> : null}
        </div>
      ) : null}

      {notFound ? (
        <p className="rounded-lg border border-destructive/30 bg-destructive/10 p-3 text-sm text-destructive">
          No workspace matches that ID or join code.
        </p>
      ) : null}

      {needsApproval ? (
        <div className="space-y-2 animate-fade-in">
          <label htmlFor="ws-msg" className="text-sm font-medium">
            Message to the admins <span className="text-muted-foreground">(optional)</span>
          </label>
          <Textarea
            id="ws-msg"
            value={message}
            onChange={(e) => setMessage(e.target.value)}
            placeholder="Hi — I'm joining the design team this week."
            maxLength={500}
            rows={3}
          />
        </div>
      ) : null}

      <Button
        type="submit"
        size="lg"
        className="w-full"
        disabled={!reference.trim() || preview?.join_policy === "invite_only"}
        loading={join.isPending}
      >
        {needsApproval ? "Request to join" : "Join workspace"} <ArrowRight className="h-4 w-4" />
      </Button>
    </form>
  );
}
