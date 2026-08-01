"use client";

import * as React from "react";
import { useRouter } from "next/navigation";
import { useQuery } from "@tanstack/react-query";

import { api } from "@/lib/api";
import { useRealtime } from "@/hooks/use-realtime";
import type { Member, Profile, Project, Workspace, WorkspaceRole } from "@/lib/types";

interface WorkspaceContextValue {
  slug: string;
  workspace: Workspace | undefined;
  me: Profile | undefined;
  members: Member[];
  projects: Project[];
  /** User IDs with an open WebSocket right now. */
  online: Set<string>;
  connection: "connecting" | "open" | "closed";
  role: WorkspaceRole;
  /** True for manager and above — gates most write affordances in the UI. */
  canManage: boolean;
  /** Guests are read-only. */
  canEdit: boolean;
  isLoading: boolean;
}

const WorkspaceContext = React.createContext<WorkspaceContextValue | null>(null);

export function useWorkspace() {
  const ctx = React.useContext(WorkspaceContext);
  if (!ctx) throw new Error("useWorkspace must be used inside <WorkspaceProvider>");
  return ctx;
}

const RANK: Record<WorkspaceRole, number> = {
  guest: 0,
  contributor: 1,
  manager: 2,
  admin: 3,
  owner: 4,
};

export function WorkspaceProvider({
  slug,
  children,
}: {
  slug: string;
  children: React.ReactNode;
}) {
  const router = useRouter();
  const { online, status } = useRealtime(slug);

  const workspaceQuery = useQuery({
    queryKey: ["workspace", slug],
    queryFn: () => api.workspace(slug),
    retry: false,
  });

  const meQuery = useQuery({ queryKey: ["me"], queryFn: api.me });

  const membersQuery = useQuery({
    queryKey: ["members", slug],
    queryFn: () => api.members(slug),
    enabled: workspaceQuery.isSuccess,
  });

  const projectsQuery = useQuery({
    queryKey: ["projects", slug],
    queryFn: () => api.projects(slug),
    enabled: workspaceQuery.isSuccess,
  });

  // A 404 here means the workspace is gone or the user was removed from it.
  React.useEffect(() => {
    if (workspaceQuery.isError) router.replace("/app");
  }, [workspaceQuery.isError, router]);

  React.useEffect(() => {
    if (workspaceQuery.isSuccess) window.localStorage.setItem("sprintly:workspace", slug);
  }, [workspaceQuery.isSuccess, slug]);

  const role = (workspaceQuery.data?.role ?? "guest") as WorkspaceRole;

  // Merge live socket presence over the stored presence column: the DB value is
  // what someone chose ("Focus mode"), the socket says whether they're actually here.
  const members = React.useMemo(() => {
    const list = membersQuery.data?.members ?? [];
    const live = new Set([...online, ...(membersQuery.data?.online ?? [])]);

    return list.map((m) =>
      m.presence === "offline" && live.has(m.user_id) ? { ...m, presence: "online" as const } : m,
    );
  }, [membersQuery.data, online]);

  const value = React.useMemo<WorkspaceContextValue>(
    () => ({
      slug,
      workspace: workspaceQuery.data,
      me: meQuery.data?.profile,
      members,
      projects: projectsQuery.data?.projects ?? [],
      online,
      connection: status,
      role,
      canManage: RANK[role] >= RANK.manager,
      canEdit: RANK[role] >= RANK.contributor,
      isLoading: workspaceQuery.isLoading || meQuery.isLoading,
    }),
    [
      slug,
      workspaceQuery.data,
      workspaceQuery.isLoading,
      meQuery.data,
      meQuery.isLoading,
      members,
      projectsQuery.data,
      online,
      status,
      role,
    ],
  );

  return <WorkspaceContext.Provider value={value}>{children}</WorkspaceContext.Provider>;
}
