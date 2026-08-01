"use client";

import * as React from "react";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { toast } from "sonner";

import { api, ApiError } from "@/lib/api";
import type { JoinPolicy } from "@/lib/types";
import { cn } from "@/lib/utils";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/misc";
import { Topbar } from "@/components/workspace/topbar";
import { useWorkspace } from "@/components/workspace/workspace-provider";

const POLICIES: { id: JoinPolicy; label: string; hint: string }[] = [
  { id: "request", label: "Ask to join", hint: "Requests need admin approval." },
  { id: "open", label: "Open", hint: "Anyone with the join code joins instantly." },
  { id: "invite_only", label: "Invite only", hint: "The join code stops working." },
];

export default function WorkspaceSettingsPage() {
  const { slug, workspace, role } = useWorkspace();
  const queryClient = useQueryClient();

  const isAdmin = role === "owner" || role === "admin";
  const [name, setName] = React.useState("");

  // Seed the field once the workspace loads, without stomping later edits.
  React.useEffect(() => {
    if (workspace && !name) setName(workspace.name);
  }, [workspace, name]);

  const update = useMutation({
    mutationFn: (body: { name?: string; join_policy?: JoinPolicy }) =>
      api.updateWorkspace(slug, body),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ["workspace", slug] });
      queryClient.invalidateQueries({ queryKey: ["my-workspaces"] });
      toast.success("Workspace updated");
    },
    onError: (error) =>
      toast.error("Could not save", {
        description: error instanceof ApiError ? error.message : undefined,
      }),
  });

  return (
    <>
      <Topbar title="Settings" />

      <div className="scrollbar-thin flex-1 overflow-y-auto p-4 lg:p-6">
        <div className="mx-auto max-w-2xl space-y-6">
          <Card>
            <CardHeader>
              <CardTitle className="text-base">General</CardTitle>
              <CardDescription>
                The workspace URL (<code className="font-mono">/w/{workspace?.slug}</code>) is fixed
                once created, so existing links never break.
              </CardDescription>
            </CardHeader>
            <CardContent className="space-y-4">
              <label className="block space-y-1.5">
                <span className="text-sm font-medium">Workspace name</span>
                <Input
                  value={name}
                  onChange={(e) => setName(e.target.value)}
                  disabled={!isAdmin}
                  maxLength={80}
                />
              </label>

              {isAdmin ? (
                <Button
                  onClick={() => update.mutate({ name: name.trim() })}
                  disabled={!name.trim() || name.trim() === workspace?.name}
                  loading={update.isPending}
                >
                  Save changes
                </Button>
              ) : (
                <p className="text-sm text-muted-foreground">
                  Only admins and the owner can change these settings.
                </p>
              )}
            </CardContent>
          </Card>

          <Card>
            <CardHeader>
              <CardTitle className="text-base">Who can join</CardTitle>
              <CardDescription>
                Controls what happens when someone enters your workspace ID or join code.
              </CardDescription>
            </CardHeader>
            <CardContent className="space-y-2">
              {POLICIES.map((p) => (
                <label
                  key={p.id}
                  className={cn(
                    "flex items-start gap-3 rounded-lg border p-3 transition-colors",
                    isAdmin ? "cursor-pointer hover:bg-accent" : "cursor-not-allowed opacity-70",
                    workspace?.join_policy === p.id && "border-primary bg-primary/5",
                  )}
                >
                  <input
                    type="radio"
                    name="join_policy"
                    checked={workspace?.join_policy === p.id}
                    disabled={!isAdmin}
                    onChange={() => update.mutate({ join_policy: p.id })}
                    className="mt-1 accent-[hsl(var(--primary))]"
                  />
                  <span className="text-sm">
                    <span className="font-medium">{p.label}</span>
                    <span className="mt-0.5 block text-xs text-muted-foreground">{p.hint}</span>
                  </span>
                </label>
              ))}
            </CardContent>
          </Card>
        </div>
      </div>
    </>
  );
}
