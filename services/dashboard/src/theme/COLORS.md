# AxiaOps Dashboard — Color Scheme

This file is the **single source of truth** for the dashboard palette. If you
change a value here, also change it in [`ThemeContext.jsx`](./ThemeContext.jsx)
— the tokens there are the live values React reads at runtime.

Brand: **indigo** on **Tailwind-slate** neutrals. Both themes target WCAG AA
or better for every text-on-background pairing actually rendered in the app.

## Quick edit guide

1. Edit a hex in the table below to design the new value.
2. Mirror the change in `ThemeContext.jsx` (same token name).
3. If you add a brand-new token, add it to **both** `lightTheme` and
   `darkTheme` — token keys must match across themes, otherwise callers that
   read `theme.fooBar` will crash under one mode.
4. Re-run the contrast check in `Verification` below (AA minimum: 4.5:1 for
   body text, 3:1 for large text / UI controls).

Local palettes in screens that render pre-ThemeProvider
(`LoginScreen.jsx`, `ConnectScreen.jsx`, `AccountSettingsScreen.jsx`) mirror
`darkTheme` by hand and must be updated alongside this file.

## Brand accent

| Token           | Light      | Dark       | Purpose                                   |
| --------------- | ---------- | ---------- | ----------------------------------------- |
| `accent`        | `#4F46E5`  | `#818CF8`  | Primary CTA, logo, active bar, spinner    |
| `accentLight`   | `#EEF2FF`  | `#1E1B4B`  | Tinted background wash (stat chips, etc.) |
| `accentBorder`  | `#C7D2FE`  | `#4338CA`  | Border on accent-tinted surfaces          |
| `accentText`    | `#3730A3`  | `#C7D2FE`  | Text on top of `accentLight`              |

### Swap recipes

Only four tokens × two themes to change the brand. Drop one of these blocks
into `ThemeContext.jsx` and into this file.

**Indigo (current):**
```
light: accent #4F46E5  accentLight #EEF2FF  accentBorder #C7D2FE  accentText #3730A3
dark:  accent #818CF8  accentLight #1E1B4B  accentBorder #4338CA  accentText #C7D2FE
```

**Teal:**
```
light: accent #0D9488  accentLight #CCFBF1  accentBorder #99F6E4  accentText #115E59
dark:  accent #2DD4BF  accentLight #042F2E  accentBorder #0F766E  accentText #99F6E4
```

**Orange (legacy):**
```
light: accent #F97316  accentLight #FFF4ED  accentBorder #FDBA74  accentText #7C2D12
dark:  accent #F97316  accentLight #2D1200  accentBorder #7C3200  accentText #FB923C
```

## Light theme

| Token            | Hex        | Notes                                     |
| ---------------- | ---------- | ----------------------------------------- |
| **Backgrounds**  |            |                                           |
| `bg`             | `#F6F8FB`  | Page background, faintly cool             |
| `bgSecondary`    | `#EDF1F7`  | Sidebars, headers, grouped sections       |
| `surface`        | `#FFFFFF`  | Cards, panels                             |
| `surfaceAlt`     | `#F6F8FB`  | Alt rows, subtle hover fills              |
| `surfaceRaised`  | `#FFFFFF`  | Modals, dropdowns (depth via shadow)      |
| **Legacy navy**  |            | Kept for backward compatibility           |
| `navy`           | `#0F172A`  |                                           |
| `navyMid`        | `#1E293B`  |                                           |
| `navyLight`      | `#334155`  |                                           |
| **Text**         |            | 4 tiers, AAA → AA                         |
| `text`           | `#0F172A`  | Primary, 17.8:1 on surface (AAA)          |
| `textMid`        | `#334155`  | Secondary, 10.3:1 (AAA)                   |
| `textMuted`      | `#5C6876`  | Tertiary, 5.3:1 on bg (AA)                |
| `textSub`        | `#7F8A9E`  | Lightest readable, 3.5:1 (AA-large)       |
| `textOnDark`     | `#FFFFFF`  | Text placed over dark accent fills        |
| `white`          | `#FFFFFF`  |                                           |
| **Chrome**       |            |                                           |
| `card`           | `#FFFFFF`  |                                           |
| `border`         | `#E2E8F0`  | slate-200, visible but quiet              |
| **Chips**        |            |                                           |
| `chipBg`         | `#F1F5F9`  |                                           |
| `chipText`       | `#475569`  | 6.9:1 on `chipBg`                         |
| `chipProdBg`     | `#FEE2E2`  | red-100                                   |
| `chipProdText`   | `#991B1B`  | 6.8:1                                     |
| `chipStagBg`     | `#FEF3C7`  | amber-100                                 |
| `chipStagText`   | `#854D0E`  | 6.2:1                                     |
| `ghostBadgeBg`   | `#FFE4E6`  | rose-100                                  |
| `ghostBadgeText` | `#9F1239`  | rose-800                                  |
| **Status**       |            | Saturated for light bg clarity            |
| `error`          | `#DC2626`  | red-600                                   |
| `success`        | `#16A34A`  | green-600                                 |
| `warning`        | `#B45309`  | amber-700, 4.7:1 on bg                    |

