import { Link } from 'react-router-dom';
import { flatStyle } from './index';

// Shared link primitives (issue #130). In-app navigation used to be
// imperative — `<button onClick={() => navigate(path)}>` — which emits no
// real `href`, so the browser had no URL to act on: middle-click,
// Ctrl/Cmd-click, and the right-click "Open link in new tab" affordance all
// silently did nothing. These wrap react-router's <Link> (which renders a
// real `<a href>`) so the browser owns those new-tab behaviours while a
// plain left-click still routes in-place via the anchor's default click.
//
// Each primitive strips the browser's default anchor chrome (underline,
// link colour) so the <Link> can be styled exactly like the <button> it
// replaces — callers pass the same inline `style` the button used.

// Base reset shared by every link primitive. `font: inherit` matters because
// anchors otherwise inherit the UA stylesheet's link styling, not the
// surrounding text.
const linkReset = {
  textDecoration: 'none',
  color: 'inherit',
  font: 'inherit',
  cursor: 'pointer',
};

// LinkButton — a <Link> dressed as a nav item / CTA button. Drop-in for
// `<button onClick={() => navigate(to)}>`: pass `to` plus the button's
// existing `style`. Hover handlers (onMouseEnter/Leave), aria-current, title,
// etc. pass straight through to the underlying <a>.
export function LinkButton({ to, style, children, ...rest }) {
  return (
    <Link
      to={to}
      style={{ ...linkReset, display: 'inline-flex', alignItems: 'center', ...flatStyle(style) }}
      {...rest}
    >
      {children}
    </Link>
  );
}

// RowLink — a <Link> that fills a clickable list row or card. Drop-in for the
// `<button onClick={...}>` that wrapped a whole row's content. Defaults to a
// full-width flex box so the row's existing inner layout is unchanged. Keep
// per-row action buttons (Scan, Manage, checkboxes) as real <button>s OUTSIDE
// this link — interactive content can't legally nest inside an <a>.
export function RowLink({ to, style, children, ...rest }) {
  return (
    <Link
      to={to}
      style={{ ...linkReset, display: 'flex', width: '100%', boxSizing: 'border-box', ...flatStyle(style) }}
      {...rest}
    >
      {children}
    </Link>
  );
}

// StretchedRowLink — for semantic <table> rows, where an <a> cannot wrap the
// <td>s (invalid HTML). Render it as a child of the FIRST <td> in a row whose
// <tr> is `position: relative`; the anchor is visually empty and stretches
// across the whole row via `inset: 0`, so a middle/Ctrl/plain-click anywhere
// on the row hits it. It sits at `zIndex: 0`; real per-row controls (Scan,
// Manage) must be raised above it with `position: relative; zIndex: 1` so they
// stay independently clickable. `label` becomes the accessible name.
export function StretchedRowLink({ to, label, ...rest }) {
  return (
    <Link
      to={to}
      aria-label={label}
      style={{ position: 'absolute', inset: 0, zIndex: 0 }}
      {...rest}
    />
  );
}
