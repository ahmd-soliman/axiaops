import { flatStyle } from './index';

export function Pressable({ onPress, style, children, disabled, activeOpacity, ...rest }) {
  return (
    <button
      {...rest}
      onClick={onPress}
      disabled={disabled}
      style={{
        background: 'none',
        border: 'none',
        padding: 0,
        font: 'inherit',
        color: 'inherit',
        textAlign: 'inherit',
        cursor: disabled ? 'default' : 'pointer',
        display: 'flex',
        flexDirection: 'column',
        ...flatStyle(style),
      }}
    >
      {children}
    </button>
  );
}
