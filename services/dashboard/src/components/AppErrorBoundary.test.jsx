// Anchor test for the Vitest + Testing Library setup. Verifies the
// AppErrorBoundary's behavioural contract: when a child throws during
// render, the boundary catches the error and renders the ServerError
// fallback instead of letting the crash propagate.
//
// This is the first frontend test in the dashboard. The goal is to prove
// the runner works end-to-end (jsdom + React Testing Library + jest-dom
// matchers + a real component with its own dependency graph) — not to
// be exhaustive. Broader coverage is follow-up work.
import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest';
import { render, screen } from '@testing-library/react';
import AppErrorBoundary from './AppErrorBoundary';
import { ThemeProvider } from '../theme/ThemeContext';

// A child component that throws unconditionally during render. Used to
// drive the error boundary into its fallback state.
function ThrowsOnRender() {
  throw new Error('intentional test error');
}

// ServerError → ErrorPage deliberately avoids useNavigate (see the comment
// in ServerError.jsx — both action buttons use window.location instead, so
// the page works even when the error originates above <Router>). That
// means tests don't need a router wrapper here.

describe('AppErrorBoundary', () => {
  let consoleErrorSpy;

  beforeEach(() => {
    // React logs the caught error to console.error during render. Suppress
    // the noise so the test output stays readable; we're not asserting on
    // log output here.
    consoleErrorSpy = vi.spyOn(console, 'error').mockImplementation(() => {});
  });

  afterEach(() => {
    consoleErrorSpy.mockRestore();
  });

  // ThemeProvider sets isLoading=true on first render and resolves it via an
  // effect that awaits an async storage.getItem('theme'). The initial paint
  // therefore renders nothing — getBy* queries fail synchronously. Use the
  // async findBy* variants throughout so RTL waits for the second render.

  it('renders children when no error is thrown', async () => {
    render(
      <ThemeProvider>
        <AppErrorBoundary>
          <span data-testid="happy-child">all good</span>
        </AppErrorBoundary>
      </ThemeProvider>,
    );
    expect(await screen.findByTestId('happy-child')).toBeInTheDocument();
    expect(screen.getByText('all good')).toBeInTheDocument();
  });

  it('renders the ServerError fallback when a child throws', async () => {
    render(
      <ThemeProvider>
        <AppErrorBoundary>
          <ThrowsOnRender />
        </AppErrorBoundary>
      </ThemeProvider>,
    );
    // ServerError surfaces a "500" code badge + the standard copy from
    // ErrorPage. Asserting both anchors the fallback identity without
    // brittle reliance on exact markup.
    expect(await screen.findByText(/Error\s+500/i)).toBeInTheDocument();
    expect(screen.getByText(/Something went wrong on our end/i)).toBeInTheDocument();
  });

  it('clears the latched fallback state when resetKey changes', async () => {
    const { rerender } = render(
      <ThemeProvider>
        <AppErrorBoundary resetKey="/route-a">
          <ThrowsOnRender />
        </AppErrorBoundary>
      </ThemeProvider>,
    );
    expect(await screen.findByText(/Error\s+500/i)).toBeInTheDocument();

    // Same boundary, new resetKey + a non-throwing child. The boundary
    // should clear its latched error state and render the new child.
    rerender(
      <ThemeProvider>
        <AppErrorBoundary resetKey="/route-b">
          <span data-testid="recovered">recovered</span>
        </AppErrorBoundary>
      </ThemeProvider>,
    );
    expect(await screen.findByTestId('recovered')).toBeInTheDocument();
  });
});
