"use client";

import * as React from "react";
import Link from "next/link";
import { useRouter } from "next/navigation";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import * as DropdownMenu from "@radix-ui/react-dropdown-menu";
import { Bell, Check, LogOut, Moon, Search, Sun, Users } from "lucide-react";
import { useTheme } from "next-themes";
import { toast } from "sonner";

import { api } from "@/lib/api";
import { supabase } from "@/lib/supabase/client";
import { cn, relativeTime } from "@/lib/utils";
import type { Presence } from "@/lib/types";
import { Button } from "@/components/ui/button";
import { PRESENCE_LABEL, UserAvatar, AvatarStack } from "@/components/ui/avatar";
import { Badge } from "@/components/ui/misc";
import { useWorkspace } from "@/components/workspace/workspace-provider";

const PRESENCE_OPTIONS: Presence[] = ["online", "focus", "in_meeting", "away"];

export function Topbar({ title }: { title?: string }) {
  const { slug, me, members, online } = useWorkspace();

  const onlineMembers = members.filter(
    (m) => online.has(m.user_id) || (m.presence !== "offline" && m.user_id !== me?.id),
  );

  return (
    <header className="flex h-14 shrink-0 items-center gap-3 border-b bg-background/80 px-4 backdrop-blur-md lg:px-6">
      <h1 className="truncate text-sm font-semibold lg:text-base">{title ?? "Board"}</h1>

      <div className="ml-auto flex items-center gap-2">
        {onlineMembers.length > 0 ? (
          <div className="hidden items-center gap-2 md:flex">
            <AvatarStack
              users={onlineMembers.map((m) => ({
                id: m.user_id,
                full_name: m.full_name,
                email: m.email,
                avatar_url: m.avatar_url,
              }))}
              max={4}
            />
          </div>
        ) : null}

        <Button variant="ghost" size="icon" asChild title="Members">
          <Link href={`/w/${slug}/settings/members`}>
            <Users className="h-4 w-4" />
          </Link>
        </Button>

        <NotificationBell />
        <ThemeToggle />
        <AccountMenu />
      </div>
    </header>
  );
}

function NotificationBell() {
  const { slug } = useWorkspace();
  const queryClient = useQueryClient();
  const router = useRouter();

  const { data } = useQuery({
    queryKey: ["notifications", slug],
    queryFn: () => api.notifications(slug),
    refetchInterval: 60_000,
  });

  const markRead = useMutation({
    mutationFn: (ids?: number[]) => api.markNotificationsRead(slug, ids),
    onSuccess: () => queryClient.invalidateQueries({ queryKey: ["notifications", slug] }),
  });

  const unread = data?.unread_count ?? 0;
  const items = data?.notifications ?? [];

  return (
    <DropdownMenu.Root>
      <DropdownMenu.Trigger asChild>
        <Button variant="ghost" size="icon" className="relative" title="Notifications">
          <Bell className="h-4 w-4" />
          {unread > 0 ? (
            <span className="absolute right-1 top-1 flex h-4 min-w-4 items-center justify-center rounded-full bg-primary px-1 text-[10px] font-semibold text-primary-foreground">
              {unread > 9 ? "9+" : unread}
            </span>
          ) : null}
        </Button>
      </DropdownMenu.Trigger>

      <DropdownMenu.Portal>
        <DropdownMenu.Content
          align="end"
          sideOffset={8}
          className="z-50 w-96 overflow-hidden rounded-xl border bg-popover shadow-xl data-[state=open]:animate-in data-[state=open]:fade-in-0"
        >
          <div className="flex items-center justify-between border-b px-4 py-3">
            <span className="text-sm font-semibold">Notifications</span>
            {unread > 0 ? (
              <button
                onClick={() => markRead.mutate(undefined)}
                className="flex items-center gap-1 text-xs text-primary hover:underline"
              >
                <Check className="h-3 w-3" /> Mark all read
              </button>
            ) : null}
          </div>

          <div className="scrollbar-thin max-h-96 overflow-y-auto">
            {items.length === 0 ? (
              <p className="px-4 py-10 text-center text-sm text-muted-foreground">
                You&apos;re all caught up.
              </p>
            ) : (
              items.map((n) => (
                <button
                  key={n.id}
                  onClick={() => {
                    if (!n.read_at) markRead.mutate([n.id]);
                    if (n.task_id) router.push(`/w/${slug}?task=${n.task_id}`);
                    else if (n.url) router.push(n.url);
                  }}
                  className={cn(
                    "flex w-full gap-3 border-b px-4 py-3 text-left transition-colors last:border-0 hover:bg-accent",
                    !n.read_at && "bg-primary/5",
                  )}
                >
                  <UserAvatar
                    size="sm"
                    name={n.actor?.full_name}
                    email={n.actor?.email}
                    src={n.actor?.avatar_url}
                  />
                  <div className="min-w-0 flex-1">
                    <p className="text-sm font-medium leading-snug">{n.title}</p>
                    {n.body ? (
                      <p className="mt-0.5 line-clamp-2 text-xs text-muted-foreground">{n.body}</p>
                    ) : null}
                    <p className="mt-1 text-[11px] text-muted-foreground">
                      {relativeTime(n.created_at)}
                    </p>
                  </div>
                  {!n.read_at ? (
                    <span className="mt-1.5 h-2 w-2 shrink-0 rounded-full bg-primary" />
                  ) : null}
                </button>
              ))
            )}
          </div>
        </DropdownMenu.Content>
      </DropdownMenu.Portal>
    </DropdownMenu.Root>
  );
}

