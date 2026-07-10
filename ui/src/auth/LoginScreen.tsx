// Login screen (docs/AUTH_FEATURES.md "UI changes": "Boot: fetch
// /api/auth/me; 401 -> login screen (card, email+password, error state)").
// Centered card, serif "taskman" wordmark, native email/password inputs,
// Enter submits (plain <form onSubmit>, no keydown plumbing needed).
import { useState } from "react";
import type { FormEvent } from "react";
import type { AuthUser } from "../api";
import { api, ApiError } from "../api";

function errMsg(e: unknown): string {
  return e instanceof ApiError ? e.message : "Couldn't reach the server. Try again.";
}

export default function LoginScreen({ onAuthenticated }: { onAuthenticated: (user: AuthUser) => void }) {
  const [email, setEmail] = useState("");
  const [password, setPassword] = useState("");
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);

  async function handleSubmit(e: FormEvent) {
    e.preventDefault();
    if (busy) return;
    setError(null);
    setBusy(true);
    try {
      const { user } = await api.auth.login(email.trim(), password);
      onAuthenticated(user);
    } catch (err) {
      setError(errMsg(err));
      setBusy(false);
    }
  }

  return (
    <div className="auth-screen">
      <div className="auth-card">
        <div className="auth-brand">
          <span className="auth-logo" aria-hidden="true">
            t
          </span>
          <h1 className="auth-wordmark">taskman</h1>
        </div>
        <div className="eyebrow auth-center">Sign in to continue</div>

        {error && <div className="error auth-error">{error}</div>}

        <form className="auth-form" onSubmit={handleSubmit}>
          <label className="auth-field">
            <span className="auth-field-label">Email</span>
            <input
              type="email"
              autoFocus
              autoComplete="username"
              required
              value={email}
              onChange={(e) => setEmail(e.target.value)}
            />
          </label>
          <label className="auth-field">
            <span className="auth-field-label">Password</span>
            <input
              type="password"
              autoComplete="current-password"
              required
              value={password}
              onChange={(e) => setPassword(e.target.value)}
            />
          </label>
          <button type="submit" className="primary auth-submit" disabled={busy || !email || !password}>
            {busy ? "Signing in…" : "Sign in"}
          </button>
        </form>
      </div>
    </div>
  );
}
