"use client";

import * as React from "react";
import Link from "next/link";
import { usePathname } from "next/navigation";
import {
  Activity,
  BarChart3,
  CalendarDays,
  ChevronDown,
  Clock,
  Folder,
  GanttChartSquare,
  Inbox,
  KanbanSquare,
  Settings,
  Users,
} from "lucide-react";

import { cn } from "@/lib/utils";
import { useWorkspace } from "@/components/workspace/workspace-provider";
import { SprintlyMark } from "@/components/brand";
import { Badge } from "@/components/ui/misc";

const NAV = [
  { href: "", label: "Board", icon: KanbanSquare, exact: true },
  { href: "/list", label: "List", icon: Folder },
  { href: "/calendar", label: "Calendar", icon: CalendarDays },
  { href: "/timeline", label: "Timeline", icon: GanttChartSquare },
  { href: "/inbox", label: "Inbox", icon: Inbox },
  { href: "/workload", label: "Workload", icon: BarChart3 },
  { href: "/time", label: "Time", icon: Clock },
  { href: "/activity", label: "Activity", icon: Activity },
];

export function Sidebar() {
  const { slug, workspace, projects, members, connection } = useWorkspace();
  const pathname = usePathname();
  const base = `/w/${slug}`;

  return (
    <aside className="hidden w-60 shrink-0 flex-col border-r bg-card/40 lg:flex">
      <div className="flex h-14 items-center gap-2 border-b px-4">
        <SprintlyMark className="h-7 w-7" />
        <div className="min-w-0 flex-1">
          <p className="truncate text-sm font-semibold leading-tight">
            {workspace?.name ?? "Loading…"}
          </p>
          <p className="flex items-center gap-1 text-[11px] text-muted-foreground">
            <span
              className={cn(
                "h-1.5 w-1.5 rounded-full",
                connection === "open"
                  ? "bg-emerald-500"
                  : connection === "connecting"
                    ? "animate-pulse bg-amber-500"
                    : "bg-slate-400",
              )}
            />
            {connection === "open" ? "Live" : connection === "connecting" ? "Connecting" : "Offline"}
          </p>
        </div>
      </div>

      <nav className="scrollbar-thin flex-1 overflow-y-auto p-3">
        <ul className="space-y-0.5">
          {NAV.map(({ href, label, icon: Icon, exact }) => {
            const target = `${base}${href}`;
            const active = exact ? pathname === target : pathname.startsWith(target);

            return (
              <li key={label}>
                <Link
                  href={target}
                  className={cn(
                    "flex items-center gap-2.5 rounded-lg px-3 py-2 text-sm font-medium transition-colors",
                    active
                      ? "bg-primary/10 text-primary"
                      : "text-muted-foreground hover:bg-accent hover:text-foreground",
                  )}
                >
                  <Icon className="h-4 w-4" />
                  {label}
                </Link>
              </li>
            );
          })}
        </ul>

        <ProjectSection base={base} projects={projects} pathname={pathname} />

        <div className="mt-6 border-t pt-3">
          <Link
            href={`${base}/settings/members`}
            className={cn(
              "flex items-center gap-2.5 rounded-lg px-3 py-2 text-sm font-medium transition-colors",
              pathname.startsWith(`${base}/settings/members`)
                ? "bg-primary/10 text-primary"
                : "text-muted-foreground hover:bg-accent hover:text-foreground",
            )}
          >
            <Users className="h-4 w-4" />
            Members
            <Badge variant="secondary" className="ml-auto">
              {members.length}
            </Badge>
          </Link>
          <Link
            href={`${base}/settings`}
            className={cn(
              "flex items-center gap-2.5 rounded-lg px-3 py-2 text-sm font-medium transition-colors",
              pathname === `${base}/settings`
                ? "bg-primary/10 text-primary"
                : "text-muted-foreground hover:bg-accent hover:text-foreground",
            )}
          >
            <Settings className="h-4 w-4" />
            Settings
          </Link>
        </div>
      </nav>
    </aside>
  );
}

function ProjectSection({
  base,
  projects,
  pathname,
}: {
  base: string;
  projects: ReturnType<typeof useWorkspace>["projects"];
  pathname: string;
}) {
  const [open, setOpen] = React.useState(true);

  return (
    <div className="mt-6">
      <button
        onClick={() => setOpen((v) => !v)}
        className="flex w-full items-center gap-1 px-3 py-1 text-[11px] font-semibold uppercase tracking-wider text-muted-foreground hover:text-foreground"
      >
        <ChevronDown className={cn("h-3 w-3 transition-transform", !open && "-rotate-90")} />
        Projects
      </button>

      {open ? (
        <ul className="mt-1 space-y-0.5">
          {projects.length === 0 ? (
            <li className="px-3 py-2 text-xs text-muted-foreground">No projects yet</li>
          ) : (
            projects.map((p) => {
              const target = `${base}/projects/${p.id}`;
              const active = pathname === target;

              return (
                <li key={p.id}>
                  <Link
                    href={target}
                    className={cn(
                      "flex items-center gap-2.5 rounded-lg px-3 py-1.5 text-sm transition-colors",
                      active
                        ? "bg-primary/10 text-primary"
                        : "text-muted-foreground hover:bg-accent hover:text-foreground",
                    )}
                  >
                    <span
                      className="h-2 w-2 shrink-0 rounded-sm"
                      style={{ backgroundColor: p.color }}
                    />
                    <span className="truncate">{p.name}</span>
                    <span className="ml-auto shrink-0 font-mono text-[10px] text-muted-foreground">
                      {p.key}
                    </span>
                  </Link>
                </li>
              );
            })
          )}
        </ul>
      ) : null}
    </div>
  );
}
