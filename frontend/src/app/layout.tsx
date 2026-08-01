import type { Metadata, Viewport } from "next";
import { Toaster } from "sonner";

import { Providers } from "@/components/providers";
import "./globals.css";

export const metadata: Metadata = {
  title: {
    default: "Sprintly — one workspace for the whole team",
    template: "%s · Sprintly",
  },
  description:
    "Sprintly is an open-source team workspace: boards, docs, chat, time tracking and workload in one place — so work stops scattering across five tools.",
};

export const viewport: Viewport = {
  themeColor: [
    { media: "(prefers-color-scheme: light)", color: "#ffffff" },
    { media: "(prefers-color-scheme: dark)", color: "#0a0f1e" },
  ],
};

export default function RootLayout({ children }: { children: React.ReactNode }) {
  return (
    // suppressHydrationWarning: next-themes sets `class` on <html> before React
    // hydrates, which would otherwise be reported as a mismatch.
    <html lang="en" suppressHydrationWarning>
      <body>
        <Providers>{children}</Providers>
        <Toaster position="bottom-right" richColors closeButton />
      </body>
    </html>
  );
}