function ThemeToggle() {
  const { resolvedTheme, setTheme } = useTheme();
  const [mounted, setMounted] = React.useState(false);

  // The server cannot know the resolved theme, so render the icon only after
  // hydration to avoid a mismatch.
  React.useEffect(() => setMounted(true), []);

  return (
    <Button
      variant="ghost"
      size="icon"
      onClick={() => setTheme(resolvedTheme === "dark" ? "light" : "dark")}
      title="Toggle theme"
    >
      {mounted && resolvedTheme === "dark" ? (
        <Sun className="h-4 w-4" />
      ) : (
        <Moon className="h-4 w-4" />
      )}
    </Button>
  );
}

function AccountMenu() {
  const { me, role, slug } = useWorkspace();
  const queryClient = useQueryClient();

  const setPresence = useMutation({
    mutationFn: (presence: Presence) => api.updateMe({ presence }),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ["me"] });
      queryClient.invalidateQueries({ queryKey: ["members", slug] });
    },
    onError: () => toast.error("Could not update your status"),
  });

  return (
    <DropdownMenu.Root>
      <DropdownMenu.Trigger asChild>
        <button className="rounded-full outline-none ring-offset-background focus-visible:ring-2 focus-visible:ring-ring">
          <UserAvatar
            name={me?.full_name}
            email={me?.email}
            src={me?.avatar_url}
            presence={me?.presence}
          />
        </button>
      </DropdownMenu.Trigger>

      <DropdownMenu.Portal>
        <DropdownMenu.Content
          align="end"
          sideOffset={8}
          className="z-50 w-64 overflow-hidden rounded-xl border bg-popover p-1.5 shadow-xl"
        >
          <div className="px-3 py-2.5">
            <p className="truncate text-sm font-medium">{me?.full_name ?? me?.email}</p>
            <p className="truncate text-xs text-muted-foreground">{me?.email}</p>
            <Badge variant="secondary" className="mt-2 capitalize">
              {role}
            </Badge>
          </div>

          <DropdownMenu.Separator className="my-1 h-px bg-border" />

          <DropdownMenu.Label className="px-3 py-1.5 text-[11px] font-semibold uppercase tracking-wider text-muted-foreground">
            Status
          </DropdownMenu.Label>

          {PRESENCE_OPTIONS.map((p) => (
            <DropdownMenu.Item
              key={p}
              onSelect={() => setPresence.mutate(p)}
              className="flex cursor-pointer items-center gap-2.5 rounded-md px-3 py-2 text-sm outline-none data-[highlighted]:bg-accent"
            >
              <span
                className={cn(
                  "h-2 w-2 rounded-full",
                  p === "online" && "bg-emerald-500",
                  p === "focus" && "bg-violet-500",
                  p === "in_meeting" && "bg-red-500",
                  p === "away" && "bg-amber-500",
                )}
              />
              {PRESENCE_LABEL[p]}
              {me?.presence === p ? <Check className="ml-auto h-3.5 w-3.5" /> : null}
            </DropdownMenu.Item>
          ))}

          <DropdownMenu.Separator className="my-1 h-px bg-border" />

          <DropdownMenu.Item
            onSelect={() => {
              window.location.href = "/onboarding";
            }}
            className="flex cursor-pointer items-center gap-2.5 rounded-md px-3 py-2 text-sm outline-none data-[highlighted]:bg-accent"
          >
            <Search className="h-4 w-4" />
            Switch workspace
          </DropdownMenu.Item>

          <DropdownMenu.Item
            onSelect={async () => {
              await supabase().auth.signOut();
              window.location.href = "/login";
            }}
            className="flex cursor-pointer items-center gap-2.5 rounded-md px-3 py-2 text-sm text-destructive outline-none data-[highlighted]:bg-destructive/10"
          >
            <LogOut className="h-4 w-4" />
            Sign out
          </DropdownMenu.Item>
        </DropdownMenu.Content>
      </DropdownMenu.Portal>
    </DropdownMenu.Root>
  );
}
