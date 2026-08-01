"use client";

import { createBrowserClient } from "@supabase/ssr";

/**
 * Browser-side Supabase client. Sessions are persisted in cookies (not
 * localStorage) so that Server Components and middleware see the same session.
 */
export function createClient() {
  return createBrowserClient(
    process.env.NEXT_PUBLIC_SUPABASE_URL!,
    process.env.NEXT_PUBLIC_SUPABASE_ANON_KEY!,
  );
}

let browserClient: ReturnType<typeof createBrowserClient> | undefined;

/** Singleton accessor — repeated createClient() calls would each open their own
 *  auth listener and refresh timer. */
export function supabase() {
  if (!browserClient) browserClient = createClient();
  return browserClient;
}
