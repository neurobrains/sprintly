import { createServerClient, type CookieOptions } from "@supabase/ssr";
import { cookies } from "next/headers";

/** Shape `setAll` receives. Declared explicitly because the `cookies` option is
 *  a union of the current and deprecated method sets, which blocks inference. */
type CookieToSet = { name: string; value: string; options: CookieOptions };

/**
 * Supabase client for Server Components, Route Handlers and Server Actions.
 *
 * Server Components cannot write cookies, so the `setAll` below swallows the
 * error it throws. Token refresh still happens — the middleware in
 * `src/middleware.ts` writes the refreshed cookies on every request.
 */
export async function createClient() {
  const cookieStore = await cookies();

  return createServerClient(
    process.env.NEXT_PUBLIC_SUPABASE_URL!,
    process.env.NEXT_PUBLIC_SUPABASE_ANON_KEY!,
    {
      cookies: {
        getAll() {
          return cookieStore.getAll();
        },
        setAll(cookiesToSet: CookieToSet[]) {
          try {
            cookiesToSet.forEach(({ name, value, options }) =>
              cookieStore.set(name, value, options),
            );
          } catch {
            // Called from a Server Component — middleware handles the refresh.
          }
        },
      },
    },
  );
}
