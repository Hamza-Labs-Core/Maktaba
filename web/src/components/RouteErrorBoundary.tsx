// A page that throws while rendering shows an error in its place instead of
// taking the whole app down with it (React unmounts the tree on an uncaught
// render error: the menu, the Back button and every other page stop working
// until a reload). The boundary clears itself when the route changes, so
// leaving the broken page brings the app back.
import { Component, type ReactNode } from "react";
import { Button } from "@ds/components/Button/Button";
import { useI18n } from "../lib/i18n";

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
  }

  render() {
    return this.state.error ? <PageError error={this.state.error} /> : this.props.children;
  }
}

function PageError({ error }: { error: Error }) {
  const { t } = useI18n();
  return (
    <section className="mkt-page" role="alert">
      <h1>{t("common.error")}</h1>
      <p>{t("common.pageError")}</p>
      <p className="mkt-muted">
        <code>{error.message}</code>
      </p>
      <Button variant="secondary" onClick={() => window.location.reload()}>
        {t("common.reloadPage")}
      </Button>
    </section>
  );
}
