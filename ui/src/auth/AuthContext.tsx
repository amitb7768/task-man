// Auth/session context (docs/AUTH_FEATURES.md "UI changes" + "API surface").
//
// Boot flow: call GET /api/auth/me. Any failure — 401 (no/expired session),
// 403, or a 404/network error (today: the OLD backend has no /api/auth/me
// route at all, since Wave A lands the endpoint separately) — is treated as
// "logged out" so boot always resolves to a screen instead of hanging or
// crashing; see this build's report for the old-backend graceful-fallback
// note. Success + mustChangePassword -> ForcedChangeScreen, which locks the
// whole shell (nothing else renders) until the password is changed. Success
// -> children render with the user available via useAuth().
//
// A global 401 anywhere in the app (session expired/invalidated mid-use —
// e.g. an admin reset or disable from another tab) routes back through the
// same anonymous state via api.ts's onUnauthorized subscription — "401
// mid-session (expired) -> redirect to login preserving nothing".
import { createContext, useCallback, useContext, useEffect, useState } from "react";
import type { ReactNode } from "react";
import type { AuthUser } from "../api";
import { api, onUnauthorized } from "../api";
import LoginScreen from "./LoginScreen";
import ForcedChangeScreen from "./ForcedChangeScreen";
import "../styles/auth.css";

interface AuthContextValue {
  user: AuthUser;
  /** Clears the session (best-effort server call) and returns to the login screen. */
  logout: () => void;
  /** Re-runs the /me boot check — used sparingly; most state changes update the context locally. */
  refresh: () => void;
}

const AuthContext = createContext<AuthContextValue | null>(null);

export function useAuth(): AuthContextValue {
  const ctx = useContext(AuthContext);
  if (!ctx) throw new Error("useAuth() called outside <AuthProvider>");
  return ctx;
}

type BootState = "loading" | "anonymous" | "must-change" | "authenticated";

export function AuthProvider({ children }: { children: ReactNode }) {
  const [state, setState] = useState<BootState>("loading");
  const [user, setUser] = useState<AuthUser | null>(null);

  const bootstrap = useCallback(() => {
    setState("loading");
    api.auth
      .me()
      .then(({ user: u }) => {
        setUser(u);
        setState(u.mustChangePassword ? "must-change" : "authenticated");
      })
      .catch(() => {
        setUser(null);
        setState("anonymous");
      });
  }, []);

  useEffect(() => {
    bootstrap();
  }, [bootstrap]);

  useEffect(() => onUnauthorized(() => {
    setUser(null);
    setState("anonymous");
  }), []);

  function handleAuthenticated(u: AuthUser) {
    setUser(u);
    setState(u.mustChangePassword ? "must-change" : "authenticated");
  }

  function handlePasswordChanged(u: AuthUser) {
    setUser(u);
    setState("authenticated");
  }

  function logout() {
    // Best-effort — even if the network call fails, the UI drops local
    // session state and returns to the login screen regardless.
    api.auth.logout().catch(() => {});
    setUser(null);
    setState("anonymous");
  }

  if (state === "loading") {
    // Deliberately bare — this only shows for the duration of one fetch on
    // boot; a full loading-card would flash and add more noise than signal.
    return <div className="auth-screen" aria-hidden="true" />;
  }

  if (state === "anonymous") {
    return <LoginScreen onAuthenticated={handleAuthenticated} />;
  }

  if (state === "must-change" && user) {
    // ForcedChangeScreen renders outside <AuthContext.Provider> (the whole
    // point is to lock the shell with nothing else reachable), so it can't
    // call useAuth().logout() itself — pass the same logout function down
    // directly instead.
    return <ForcedChangeScreen user={user} onChanged={handlePasswordChanged} onLogout={logout} />;
  }

  if (!user) return null; // unreachable: "authenticated" is only ever set alongside user

  return <AuthContext.Provider value={{ user, logout, refresh: bootstrap }}>{children}</AuthContext.Provider>;
}
