"use client";

import * as React from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Check, Copy, RefreshCw, UserX, X } from "lucide-react";
import { toast } from "sonner";

import { api, ApiError } from "@/lib/api";
import type { WorkspaceRole } from "@/lib/types";
import { relativeTime } from "@/lib/utils";
import { Button } from "@/components/ui/button";
import { Badge, Card, CardContent, CardDescription, CardHeader, CardTitle, Skeleton } from "@/components/ui/misc";
import { PRESENCE_LABEL, UserAvatar } from "@/components/ui/avatar";
import { Topbar } from "@/components/workspace/topbar";
import { useWorkspace } from "@/components/workspace/workspace-provider";

const ASSIGNABLE: WorkspaceRole[] = ["admin", "manager", "contributor", "guest"];

export default function MembersPage() {
  const { slug, workspace, members, canManage, role, me } = useWorkspace();
  const queryClient = useQueryClient();

  const joinRequests = useQuery({
    queryKey: ["join-requests", slug],
    queryFn: () => api.joinRequests(slug),
    enabled: canManage,
  });

  const decide = useMutation({
    mutationFn: (vars: { id: string; approve: boolean }) =>
      api.decideJoinRequest(slug, vars.id, vars.approve),
    onSuccess: (_data, vars) => {
      queryClient.invalidateQueries({ queryKey: ["join-requests", slug] });
      queryClient.invalidateQueries({ queryKey: ["members", slug] });
      toast.success(vars.approve ? "Member approved" : "Request declined");
    },
    onError: (error) =>
      toast.error("Could not apply that decision", {
        description: error instanceof ApiError ? error.message : undefined,
      }),
  });

  const changeRole = useMutation({
    mutationFn: (vars: { userId: string; role: WorkspaceRole }) =>
      api.updateMember(slug, vars.userId, { role: vars.role }),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ["members", slug] });
      toast.success("Role updated");
    },
    onError: (error) =>
      toast.error("Could not update the role", {
        description: error instanceof ApiError ? error.message : undefined,
      }),
  });

  const removeMember = useMutation({
    mutationFn: (userId: string) => api.removeMember(slug, userId),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ["members", slug] });
      toast.success("Member removed");
    },
    onError: (error) =>
      toast.error("Could not remove that member", {
        description: error instanceof ApiError ? error.message : undefined,
      }),
  });

  const rotate = useMutation({
    mutationFn: () => api.rotateJoinCode(slug),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ["workspace", slug] });
      toast.success("Join code rotated", { description: "The old code no longer works." });
    },
  });

  const pending = joinRequests.data?.requests ?? [];

  return (
    <>
      <Topbar title="Members" />

      <div className="scrollbar-thin flex-1 space-y-6 overflow-y-auto p-4 lg:p-6">
        {canManage && workspace?.join_code ? (
          <Card>
            <CardHeader>
              <CardTitle className="text-base">Invite your team</CardTitle>
              <CardDescription>
                Share this code. Teammates create an account, choose &ldquo;Join existing&rdquo;
                and paste it.
              </CardDescription>
            </CardHeader>
            <CardContent className="flex flex-wrap items-center gap-2">
              <code className="rounded-lg border bg-muted px-4 py-2.5 font-mono text-lg font-semibold tracking-widest">
                {workspace.join_code}
              </code>
              <Button
                variant="outline"
                onClick={() => {
                  navigator.clipboard.writeText(workspace.join_code!);
                  toast.success("Join code copied");
                }}
              >
                <Copy className="h-4 w-4" /> Copy
              </Button>
              <Button variant="ghost" onClick={() => rotate.mutate()} loading={rotate.isPending}>
                <RefreshCw className="h-4 w-4" /> Rotate
              </Button>
              <Badge variant="secondary" className="ml-auto capitalize">
                {workspace.join_policy.replace("_", " ")}
              </Badge>
            </CardContent>
          </Card>
        ) : null}

        {canManage && pending.length > 0 ? (
          <Card>
            <CardHeader>
              <CardTitle className="text-base">
                Requests to join
                <Badge variant="warning" className="ml-2">
                  {pending.length}
                </Badge>
              </CardTitle>
            </CardHeader>
            <CardContent className="space-y-2">
              {pending.map((r) => (
                <div key={r.id} className="flex items-center gap-3 rounded-lg border p-3">
                  <UserAvatar name={r.full_name} email={r.email} src={r.avatar_url} />
                  <div className="min-w-0 flex-1">
                    <p className="truncate text-sm font-medium">{r.full_name ?? r.email}</p>
                    <p className="truncate text-xs text-muted-foreground">
                      {r.message || r.email} · {relativeTime(r.created_at)}
                    </p>
                  </div>
                  <Button
                    size="sm"
                    onClick={() => decide.mutate({ id: r.id, approve: true })}
                    loading={decide.isPending}
                  >
                    <Check className="h-4 w-4" /> Approve
                  </Button>
                  <Button
                    size="sm"
                    variant="ghost"
                    onClick={() => decide.mutate({ id: r.id, approve: false })}
                  >
                    <X className="h-4 w-4" />
                  </Button>
                </div>
              ))}
            </CardContent>
          </Card>
        ) : null}

        <Card>
          <CardHeader>
            <CardTitle className="text-base">Members ({members.length})</CardTitle>
          </CardHeader>
          <CardContent className="space-y-1">
            {members.length === 0 ? (
              <Skeleton className="h-14 w-full" />
            ) : (
              members.map((m) => {
                const isSelf = m.user_id === me?.id;
                // Owners are immutable here; ownership transfer is separate.
                const editable = canManage && m.role !== "owner" && !isSelf;

                return (
                  <div
                    key={m.user_id}
                    className="flex flex-wrap items-center gap-3 rounded-lg px-2 py-2.5 transition-colors hover:bg-accent/50"
                  >
                    <UserAvatar
                      name={m.full_name}
                      email={m.email}
                      src={m.avatar_url}
                      presence={m.presence}
                    />
                    <div className="min-w-0 flex-1">
                      <p className="truncate text-sm font-medium">
                        {m.full_name ?? m.email}
                        {isSelf ? <span className="ml-1.5 text-muted-foreground">(you)</span> : null}
                      </p>
                      <p className="truncate text-xs text-muted-foreground">
                        {m.email} · {PRESENCE_LABEL[m.presence]}
                        {m.title ? ` · ${m.title}` : ""}
                      </p>
                    </div>

                    {m.status !== "active" ? (
                      <Badge variant="warning" className="capitalize">
                        {m.status}
                      </Badge>
                    ) : null}

                    {editable ? (
                      <select
                        value={m.role}
                        onChange={(e) =>
                          changeRole.mutate({
                            userId: m.user_id,
                            role: e.target.value as WorkspaceRole,
                          })
                        }
                        className="h-8 rounded-md border border-input bg-background px-2 text-xs capitalize"
                      >
                        {ASSIGNABLE.map((r) => (
                          <option key={r} value={r}>
                            {r}
                          </option>
                        ))}
                      </select>
                    ) : (
                      <Badge variant="secondary" className="capitalize">
                        {m.role}
                      </Badge>
                    )}

                    {role === "owner" || (role === "admin" && editable) ? (
                      <Button
                        size="icon"
                        variant="ghost"
                        title="Remove from workspace"
                        onClick={() => {
                          if (confirm(`Remove ${m.full_name ?? m.email} from this workspace?`)) {
                            removeMember.mutate(m.user_id);
                          }
                        }}
                        disabled={m.role === "owner" || isSelf}
                      >
                        <UserX className="h-4 w-4 text-destructive" />
                      </Button>
                    ) : null}
                  </div>
                );
              })
            )}
          </CardContent>
        </Card>
      </div>
    </>
  );
}
