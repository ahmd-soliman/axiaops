import { useEffect, useRef } from 'react';
import { Link } from 'react-router-dom';
import { useTheme } from '../theme/ThemeContext';

// ErrorPage — reusable full-screen layout for HTTP-style failure states
// (404, 500, 503, etc.) and uncaught client render errors.
//
// Props:
//   code        — small label rendered above the heading ("404", …).
//   title       — human-language heading. No HTTP jargon ("This page isn't
//                 here", not "404 Not Found").
//   description — 1–2 sentences explaining what happened and, if useful,
//                 what to do next.
//   actions     — array of { label, onClick, to, primary }. An action with a
//                 `to` renders as a real <Link> (issue #130: in-app
//                 destinations open in a new tab on middle/Ctrl-click);
//                 otherwise it's a <button> driven by `onClick` (e.g. "Go
//                 back" → history). Primary renders as the brand-coloured
//                 solid button; secondaries as outlined.
//   reference   — optional small monospaced support reference. Show only
//                 when there's something useful to quote (request id,
//                 trace id). Don't fabricate one.
//   embedded    — true when this page is rendered inside <AppShell />.
//                 Drops the logo header (AppShell already shows one) and
//                 the min-height: 100vh wrapper so the page sits inside
//                 the shell's content area. Default false for stand-alone
//                 use from the error boundary (which fires before
//                 AppShell mounts) and from full-screen contexts.
export default function ErrorPage({ code, title, description, actions = [], reference, embedded = false }) {
  const { isDark } = useTheme();
  const headingRef = useRef(null);

  // Focus the heading on mount so screen readers announce the error state
  // and keyboard users start tab-order at the meaningful content. The
  // heading carries tabIndex={-1} so it accepts focus without joining
  // the natural tab sequence.
  useEffect(() => {
    headingRef.current?.focus();
  }, []);

  // When `embedded`, this renders inside AppShell's existing <main> — a
  // nested role="main" landmark is invalid ARIA and confuses screen
  // readers, so use a plain region wrapper. When standalone (boundary
  // fallback, full-screen error pages), this IS the main landmark.
  const Wrapper = embedded ? 'div' : 'main';
  return (
    <Wrapper
      style={{
        minHeight: embedded ? '60vh' : '100vh',
        backgroundColor: 'var(--color-bg)',
        display: 'flex',
        flexDirection: 'column',
        alignItems: 'stretch',
      }}
    >
      {!embedded && (
        <header style={{ padding: '20px 24px', display: 'flex', alignItems: 'center' }}>
          <img
            src={isDark ? '/axiaops-logo-dark.svg' : '/axiaops-logo.svg'}
            alt="AxiaOps"
            style={{ height: 36, width: 'auto', display: 'block' }}
          />
        </header>
      )}

      <div
        style={{
          flex: 1,
          display: 'flex',
          alignItems: 'center',
          justifyContent: 'center',
          padding: '24px',
        }}
      >
        <div style={{ maxWidth: 480, width: '100%', textAlign: 'center' }}>
          {code && (
            <span
              style={{
                display: 'block',
                fontSize: 12,
                fontWeight: 700,
                color: 'var(--color-text-muted)',
                letterSpacing: 1.5,
                textTransform: 'uppercase',
                marginBottom: 16,
              }}
            >
              Error {code}
            </span>
          )}

          <h1
            ref={headingRef}
            tabIndex={-1}
            style={{
              fontSize: 28,
              fontWeight: 800,
              color: 'var(--color-text)',
              letterSpacing: -0.5,
              margin: '0 0 12px',
              outline: 'none',
            }}
          >
            {title}
          </h1>

          {description && (
            <p
              style={{
                fontSize: 15,
                lineHeight: '24px',
                color: 'var(--color-text-mid)',
                margin: '0 0 28px',
              }}
            >
              {description}
            </p>
          )}

          {actions.length > 0 && (
            <div
              style={{
                display: 'flex',
                gap: 10,
                justifyContent: 'center',
                flexWrap: 'wrap',
                marginBottom: reference ? 24 : 0,
              }}
            >
              {actions.map((a, i) => {
                const btnStyle = a.primary
                  ? {
                      padding: '11px 22px',
                      borderRadius: 10,
                      backgroundColor: 'var(--color-accent)',
                      color: 'var(--color-text-on-dark)',
                      border: 'none',
                      cursor: 'pointer',
                      fontWeight: 700,
                      fontSize: 14,
                    }
                  : {
                      padding: '11px 22px',
                      borderRadius: 10,
                      backgroundColor: 'transparent',
                      color: 'var(--color-text-mid)',
                      border: '1px solid var(--color-border)',
                      cursor: 'pointer',
                      fontWeight: 600,
                      fontSize: 14,
                    };
                return a.to ? (
                  <Link key={i} to={a.to} onClick={a.onClick} style={{ ...btnStyle, textDecoration: 'none', display: 'inline-block' }}>
                    {a.label}
                  </Link>
                ) : (
                  <button key={i} type="button" onClick={a.onClick} style={btnStyle}>
                    {a.label}
                  </button>
                );
              })}
            </div>
          )}

          {reference && (
            <span
              style={{
                display: 'inline-block',
                fontSize: 11,
                color: 'var(--color-text-muted)',
                fontFamily: '"Geist Mono Variable", monospace',
                padding: '4px 10px',
                borderRadius: 6,
                backgroundColor: 'var(--color-surface-raised)',
                border: '1px solid var(--color-border)',
              }}
            >
              Ref: {reference}
            </span>
          )}
        </div>
      </div>
    </Wrapper>
  );
}
