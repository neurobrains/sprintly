import { cn } from "@/lib/utils";

/** Sprintly mark: three stacked bars sprinting to the right. */
export function SprintlyMark({ className }: { className?: string }) {
  return (
    <svg viewBox="0 0 40 40" fill="none" className={cn("h-8 w-8", className)} aria-hidden="true">
      <rect width="40" height="40" rx="11" className="fill-primary" />
      <path
        d="M11 14h18M11 20h13M11 26h8"
        stroke="currentColor"
        className="text-primary-foreground"
        strokeWidth="3"
        strokeLinecap="round"
      />
    </svg>
  );
}

export function SprintlyWordmark({ className }: { className?: string }) {
  return (
    <span className={cn("flex items-center gap-2", className)}>
      <SprintlyMark className="h-7 w-7" />
      <span className="text-lg font-semibold tracking-tight">Sprintly</span>
    </span>
  );
}