## Dark theme

| Token            | Hex        | Notes                                     |
| ---------------- | ---------- | ----------------------------------------- |
| **Backgrounds**  |            | 4-level elevation ladder                  |
| `bg`             | `#0B1220`  | Deepest layer (page)                      |
| `bgSecondary`    | `#111827`  | Sidebars, grouped sections                |
| `surface`        | `#182031`  | Cards / panels                            |
| `surfaceAlt`     | `#1F2A3D`  | Alt rows, hover                           |
| `surfaceRaised`  | `#2A3550`  | Dropdowns, tooltips, modals               |
| **Legacy navy**  |            |                                           |
| `navy`           | `#0B1220`  |                                           |
| `navyMid`        | `#111827`  |                                           |
| `navyLight`      | `#2A3550`  |                                           |
| **Text**         |            | Warm off-whites, 4 tiers                  |
| `text`           | `#E5ECF5`  | 15.7:1 on `bg` (AAA)                      |
| `textMid`        | `#B8C4D6`  | 9.2:1 on surface (AAA)                    |
| `textMuted`      | `#8497B2`  | 6.3:1 (AA)                                |
| `textSub`        | `#94A3B8`  | 7.3:1 on bg (AAA)                         |
| `textOnDark`     | `#FFFFFF`  |                                           |
| `white`          | `#FFFFFF`  |                                           |
| **Chrome**       |            |                                           |
| `card`           | `#182031`  |                                           |
| `border`         | `#2E3A52`  | Visible, not harsh                        |
| **Chips**        |            |                                           |
| `chipBg`         | `#263042`  |                                           |
| `chipText`       | `#B8C4D6`  | 7.5:1                                     |
| `chipProdBg`     | `#3B0D0D`  | deep red wash                             |
| `chipProdText`   | `#FCA5A5`  | red-300, 8.9:1                            |
| `chipStagBg`     | `#3A1D00`  | deep amber wash                           |
| `chipStagText`   | `#FCD34D`  | amber-300, 10.7:1                         |
| `ghostBadgeBg`   | `#3B0F1A`  | deep rose wash                            |
| `ghostBadgeText` | `#FDA4AF`  | rose-300                                  |
| **Status**       |            | Desaturated for dark-mode comfort         |
| `error`          | `#F87171`  | red-400, 6.8:1 on bg                      |
| `success`        | `#34D399`  | emerald-400, 9.7:1                        |
| `warning`        | `#FBBF24`  | amber-400, 11.2:1                         |

## Verification

All contrast ratios measured against the background token the text is
actually rendered on in the app (checked Apr 2026 against `ThemeContext.jsx`
current values).

| Pair                                      | Light     | Dark      | Target       |
| ----------------------------------------- | --------- | --------- | ------------ |
| `text` on `surface` / `bg`                | 17.8:1    | 13.7:1    | AAA (≥ 7)    |
| `textMid` on surface                      | 10.3:1    | 9.2:1     | AAA          |
| `textMuted` on surface                    | 4.8:1     | 5.5:1     | AA (≥ 4.5)   |
| `textSub` on surface                      | 3.5:1     | 7.3:1     | ≥ 3 (AA-lg)  |
| `accent` on surface                       | 6.3:1     | 5.5:1     | AA           |
| `accentText` on `accentLight`             | 8.9:1     | 10.7:1    | AAA          |
| `error` / `success` / `warning` on bg     | 4.8–3.3:1 | 6.8–11:1  | AA+          |

Re-run the scripted check after any edit:
[`scripts/check-contrast.mjs`](./scripts/check-contrast.mjs)

```bash
node services/dashboard/src/theme/scripts/check-contrast.mjs
```

## Do not confuse

`serviceConfig.js` hardcodes AWS service **brand** colors (EC2 orange,
RDS blue, Lambda amber, …). These are AWS-owned and intentionally not part
of the AxiaOps palette. Do not replace them with theme tokens.
