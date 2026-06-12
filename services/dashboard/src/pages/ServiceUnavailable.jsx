import ErrorPage from '../components/ErrorPage';

// ServiceUnavailable — 503-specific page for runtime "API reachable but
// degraded" states. Distinct from ServerError (500) because the user
// intent differs: 503 = "try again in a moment, this isn't your fault,
// nothing is broken", 500 = "we hit an unexpected error". Distinct from
// the static public/maintenance.html, which is served by the edge proxy
// when the SPA bundle itself can't be reached.
//
// Wired in App.jsx: api/client.js dispatches SERVICE_UNAVAILABLE_EVENT
// on every 503 response, and the App-level listener navigates here.
// The error boundary uses ServerError (500) for uncaught render errors.
export default function ServiceUnavailable({ reference }) {
  return (
    <ErrorPage
      code="503"
      title="We'll be back shortly"
      description="AxiaOps is temporarily unavailable. No action is needed on your side — your data is safe. Try again in a moment."
      actions={[
        { label: 'Try again', primary: true, onClick: () => window.location.reload() },
        { label: 'Go to overview',           to: '/' },
      ]}
      reference={reference}
    />
  );
}
