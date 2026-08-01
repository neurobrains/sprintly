"use client";

import * as React from "react";
import * as AvatarPrimitive from "@radix-ui/react-avatar";

import { cn, initials } from "@/lib/utils";
import type { Presence } from "@/lib/types";

const PRESENCE_RING: Record<Presence, string> = {
  online: "bg-emerald-500",
  away: "bg-amber-500",
  in_meeting: "bg-red-500",
  focus: "bg-violet-500",
  offline: "bg-slate-400",
};

export const PRESENCE_LABEL: Record<Presence, string> = {
  online: "Online",
  away: "Away",
  in_meeting: "In a meeting",
  focus: "Focus mode",
  offline: "Offline",
};

const SIZES = {
  xs: "h-5 w-5 text-[9px]",
  sm: "h-7 w-7 text-[10px]",
  md: "h-9 w-9 text-xs",
  lg: "h-12 w-12 text-sm",
} as const;

interface UserAvatarProps {
  name?: string | null;
  email?: string;
  src?: string | null;
  size?: keyof typeof SIZES;
  presence?: Presence;
  className?: string;
}

/**
 * Avatar with an optional presence dot. `presence` is omitted rather than set
 * to "offline" when the state is genuinely unknown, so the dot never lies.
 */
export function UserAvatar({
  name,
  email,
  src,
  size = "md",
  presence,
  className,
}: UserAvatarProps) {
  return (
    <span className={cn("relative inline-flex shrink-0", className)}>
      <AvatarPrimitive.Root
        className={cn(
          "relative flex shrink-0 overflow-hidden rounded-full ring-1 ring-border",
          SIZES[size],
        )}
      >
        {src ? (
          <AvatarPrimitive.Image
            src={src}
            alt={name ?? email ?? "User"}
            className="aspect-square h-full w-full object-cover"
          />
        ) : null}
        <AvatarPrimitive.Fallback
          className="flex h-full w-full items-center justify-center bg-primary/10 font-semibold text-primary"
          delayMs={src ? 300 : 0}
        >
          {initials(name, email)}
        </AvatarPrimitive.Fallback>
      </AvatarPrimitive.Root>

      {presence ? (
        <span
          title={PRESENCE_LABEL[presence]}
          className={cn(
            "absolute -bottom-0.5 -right-0.5 rounded-full ring-2 ring-background",
            size === "xs" || size === "sm" ? "h-2 w-2" : "h-2.5 w-2.5",
            PRESENCE_RING[presence],
          )}
        />
      ) : null}
    </span>
  );
}

/** Overlapping stack, e.g. members on a project card. */
export function AvatarStack({
  users,
  max = 4,
  size = "sm",
}: {
  users: { id: string; full_name: string | null; email?: string; avatar_url: string | null }[];
  max?: number;
  size?: keyof typeof SIZES;
}) {
  const shown = users.slice(0, max);
  const overflow = users.length - shown.length;

  return (
    <div className="flex -space-x-2">
      {shown.map((u) => (
        <UserAvatar
          key={u.id}
          name={u.full_name}
          email={u.email}
          src={u.avatar_url}
          size={size}
          className="ring-2 ring-background"
        />
      ))}
      {overflow > 0 ? (
        <span
          className={cn(
            "inline-flex items-center justify-center rounded-full bg-muted font-medium text-muted-foreground ring-2 ring-background",
            SIZES[size],
          )}
        >
          +{overflow}
        </span>
      ) : null}
    </div>
  );
}
