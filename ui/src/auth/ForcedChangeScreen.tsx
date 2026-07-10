// Forced change-password screen (docs/AUTH_FEATURES.md decision #6: "UI
// locks to change-password screen until cleared"). Renders in place of the
// entire app shell — AuthProvider only reaches this branch while
// mustChangePassword is set, so there's no sidebar/nav to escape through.
import type { AuthUser } from "../api";
import ChangePasswordForm from "./ChangePasswordForm";

export default function ForcedChangeScreen({
  user,
  onChanged,
  onLogout,
}: {
  user: AuthUser;
  onChanged: (user: AuthUser) => void;
  /** Signs out and returns to the login screen — the escape hatch for a
   * forced-change session that doesn't want to (or can't) set a new
   * password right now. */
  onLogout: () => void;
}) {
  return (
    <div className="auth-screen">
      <div className="auth-card">
        <div className="auth-brand">
          <span className="auth-logo" aria-hidden="true">
            t
          </span>
          <h1 className="auth-wordmark">taskman</h1>
        </div>
        <h2 className="auth-title">Set a new password</h2>
        <p className="auth-copy">
          Your account needs a new password before you can continue — either this is your first sign-in, or an
          admin just reset it. Choose something only you know.
        </p>
        <ChangePasswordForm mode="forced" onSuccess={() => onChanged({ ...user, mustChangePassword: false })} />
        <div className="auth-signout-row">
          <button type="button" className="link" onClick={onLogout}>
            Sign out instead
          </button>
        </div>
      </div>
    </div>
  );
}
