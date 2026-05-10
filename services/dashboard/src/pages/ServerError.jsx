import ErrorPage from '../components/ErrorPage';

// ServerError — shown for runtime API failures (500, 503, or unreachable
// API). For *planned* maintenance the static public/maintenance.html
// served by nginx takes over instead — it doesn't depend on the SPA
// bundle being reachable. See the comment in App.jsx's error boundary.
//
// Both actions use full-page navigation rather than useNavigate(): this
// component is rendered by AppErrorBoundary, which can fire from an
// error originating *above* the <Router> (inside ThemeProvider or
// QueryClientProvider). useNavigate() throws outside Router context, so
// a router-dependent action would cascade the boundary into a second
// crash. A hard reload also gives the failed providers a fresh start.
export default function ServerError({ code = '500', reference }) {
  return (
    <ErrorPage
      code={code}
      title="Something went wrong on our end"
      description="We hit an unexpected error. The team has been notified — please try again in a moment. If it keeps happening, contact support."
      actions={[
        { label: 'Try again', primary: true, onClick: () => window.location.reload() },
        { label: 'Go to overview',           onClick: () => { window.location.href = '/'; } },
      ]}
      reference={reference}
    />
  );
}
