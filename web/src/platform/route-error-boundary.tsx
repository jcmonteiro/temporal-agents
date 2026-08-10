import { Component, type ErrorInfo, type ReactNode } from "react";

/**
 * What the operator sees when a page fails outright.
 *
 * A render that throws unmounts the whole tree in React, so without a boundary
 * one broken page leaves a blank document with no way back. The boundary is
 * placed around the routed page only: the shell — the top bar and the
 * navigation — stays alive, so the operator can leave the failure behind
 * instead of reloading the hub.
 *
 * `resetKey` is the address of the current route. When it changes the boundary
 * forgets the failure, because the failure belonged to the page that is gone.
 * Without that, navigating away from a broken page would keep showing its
 * error.
 */
interface Props {
  resetKey: string;
  children: ReactNode;
}

interface State {
  failure: Error | null;
}

export class RouteErrorBoundary extends Component<Props, State> {
  state: State = { failure: null };

  static getDerivedStateFromError(error: unknown): State {
    return { failure: error instanceof Error ? error : new Error(String(error)) };
  }

  componentDidCatch(error: Error, info: ErrorInfo): void {
    // The operator gets the summary below; whoever debugs it gets the stack.
    console.error("The page failed", error, info.componentStack);
  }

  componentDidUpdate(previous: Props): void {
    if (previous.resetKey !== this.props.resetKey && this.state.failure !== null) {
      this.forget();
    }
  }

  private forget = (): void => {
    this.setState({ failure: null });
  };

  render(): ReactNode {
    const { failure } = this.state;
    if (failure === null) return this.props.children;
    return (
      <main
        role="alert"
        style={{
          flex: 1,
          display: "flex",
          flexDirection: "column",
          gap: "var(--space-3)",
          alignItems: "flex-start",
          padding: "var(--space-5)",
        }}
      >
        <h1 style={{ margin: 0, fontSize: "var(--font-size-xl)", fontWeight: 600 }}>
          This page could not be shown
        </h1>
        <p style={{ margin: 0, color: "var(--color-text-muted)" }}>
          Something in the page failed, so the hub stopped drawing it. The rest
          of the hub still works: try again, or go somewhere else.
        </p>
        <code
          style={{
            fontSize: "var(--font-size-sm)",
            color: "var(--color-text-subtle)",
          }}
        >
          {failure.message}
        </code>
        <button
          type="button"
          onClick={this.forget}
          style={{
            padding: "8px 12px",
            borderRadius: "var(--radius-sm)",
            border: "1px solid var(--color-border-strong)",
            background: "var(--color-surface-2)",
            color: "var(--color-text)",
            fontSize: "var(--font-size-sm)",
          }}
        >
          Try again
        </button>
      </main>
    );
  }
}
