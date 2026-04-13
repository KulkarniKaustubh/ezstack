import React from "react";

interface State {
  error: Error | null;
}

/**
 * Top-level error boundary so a render-time exception in any subtree shows
 * a recoverable fallback instead of a blank window. Reload button restarts
 * the React tree without forcing a full Tauri webview reload.
 */
export class ErrorBoundary extends React.Component<
  { children: React.ReactNode },
  State
> {
  state: State = { error: null };

  static getDerivedStateFromError(error: Error): State {
    return { error };
  }

  componentDidCatch(error: Error, info: React.ErrorInfo) {
    // Surface to console; Tauri devtools / log files will pick this up.
    console.error("ErrorBoundary caught:", error, info.componentStack);
  }

  reset = () => this.setState({ error: null });

  render() {
    if (!this.state.error) return this.props.children;
    return (
      <div className="flex flex-col items-center justify-center h-screen p-8 gap-4 bg-background text-foreground">
        <div className="max-w-md w-full rounded-md border border-destructive/40 bg-destructive/5 p-4">
          <div className="text-sm font-semibold text-destructive mb-2">
            Something went wrong
          </div>
          <pre className="text-xs whitespace-pre-wrap font-mono text-muted-foreground max-h-64 overflow-auto">
            {this.state.error.message}
            {"\n\n"}
            {this.state.error.stack}
          </pre>
        </div>
        <div className="flex gap-2">
          <button
            type="button"
            onClick={this.reset}
            className="px-3 py-1.5 text-sm rounded-md border hover:bg-accent"
          >
            Try again
          </button>
          <button
            type="button"
            onClick={() => window.location.reload()}
            className="px-3 py-1.5 text-sm rounded-md bg-primary text-primary-foreground hover:opacity-90"
          >
            Reload
          </button>
        </div>
      </div>
    );
  }
}
