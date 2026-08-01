import { Suspense } from "react";
import type { Metadata } from "next";

import { LoginCard } from "@/components/auth/login-card";
import { Skeleton } from "@/components/ui/misc";

export const metadata: Metadata = { title: "Sign in" };

export default function LoginPage() {
  return (
    <main className="mesh-bg flex min-h-dvh items-center justify-center px-4 py-12">
      <Suspense fallback={<Skeleton className="h-[420px] w-full max-w-md rounded-2xl" />}>
        <LoginCard />
      </Suspense>
    </main>
  );
}
