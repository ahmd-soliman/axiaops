// Flattens React Native–style `style={[a, b && c, [d, e]]}` arrays into a
// single object suitable for React DOM. Falsy entries are skipped.
export function flatStyle(style) {
  if (!style) return undefined;
  if (!Array.isArray(style)) return style;
  return Object.assign({}, ...style.flat(Infinity).filter(Boolean));
}

export { View } from './View';
export { Text } from './Text';
export { Pressable } from './Pressable';
export { LinkButton, RowLink, StretchedRowLink } from './RouterLink';
export { Spinner } from './Spinner';
export { Overlay } from './Overlay';
export { InfoTooltip } from './InfoTooltip';
export { useWindowWidth } from './useWindowWidth';
