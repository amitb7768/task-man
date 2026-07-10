// Non-forced "Change password" affordance (docs/AUTH_FEATURES.md "UI
// changes": "Sidebar footer... Settings-lite: a 'Change password' affordance
// in the footer identity block (opens the same change-password card as a
// panel, non-forced mode)"). Same ChangePasswordForm as the forced screen,
// presented as a dismissible centered overlay instead of locking the shell.
import { useEffect } from "react";
import ChangePasswordForm from "./ChangePasswordForm";

export default function ChangePasswordPanel({ onClose }: { onClose: () => void }) {
  useEffect(() => {
    function onKey(e: KeyboardEvent) {
      if (e.key === "Escape") onClose();
    }
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [onClose]);

  return (
    <>
      <div className="auth-panel-backdrop" onClick={onClose} />
      <div className="auth-panel" role="dialog" aria-modal="true" aria-label="Change password">
        <div className="auth-card auth-card-panel">
          <div className="auth-panel-head">
            <h2 className="auth-title">Change password</h2>
            <button type="button" className="auth-panel-close" title="Close" onClick={onClose}>
              <svg width="17" height="17" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.9" strokeLinecap="round">
                <line x1="6" y1="6" x2="18" y2="18" />
                <line x1="18" y1="6" x2="6" y2="18" />
              </svg>
            </button>
          </div>
          <p className="auth-copy">
            Choose a new password. You&rsquo;ll stay signed in on this device; every other session is signed out.
          </p>
          <ChangePasswordForm mode="panel" onSuccess={onClose} onCancel={onClose} />
        </div>
      </div>
    </>
  );
}
