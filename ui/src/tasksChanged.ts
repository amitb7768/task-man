// Cross-cutting "something changed" signal (moved out of App.tsx in v10 so
// components/TagFilter.tsx can subscribe without importing App). TaskRow and
// TaskComposer call notifyTasksChanged() after every mutation/create so the
// sidebar Attention badge (and the tag filter's known-tags list) stay fresh
// without prop-drilling a refresh callback through every view. See
// docs/DESIGN_V2_UI.md "Attention badge".
import { useEffect } from "react";

type ChangeListener = () => void;
const changeListeners = new Set<ChangeListener>();

export function notifyTasksChanged(): void {
  changeListeners.forEach((l) => l());
}

/** Subscribe `cb` for the component's lifetime; pass a stable callback. */
export function useTasksChangedSubscription(cb: ChangeListener) {
  useEffect(() => {
    changeListeners.add(cb);
    return () => {
      changeListeners.delete(cb);
    };
  }, [cb]);
}
