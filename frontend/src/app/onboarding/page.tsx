import { Suspense } from "react";
import type { Metadata } from "next";

import { OnboardingPanel } from "@/components/onboarding/onboarding-panel";
import { Skeleton } from "@/components/ui/misc";

export const metadata: Metadata = { title: "Get started" };

export default function OnboardingPage() {
  return (
    <main className="mesh-bg flex min-h-dvh items-center justify-center px-4 py-12">
      <Suspense fallback={<Skeleton className="h-[520px] w-full max-w-xl rounded-2xl" />}>
        <OnboardingPanel />
      </Suspense>
    </main>
  );
}
