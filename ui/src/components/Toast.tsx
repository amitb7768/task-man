// Singleton undo toast. Dark pill, bottom-center, 6s auto-dismiss.
// Imperative API: showToast(message, onUndo?) — callable from anywhere
// (TaskRow mutations, bulk actions, etc.) without prop-drilling.
//
// Mount <ToastHost /> once, near the root (App.tsx does this).
import { useEffect, useRef, useState } from "react";

interface ToastState {
  id: number;
  message: string;
  onUndo?: () => void;
}

type Listener = (state: ToastState) => void;

let listener: Listener | null = null;
let clearFn: (() => void) | null = null;
let nextId = 1;

export function showToast(message: string, onUndo?: () => void): void {
  listener?.({ id: nextId++, message, onUndo });
}

/** Hides the current toast (if any) without firing its undo callback. */
export function dismissToast(): void {
  clearFn?.();
}

const AUTO_DISMISS_MS = 6000;

export default function ToastHost() {
  const [toast, setToast] = useState<ToastState | null>(null);
  const timerRef = useRef<number | null>(null);

  useEffect(() => {
    listener = (state) => {
      if (timerRef.current !== null) window.clearTimeout(timerRef.current);
      setToast(state);
      timerRef.current = window.setTimeout(() => setToast(null), AUTO_DISMISS_MS);
    };
    clearFn = () => {
      if (timerRef.current !== null) window.clearTimeout(timerRef.current);
      setToast(null);
    };
    return () => {
      listener = null;
      clearFn = null;
      if (timerRef.current !== null) window.clearTimeout(timerRef.current);
    };
  }, []);

  if (!toast) return null;

  function dismiss() {
    if (timerRef.current !== null) window.clearTimeout(timerRef.current);
    setToast(null);
  }

  return (
    <div className="toast" role="status">
      <span className="toast-message">{toast.message}</span>
      {toast.onUndo && (
        <>
          <span className="toast-sep">·</span>
          <button
            type="button"
            className="toast-undo"
            onClick={() => {
              toast.onUndo?.();
              dismiss();
            }}
          >
            Undo
          </button>
        </>
      )}
    </div>
  );
}
