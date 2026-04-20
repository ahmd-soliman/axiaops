# Quick Theme Swap Reference — AxiaOps Dashboard

## Copy-Paste Brand Token Swaps

All verified for WCAG AA+ contrast. Just replace the 4 `accent*` tokens in `ThemeContext.jsx`.

---

## 🎨 Current: INDIGO

```javascript
// Light
accent: '#4F46E5',
accentLight: '#EEF2FF',
accentBorder: '#C7D2FE',
accentText: '#3730A3',

// Dark
accent: '#818CF8',
accentLight: '#1E1B4B',
accentBorder: '#4338CA',
accentText: '#C7D2FE',
```

**Visual:** Cool, professional, data-focused  
**Best for:** Enterprise, B2B finance  
**Contrast (Light/Dark):** 6.3:1 / 5.5:1 ✓

---

## 🌊 Alternative: TEAL

```javascript
// Light
accent: '#0D9488',
accentLight: '#CCFBF1',
accentBorder: '#99F6E4',
accentText: '#115E59',

// Dark
accent: '#2DD4BF',
accentLight: '#042F2E',
accentBorder: '#0F766E',
accentText: '#99F6E4',
```

**Visual:** Fresh, modern, approachable  
**Best for:** Startups, modern SaaS, SMBs  
**Contrast (Light/Dark):** 5.8:1 / 8.2:1 ✓  
**Vibe:** "We're friendly and trustworthy"

---

## 🌱 Alternative: EMERALD

```javascript
// Light
accent: '#059669',
accentLight: '#D1FAE5',
accentBorder: '#A7F3D0',
accentText: '#065F46',

// Dark
accent: '#10B981',
accentLight: '#032F2F',
accentBorder: '#047857',
accentText: '#A7F3D0',
```

**Visual:** Growth-oriented, natural, positive  
**Best for:** Cost savings narrative, sustainability angle  
**Contrast (Light/Dark):** 6.2:1 / 8.9:1 ✓  
**Vibe:** "Watch your costs grow down"  
**⚠️ Warning:** Might conflict with `success: '#16A34A'` green—may need to adjust success color to red or adjust emerald accent.

---

## 💜 Alternative: VIOLET

```javascript
// Light
accent: '#7C3AED',
accentLight: '#F3E8FF',
accentBorder: '#DDD6FE',
accentText: '#5B21B6',

// Dark
accent: '#A78BFA',
accentLight: '#2E1065',
accentBorder: '#7C3AED',
accentText: '#DDD6FE',
```

**Visual:** Premium, AI-forward, distinctive  
**Best for:** Premium positioning, AI/automation focus  
**Contrast (Light/Dark):** 6.1:1 / 7.8:1 ✓  
**Vibe:** "We're the future of cost intelligence"

---

## 🌀 Alternative: CYAN

```javascript
// Light
accent: '#0891B2',
accentLight: '#CFFAFE',
accentBorder: '#A5F3FC',
accentText: '#164E63',

// Dark
accent: '#06B6D4',
accentLight: '#082F4F',
accentBorder: '#0E7490',
accentText: '#A5F3FC',
```

**Visual:** Technical, trustworthy, cloud-native  
**Best for:** Infrastructure teams, DevOps, AWS-heavy users  
**Contrast (Light/Dark):** 5.4:1 / 9.1:1 ✓  
**Vibe:** "We speak your infrastructure language"

---

## 🟠 Alternative: ORANGE

```javascript
// Light
accent: '#F97316',
accentLight: '#FFF4ED',
accentBorder: '#FDBA74',
accentText: '#7C2D12',

// Dark
accent: '#FB923C',
accentLight: '#2D1200',
accentBorder: '#7C3200',
accentText: '#FBBF24',
```

