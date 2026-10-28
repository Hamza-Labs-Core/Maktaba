// A page that throws while rendering shows an error in its place instead of
// taking the whole app down with it (React unmounts the tree on an uncaught
// render error: the menu, the Back button and every other page stop working
// until a reload). The boundary clears itself when the route changes, so
// leaving the broken page brings the app back. App wraps the whole app in one
// too (a fixed resetKey), so a crash outside a page (the shell, a provider)
// shows the same screen instead of a blank window.
//
// The screen never shows the error's message or stack (they can name a title,
// a path or a value from the server): they go to the console only.
import { Component, type ReactNode } from "react";
import { Button } from "@ds/components/Button/Button";
import { useI18n } from "../lib/i18n";
import { forgetAnswers } from "../lib/pageCache";

interface Props {
  /** the current route: a new value clears the error */
  resetKey: string;
  children: ReactNode;
}

interface State {
  error: Error | null;
  key: string;
}

export class RouteErrorBoundary extends Component<Props, State> {
  state: State = { error: null, key: this.props.resetKey };

  static getDerivedStateFromError(error: Error): Partial<State> {
    return { error };
  }

  static getDerivedStateFromProps(props: Props, state: State): Partial<State> | null {
    return props.resetKey !== state.key ? { error: null, key: props.resetKey } : null;
  }

  componentDidCatch(error: Error) {
    console.error("[page] render failed", error);
    // the answer the page failed on may be a kept one: a return (or Retry)
    // reads afresh instead of failing on it again
    forgetAnswers();
  }

  render() {
    return this.state.error ? (
      <PageError onRetry={() => this.setState({ error: null })} />
    ) : (
      this.props.children
    );
  }
}

function PageError({ onRetry }: { onRetry: () => void }) {
  const { t } = useI18n();
  return (
    <section className="mkt-page" role="alert" data-testid="page-error">
      <h1>{t("common.error")}</h1>
      <p>{t("common.pageError")}</p>
      <div className="mkt-load-error__actions mkt-load-error__actions--start">
        <Button onClick={onRetry}>{t("common.retry")}</Button>
        <Button variant="secondary" onClick={() => window.location.reload()}>
          {t("common.reloadPage")}
        </Button>
      </div>
    </section>
  );
}
