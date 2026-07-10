// Shared current/new/confirm form — used both by ForcedChangeScreen (mode
// "forced", locks the shell) and ChangePasswordPanel (mode "panel", the
// sidebar footer's settings-lite affordance, non-forced). Same fields, same
// min-length-8 validation, same POST /api/auth/change-password call
// (docs/AUTH_FEATURES.md "API surface" — the only mutating route allowed
// while mustChangePassword is set).
import { useState } from "react";
import type { FormEvent } from "react";
import { api, ApiError } from "../api";

function errMsg(e: unknown): string {
  return e instanceof ApiError ? e.message : "Something went wrong. Try again.";
}

const MIN_LENGTH = 8;

export interface ChangePasswordFormProps {
  mode: "forced" | "panel";
  onSuccess: () => void;
  onCancel?: () => void;
}

export default function ChangePasswordForm({ mode, onSuccess, onCancel }: ChangePasswordFormProps) {
  const [current, setCurrent] = useState("");
  const [next, setNext] = useState("");
  const [confirm, setConfirm] = useState("");
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);

  const tooShort = next.length > 0 && next.length < MIN_LENGTH;
  const mismatch = confirm.length > 0 && next !== confirm;
  const canSubmit = current.length > 0 && next.length >= MIN_LENGTH && next === confirm;

  async function handleSubmit(e: FormEvent) {
    e.preventDefault();
    if (busy || !canSubmit) return;
    setError(null);
    setBusy(true);
    try {
      await api.auth.changePassword(current, next);
      onSuccess();
    } catch (err) {
      setError(errMsg(err));
      setBusy(false);
    }
  }

  return (
    <form className="auth-form" onSubmit={handleSubmit}>
      <label className="auth-field">
        <span className="auth-field-label">Current password</span>
        <input
          type="password"
          autoFocus={mode === "forced"}
          autoComplete="current-password"
          required
          value={current}
          onChange={(e) => setCurrent(e.target.value)}
        />
      </label>
      <label className="auth-field">
        <span className="auth-field-label">New password</span>
        <input
          type="password"
          autoComplete="new-password"
          required
          minLength={MIN_LENGTH}
          value={next}
          onChange={(e) => setNext(e.target.value)}
        />
      </label>
      <label className="auth-field">
        <span className="auth-field-label">Confirm new password</span>
        <input
          type="password"
          autoComplete="new-password"
          required
          minLength={MIN_LENGTH}
          value={confirm}
          onChange={(e) => setConfirm(e.target.value)}
        />
      </label>

      {tooShort && <div className="auth-hint">At least {MIN_LENGTH} characters.</div>}
      {mismatch && <div className="auth-hint auth-hint-bad">Passwords don&rsquo;t match.</div>}
      {error && <div className="error auth-error">{error}</div>}

      <div className="auth-form-actions">
        {mode === "panel" && onCancel && (
          <button type="button" onClick={onCancel} disabled={busy}>
            Cancel
          </button>
        )}
        <button type="submit" className="primary auth-submit" disabled={busy || !canSubmit}>
          {busy ? "Saving…" : mode === "forced" ? "Set new password" : "Change password"}
        </button>
      </div>
    </form>
  );
}
