import { createServerClient, type CookieOptions } from "@supabase/ssr";
import { NextResponse, type NextRequest } from "next/server";

/** Shape `setAll` receives — annotated because the `cookies` option is a union
 *  of the current and deprecated method sets, which blocks inference. */
type CookieToSet = { name: string; value: string; options: CookieOptions };

/** Routes reachable without a session. */
const PUBLIC_PATHS = ["/", "/login", "/auth/callback", "/auth/auth-code-error"];

function isPublic(pathname: string) {
  return PUBLIC_PATHS.some((p) => pathname === p || pathname.startsWith(`${p}/`));
}

/**
 * Refreshes the Supabase session on every request and gates private routes.
 *
 * The cookie juggling below is required: `supabase.auth.getUser()` may rotate
 * the refresh token, and those new cookies have to be written onto the response
 * we actually return, or the next request will use a token that has been spent.
 */
export async function middleware(request: NextRequest) {
  let response = NextResponse.next({ request });

  const supabase = createServerClient(
    process.env.NEXT_PUBLIC_SUPABASE_URL!,
    process.env.NEXT_PUBLIC_SUPABASE_ANON_KEY!,
    {
      cookies: {
        getAll() {
          return request.cookies.getAll();
        },
        setAll(cookiesToSet: CookieToSet[]) {
          cookiesToSet.forEach(({ name, value }) => request.cookies.set(name, value));
          response = NextResponse.next({ request });
          cookiesToSet.forEach(({ name, value, options }) =>
            response.cookies.set(name, value, options),
          );
        },
      },
    },
  );

  // getUser() revalidates against the auth server; getSession() would trust
  // whatever the cookie claims.
  const {
    data: { user },
  } = await supabase.auth.getUser();

  const { pathname } = request.nextUrl;

  if (!user && !isPublic(pathname)) {
    const login = request.nextUrl.clone();
    login.pathname = "/login";
    // Preserve where they were headed so the callback can return them there.
    login.searchParams.set("next", pathname);
    return NextResponse.redirect(login);
  }

  if (user && (pathname === "/login" || pathname === "/")) {
    const home = request.nextUrl.clone();
    home.pathname = "/app";
    home.search = "";
    return NextResponse.redirect(home);
  }

  return response;
}

export const config = {
  matcher: [
    // Everything except Next internals and static assets.
    "/((?!_next/static|_next/image|favicon.ico|.*\\.(?:svg|png|jpg|jpeg|gif|webp|ico)$).*)",
  ],
};
