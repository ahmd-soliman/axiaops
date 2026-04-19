import { flatStyle } from './index';

export function Text({ style, children, numberOfLines, ...rest }) {
  const lineClampStyle = numberOfLines === 1
    ? { overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }
    : numberOfLines
      ? { overflow: 'hidden', display: '-webkit-box', WebkitLineClamp: numberOfLines, WebkitBoxOrient: 'vertical' }
      : {};

  return (
    <span {...rest} style={{ display: 'block', ...flatStyle(style), ...lineClampStyle }}>
      {children}
    </span>
  );
}
