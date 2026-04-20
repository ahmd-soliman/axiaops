import { flatStyle } from './index';

export function View({ style, children, ...rest }) {
  return (
    <div
      {...rest}
      style={{ display: 'flex', flexDirection: 'column', ...flatStyle(style) }}
    >
      {children}
    </div>
  );
}
