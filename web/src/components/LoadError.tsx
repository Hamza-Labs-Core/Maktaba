// The one error state for a screen whose data did not load. It says what went
// wrong in the app's words (lib/errorText: never the server's title, detail,
// JSON or a path) and offers what fits the failure:
//
//   - Try again, for anything that may pass on a second go;
//   - Sign in again (to /login, coming back here after), when the sign-in ended;
//   - Back, when the thing is gone or not this person's to see;
//   - and, when the network was the problem, it tries again by itself once
//     the browser is back online.
import { useEffect, useRef, type ReactNode } from "react";
import { Link, useLocation, useNavigate } from "react-router-dom";
import { Button } from "@ds/components/Button/Button";
import { ErrorState } from "@ds/components/ErrorState/ErrorState";
import { errorStateKind } from "../lib/errorKind";
import { errorText, userErrorKind } from "../lib/errorText";
import { useI18n } from "../lib/i18n";

export interface LoadErrorProps {
  /** what the failed call threw */
  error: unknown;
  /** loads the screen again; without it there is no Try again */
  onRetry?: () => void;
  /** the page Back goes to (default: the previous page) */
  backTo?: string;
  /** Back's label (default: Go back) */
  backLabel?: string;
  /** a title other than "Something went wrong." */
  title?: ReactNode;
  className?: string;
}

export function LoadError({ error, onRetry, backTo, backLabel, title, className }: LoadErrorProps) {
  const { t } = useI18n();
  const navigate = useNavigate();
  const location = useLocation();
  const kind = userErrorKind(error);

  // a load that failed for want of a network loads again once it is back
  const retry = useRef(onRetry);
  useEffect(() => {
    retry.current = onRetry;
  });
  useEffect(() => {
    if (kind !== "offline" && kind !== "unreachable") return;
    const again = () => retry.current?.();
    window.addEventListener("online", again);
    return () => window.removeEventListener("online", again);
  }, [kind]);

  const actions: ReactNode[] = [];
  if (kind === "signedOut") {
    actions.push(
      <Link
        key="signin"
        className="mkt-btn mkt-btn--primary"
        to="/login"
        state={{ from: location.pathname + location.search }}
      >
        {t("error.signIn")}
      </Link>
    );
  } else if (onRetry && kind !== "notFound" && kind !== "noAccess") {
    actions.push(
      <Button key="retry" onClick={onRetry}>
        {t("common.retry")}
      </Button>
    );
  }
  if (kind === "notFound" || kind === "noAccess") {
    const label = backLabel ?? t("error.back");
    actions.push(
      backTo ? (
        <Link key="back" className="mkt-btn mkt-btn--secondary" to={backTo}>
          {label}
        </Link>
      ) : (
        <Button key="back" variant="secondary" onClick={() => navigate(-1)}>
          {label}
        </Button>
      )
    );
  }

  return (
    <ErrorState
      className={className}
      kind={kind === "offline" ? "network" : errorStateKind(kind)}
      title={title ?? t("common.error")}
      description={
        // a page that is gone or not theirs says so in the app's words (a 403's
        // reason does not say whether the thing exists)
        kind === "notFound" || kind === "noAccess" ? t(`error.kind.${kind}`) : errorText(error, t)
      }
      action={actions.length > 0 ? <div className="mkt-load-error__actions">{actions}</div> : null}
    />
  );
}
