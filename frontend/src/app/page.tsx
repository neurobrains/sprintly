import Link from "next/link";
import { ArrowRight, CalendarRange, Clock, GanttChart, MessagesSquare, Users } from "lucide-react";

import { Button } from "@/components/ui/button";
import { SprintlyWordmark } from "@/components/brand";

const PROBLEMS = [
  {
    icon: MessagesSquare,
    problem: "Work is scattered across five tools",
    solution:
      "Tasks, docs, discussion and time live in one workspace, so nobody has to guess where a decision was made.",
  },
  {
    icon: Users,
    problem: "Nobody knows who is doing what",
    solution:
      "Every task has an owner, a state and a due date. Presence shows who is online, in a meeting, or heads-down.",
  },
  {
    icon: GanttChart,
    problem: "Blockers surface too late",
    solution:
      "Dependency trees and Gantt views make the critical path visible before the deadline slips.",
  },
  {
    icon: Clock,
    problem: "Capacity is a guess",
    solution:
      "Time tracking and workload indicators show who is over capacity this week, before they burn out.",
  },
];

export default function LandingPage() {
  return (
    <div className="mesh-bg min-h-dvh">
      <header className="mx-auto flex max-w-6xl items-center justify-between px-6 py-6">
        <SprintlyWordmark />
        <Button asChild variant="outline">
          <Link href="/login">Sign in</Link>
        </Button>
      </header>

      <main className="mx-auto max-w-6xl px-6">
        <section className="py-20 text-center sm:py-28">
          <span className="inline-flex items-center gap-2 rounded-full border bg-background/60 px-3 py-1 text-xs font-medium text-muted-foreground backdrop-blur">
            Open source · Self-hostable
          </span>

          <h1 className="mx-auto mt-6 max-w-3xl text-4xl font-semibold tracking-tight sm:text-6xl">
            One workspace for the{" "}
            <span className="bg-gradient-to-r from-primary to-violet-500 bg-clip-text text-transparent">
              whole team
            </span>
          </h1>

          <p className="mx-auto mt-6 max-w-2xl text-lg text-muted-foreground">
            Boards, docs, discussion, time tracking and workload — together. Sprintly replaces the
            stack of half-used tools where your team&apos;s context goes to die.
          </p>

          <div className="mt-10 flex flex-col items-center justify-center gap-3 sm:flex-row">
            <Button asChild size="lg" className="w-full sm:w-auto">
              <Link href="/login">
                Get started free <ArrowRight className="h-4 w-4" />
              </Link>
            </Button>
            <Button asChild size="lg" variant="ghost" className="w-full sm:w-auto">
              <Link href="/login?next=/onboarding?tab=join">I have a workspace ID</Link>
            </Button>
          </div>
        </section>

        <section className="pb-24">
          <h2 className="text-center text-2xl font-semibold tracking-tight">
            Built around the problems teams actually have
          </h2>

          <div className="mt-12 grid gap-6 sm:grid-cols-2">
            {PROBLEMS.map(({ icon: Icon, problem, solution }) => (
              <div
                key={problem}
                className="rounded-xl border bg-card/70 p-6 shadow-sm backdrop-blur transition-shadow hover:shadow-md"
              >
                <div className="mb-4 inline-flex rounded-lg bg-primary/10 p-2.5">
                  <Icon className="h-5 w-5 text-primary" />
                </div>
                <h3 className="font-semibold">{problem}</h3>
                <p className="mt-2 text-sm leading-relaxed text-muted-foreground">{solution}</p>
              </div>
            ))}
          </div>
        </section>

        <section className="pb-28">
          <div className="rounded-2xl border bg-card/70 p-10 text-center shadow-sm backdrop-blur">
            <CalendarRange className="mx-auto h-8 w-8 text-primary" />
            <h2 className="mt-4 text-2xl font-semibold tracking-tight">
              Create a workspace, or join your team&apos;s
            </h2>
            <p className="mx-auto mt-3 max-w-xl text-muted-foreground">
              Create an account and you&apos;re one screen away — start fresh, or enter the
              workspace ID a teammate sent you.
            </p>
            <Button asChild size="lg" className="mt-8">
              <Link href="/login">
                Get started <ArrowRight className="h-4 w-4" />
              </Link>
            </Button>
          </div>
        </section>
      </main>

      <footer className="border-t py-8">
        <div className="mx-auto max-w-6xl px-6 text-center text-sm text-muted-foreground">
          Sprintly — open-source team management.
        </div>
      </footer>
    </div>
  );
}
