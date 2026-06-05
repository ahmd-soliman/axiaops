import { describe, it, expect } from 'vitest';
import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { ThemeProvider } from './theme.jsx';
import { ThemeToggle } from './Brand.jsx';

describe('theme toggle', () => {
  it('flips data-theme on <html> and back', async () => {
    render(
      <ThemeProvider>
        <ThemeToggle />
      </ThemeProvider>,
    );
    const first = document.documentElement.dataset.theme;
    expect(first === 'light' || first === 'dark').toBe(true);

    await userEvent.click(screen.getByRole('button'));
    const second = document.documentElement.dataset.theme;
    expect(second).not.toBe(first);

    await userEvent.click(screen.getByRole('button'));
    expect(document.documentElement.dataset.theme).toBe(first);
  });
});