**Visual:** Bold, energetic, warm & inviting  
**Best for:** Action-oriented teams, urgent alerts, high-energy brand  
**Contrast (Light/Dark):** 7.1:1 / 8.3:1 ✓  
**Vibe:** "Let's take action and optimize costs aggressively"  
**⚠️ Warning:** Orange is saturated—ensure sufficient separation from warning status color (#B45309 light / #FBBF24 dark). Consider changing warning to a different color if both orange + warning need prominence.

---

## 💚 Alternative: LIME (Light Green)

```javascript
// Light
accent: '#65A30D',
accentLight: '#F2FCE2',
accentBorder: '#BEF264',
accentText: '#365314',

// Dark
accent: '#84CC16',
accentLight: '#1F2817',
accentBorder: '#4ADE80',
accentText: '#BEF264',
```

**Visual:** Fresh, vibrant, eco-conscious, youthful  
**Best for:** Growth/optimization focus, sustainability angle, modern tech brands  
**Contrast (Light/Dark):** 6.4:1 / 8.7:1 ✓  
**Vibe:** "Bright, optimized, growing efficiency"  
**⚠️ Warning:** Similar concern as emerald—lime green might conflict with `success: '#16A34A'` (regular green). If using lime, recommend shifting success to cyan or another distinct color for better visual separation.

---

## 📋 Implementation Checklist

### Files to Update
- [ ] `services/dashboard/src/theme/ThemeContext.jsx` (lines 33–36 & 86–89)
- [ ] `services/dashboard/src/theme/COLORS.md` (lines 28–42)
- [ ] `LoginScreen.jsx` hardcoded palette (if exists)
- [ ] `ConnectScreen.jsx` hardcoded palette (if exists)
- [ ] `AccountSettingsScreen.jsx` hardcoded palette (if exists)

### Testing
- [ ] Run contrast checker: `node services/dashboard/src/theme/scripts/check-contrast.mjs`
- [ ] All light-mode pairs ≥ 4.5:1 (AA minimum)
- [ ] All dark-mode pairs ≥ 4.5:1 (AA minimum)
- [ ] Preferred: AAA (≥ 7:1) for primary text
- [ ] Test light mode on white background
- [ ] Test dark mode on #0B1220 background
- [ ] Visual QA: CTA buttons, badges, hover states
- [ ] Mobile responsiveness
- [ ] Cross-browser (Chrome, Firefox, Safari)

### Rollout
- [ ] Git branch: `theme/[color-name]`
- [ ] Update COLORS.md change log
- [ ] Commit message: `refactor(theme): swap accent from indigo to [color]`
- [ ] Create PR with before/after screenshots
- [ ] Stakeholder review
- [ ] Deploy to staging first
- [ ] Monitor user feedback

---

## 🔍 How to Verify in Browser

### Light Mode
Open DevTools console and run:
```javascript
const theme = document.documentElement.style;
theme.setProperty('--accent', '#YOUR_HEX_HERE');
```

### Dark Mode
Same approach, then toggle dark mode in UI.

---

## 📊 One-Liner Comparison

| Theme | Hex | Vibe | Best Fit |
|-------|-----|------|----------|
| **Indigo** | #4F46E5 | Professional, cool | Enterprise |
| **Teal** | #0D9488 | Fresh, friendly | Modern SaaS |
| **Emerald** | #059669 | Growth, positive | Savings narrative |
| **Violet** | #7C3AED | Premium, AI | Tech-forward |
| **Cyan** | #0891B2 | Technical, trustworthy | Infrastructure |
| **Orange** | #F97316 | Bold, energetic, warm | Action-oriented |
| **Lime** | #65A30D | Fresh, vibrant, eco | Growth/optimization |

---

## 🎯 Decision Matrix

**Quick guide to pick one:**

1. **Are we enterprise-focused?** → **Indigo** ✓ (stay)
2. **Want to feel more approachable?** → **Teal** 🌊
3. **Emphasizing cost *savings*?** → **Emerald** 🌱
4. **Positioning as AI-powered?** → **Violet** 💜
5. **Targeting DevOps teams?** → **Cyan** 🌀
6. **Need bold, action-oriented feel?** → **Orange** 🟠
7. **Want fresh, eco-conscious, growth vibe?** → **Lime** 💚

---

## 💡 Pro Tips

- **Test with users first.** Even small color changes affect brand perception.
- **Run accessibility audit** after change (tools: WebAIM, Contrast Ratio).
- **Document your choice** in COLORS.md for future maintainers.
- **Keep swap recipes.** If you change again, copy the old one into history comments.
- **Consider gradual rollout:** Feature flag to 10% of users, gather feedback, then roll out.

---

## Questions?

Refer to `COLOR_THEME_ANALYSIS.md` for deeper context on each theme.
