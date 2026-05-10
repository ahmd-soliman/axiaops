import { Component } from 'react';
import ServerError from '../pages/ServerError';

// AppErrorBoundary — last-resort net for uncaught render errors. Any
// component that throws inside the tree below this boundary renders the
// generic <ServerError /> fallback instead of an empty white screen.
//
// React error boundaries must be class components (the hook equivalent
// doesn't exist as of React 18). We do not surface stack traces or
// component names to the user — those go to the console for developers
// and to the logging pipeline below.
export default class AppErrorBoundary extends Component {
  constructor(props) {
    super(props);
    this.state = { hasError: false };
  }

  static getDerivedStateFromError() {
    // Trigger re-render with the fallback UI. We don't keep the error
    // object in state — the user-facing copy is generic on purpose.
    return { hasError: true };
  }

  componentDidCatch(error, info) {
    // Forward to console so the error remains debuggable in dev tools.
    // A future hook to a logging service (Sentry, Honeybadger, etc.)
    // belongs here — keep the user-facing fallback unchanged.
    console.error('Uncaught render error', error, info);
  }

  // Without this, a single transient render error latches the fallback
  // for the rest of the session — every subsequent navigation keeps
  // rendering the 500 page even though the offending component is gone.
  // App.jsx passes `resetKey={location.pathname}`; a route change clears
  // the latch so the new route gets a fresh attempt at rendering.
  componentDidUpdate(prevProps) {
    if (this.state.hasError && prevProps.resetKey !== this.props.resetKey) {
      this.setState({ hasError: false });
    }
  }

  render() {
    if (this.state.hasError) {
      return <ServerError code="500" />;
    }
    return this.props.children;
  }
}
