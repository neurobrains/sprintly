import { clsx, type ClassValue } from "clsx";
import { twMerge } from "tailwind-merge";

export function cn(...inputs: ClassValue[]) {
  return twMerge(clsx(inputs));
}

/** "Ada Lovelace" -> "AL"; falls back to the email's first letter. */
export function initials(name: string | null | undefined, email?: string) {
  const source = name?.trim() || email?.split("@")[0] || "?";
  const parts = source.split(/[\s._-]+/).filter(Boolean);
  if (parts.length >= 2) return (parts[0][0] + parts[1][0]).toUpperCase();
  return source.slice(0, 2).toUpperCase();
}

export function formatDuration(seconds: number) {
  const h = Math.floor(seconds / 3600);
  const m = Math.floor((seconds % 3600) / 60);
  if (h === 0 && m === 0) return `${seconds}s`;
  if (h === 0) return `${m}m`;
  return `${h}h ${m}m`;
}

/** Compact, human relative time: "just now", "4m", "3d", then a date. */
export function relativeTime(iso: string) {
  const then = new Date(iso).getTime();
  const diff = Date.now() - then;
  const min = 60_000;

  if (diff < min) return "just now";
  if (diff < 60 * min) return `${Math.floor(diff / min)}m ago`;
  if (diff < 24 * 60 * min) return `${Math.floor(diff / (60 * min))}h ago`;
  if (diff < 7 * 24 * 60 * min) return `${Math.floor(diff / (24 * 60 * min))}d ago`;

  return new Date(iso).toLocaleDateString(undefined, { month: "short", day: "numeric" });
}

/** Due-date colouring: overdue is red, today/tomorrow amber, else muted. */
export function dueDateTone(due: string | null, done: boolean) {
  if (!due || done) return "text-muted-foreground";

  const diff = new Date(due).getTime() - Date.now();
  if (diff < 0) return "text-red-600 dark:text-red-400";
  if (diff < 48 * 3600_000) return "text-amber-600 dark:text-amber-400";
  return "text-muted-foreground";
}

export function formatDate(iso: string | null) {
  if (!iso) return "";
  return new Date(iso).toLocaleDateString(undefined, { month: "short", day: "numeric" });
}

/**
 * Fractional ranking for optimistic drag-and-drop.
 *
 * The server recomputes the authoritative rank; this only keeps the card from
 * visibly snapping back before the response lands.
 */
export function midpointRank(before: number | null, after: number | null) {
  if (before === null && after === null) return 65536;
  if (before === null) return after! / 2;
  if (after === null) return before + 65536;
  return (before + after) / 2;
}
