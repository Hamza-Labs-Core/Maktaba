// Whether to offer admin-only controls. Outside a session context (the queue
// pages are tested and embedded without one) the answer is yes and the API,
// which enforces admin on every mutation, decides.
import { useOptionalAuth } from "../../lib/auth";

export function useIsAdmin(): boolean {
  const auth = useOptionalAuth();
  return auth ? !!auth.user?.is_admin : true;
}
