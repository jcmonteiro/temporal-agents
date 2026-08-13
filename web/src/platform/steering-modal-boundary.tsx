import {
  Component,
  useEffect,
  useRef,
  type CSSProperties,
  type ErrorInfo,
  type ReactNode,
} from "react";

interface SteeringModalBoundaryProps {
  resetKey: string;
  onClose(): void;
  onReload(): void;
  children: ReactNode;
}

interface SteeringModalBoundaryState {
  failure: Error | null;
}

/** Contains failures from the separately loaded modal chunk. */
export class SteeringModalErrorBoundary extends Component<
  SteeringModalBoundaryProps,
  SteeringModalBoundaryState
> {
  state: SteeringModalBoundaryState = { failure: null };

  static getDerivedStateFromError(error: unknown): SteeringModalBoundaryState {
    return { failure: error instanceof Error ? error : new Error(String(error)) };
  }

  componentDidCatch(error: Error, info: ErrorInfo): void {
    console.error("The steering modal failed", error, info.componentStack);
  }

  componentDidUpdate(previous: SteeringModalBoundaryProps): void {
    if (previous.resetKey !== this.props.resetKey && this.state.failure !== null) {
      this.setState({ failure: null });
    }
  }

  render(): ReactNode {
    if (this.state.failure === null) return this.props.children;
    return (
      <SteeringModalLoadFailure
        onClose={this.props.onClose}
        onReload={this.props.onReload}
      />
    );
  }
}

// Keep the recovery surface styled without the CSS that belongs to the failed chunk.
const backdropStyle: CSSProperties = {
  position: "fixed",
  zIndex: 100,
  inset: 0,
  display: "grid",
  padding: "var(--space-5)",
  placeItems: "center",
  background: "color-mix(in srgb, #06080d 72%, transparent)",
  backdropFilter: "blur(8px)",
};

const surfaceStyle: CSSProperties = {
  display: "grid",
  width: "min(520px, 100%)",
  gap: "var(--space-4)",
  padding: "var(--space-6)",
  border: "1px solid var(--color-border-strong)",
  borderRadius: "var(--radius-md)",
  color: "var(--color-text)",
  background: "var(--color-surface)",
  boxShadow: "var(--shadow-lg)",
};

const actionsStyle: CSSProperties = {
  display: "flex",
  flexWrap: "wrap",
  gap: "var(--space-3)",
};

const buttonStyle: CSSProperties = {
  minHeight: "var(--control-height)",
  padding: "8px 12px",
  border: "1px solid var(--color-border-strong)",
  borderRadius: "var(--radius-sm)",
  color: "var(--color-text)",
  background: "var(--color-surface-2)",
};

export function SteeringModalLoading({ onClose }: { onClose(): void }): ReactNode {
  return (
    <div style={backdropStyle}>
      <section
        aria-labelledby="steering-loading-title"
        aria-modal="true"
        role="dialog"
        style={surfaceStyle}
      >
        <div role="status" aria-live="polite">
          <h2 id="steering-loading-title" style={{ margin: 0 }}>Opening steering…</h2>
          <p style={{ marginBottom: 0, color: "var(--color-text-muted)" }}>
            Loading the guidance controls.
          </p>
        </div>
        <div style={actionsStyle}>
          <button type="button" onClick={onClose} style={buttonStyle}>
            Close steering
          </button>
        </div>
      </section>
    </div>
  );
}

function SteeringModalLoadFailure({
  onClose,
  onReload,
}: {
  onClose(): void;
  onReload(): void;
}): ReactNode {
  const closeRef = useRef<HTMLButtonElement>(null);
  const returnFocusRef = useRef<HTMLElement | null>(
    typeof document !== "undefined" && document.activeElement instanceof HTMLElement
      ? document.activeElement
      : null,
  );

  useEffect(() => {
    closeRef.current?.focus();
    return () => returnFocusRef.current?.focus();
  }, []);

  return (
    <div style={backdropStyle}>
      <section
        aria-labelledby="steering-load-failure-title"
        aria-modal="true"
        role="dialog"
        style={surfaceStyle}
      >
        <div role="alert">
          <h2 id="steering-load-failure-title" style={{ margin: 0 }}>
            Steering could not be opened
          </h2>
          <p style={{ marginBottom: 0, color: "var(--color-text-muted)" }}>
            The guidance controls could not be loaded. Close this message or reload the
            application.
          </p>
          <div style={{ ...actionsStyle, marginTop: "var(--space-4)" }}>
            <button ref={closeRef} type="button" onClick={onClose} style={buttonStyle}>
              Close steering
            </button>
            <button type="button" onClick={onReload} style={buttonStyle}>
              Reload application
            </button>
          </div>
        </div>
      </section>
    </div>
  );
}
