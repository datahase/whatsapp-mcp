---
name: WhatsApp MCP
description: Compact account administration with system light and dark themes.
colors:
  background: "#fafafa"
  foreground: "#202522"
  card: "#ffffff"
  muted: "#f0f2f1"
  muted-foreground: "#626b65"
  border: "#e1e5e2"
  primary: "#177548"
  primary-foreground: "#ffffff"
  destructive: "#b42330"
  sidebar: "#f5f6f5"
  positive: "#177548"
  positive-bg: "#e9f5ee"
  warning: "#866014"
  warning-bg: "#fbf3e0"
  dark-background: "#fafafa"
  dark-foreground: "#202522"
  dark-card: "#ffffff"
  dark-muted: "#f0f2f1"
  dark-muted-foreground: "#626b65"
  dark-border: "#e1e5e2"
  dark-primary: "#177548"
  dark-primary-foreground: "#ffffff"
  dark-destructive: "#b42330"
  dark-sidebar: "#f5f6f5"
  dark-positive: "#177548"
  dark-positive-bg: "#e9f5ee"
  dark-warning: "#866014"
  dark-warning-bg: "#fbf3e0"
typography:
  headline:
    fontSize: "28px"
    fontWeight: 600
    lineHeight: 1.2
    letterSpacing: "-0.03em"
  title:
    fontSize: "19px"
    fontWeight: 600
    lineHeight: 1.4
    letterSpacing: "-0.02em"
  body:
    fontFamily: '-apple-system, BlinkMacSystemFont, "Segoe UI", sans-serif'
    fontSize: "14px"
    lineHeight: 1.65
  label:
    fontSize: "12px"
    fontWeight: 500
  code:
    fontSize: "12px"
    lineHeight: 1.8
rounded:
  sm: "6px"
  md: "8px"
  lg: "12px"
  xl: "16px"
spacing:
  xs: "4px"
  sm: "8px"
  md: "12px"
  lg: "16px"
  xl: "24px"
  panel: "36px"
components:
  button-primary:
    backgroundColor: "{colors.primary}"
    textColor: "{colors.primary-foreground}"
    rounded: "{rounded.md}"
    height: "36px"
    padding: "8px 16px"
  button-outline:
    backgroundColor: "{colors.background}"
    textColor: "{colors.foreground}"
    rounded: "{rounded.md}"
    height: "36px"
  button-ghost:
    rounded: "{rounded.md}"
    height: "36px"
  button-destructive:
    backgroundColor: "{colors.destructive}"
    textColor: "#ffffff"
    rounded: "{rounded.md}"
    height: "36px"
  input:
    rounded: "{rounded.md}"
    height: "36px"
    padding: "4px 12px"
  navigation:
    textColor: "{colors.muted-foreground}"
    rounded: "7px"
    padding: "10px 12px"
  status-connected:
    backgroundColor: "{colors.positive-bg}"
    textColor: "{colors.positive}"
    typography: "{typography.label}"
    rounded: "{rounded.sm}"
    padding: "4px 9px"
  surface:
    backgroundColor: "{colors.card}"
    rounded: "{rounded.lg}"
---

# Design System: WhatsApp MCP

## Overview

**Creative North Star: "Operate"**

Operate is a compact account manager with neutral surfaces, emerald actions, and explicit connection states. System typography, thin borders, and restrained depth keep account setup and recovery easy to scan.

**Key Characteristics:**
- System light and dark themes.
- Compact navigation and bordered surfaces.
- Text labels accompany connection colors.

## Colors

Emerald marks primary actions and successful connections. Neutral canvas, card, sidebar, muted, text, and border roles form the surrounding interface. Warning tones mark pairing, connecting, and reconnecting; destructive tones mark errors and destructive actions. The frontmatter preserves the exact CSS values, with `dark-` counterparts selected by the system preference. Secondary and accent component roles both use the muted neutral surface.

The QR canvas remains white in both themes so the code keeps a stable scanning background. Its placeholder text uses the source's local `#526258` value. These are local QR exceptions rather than new palette roles.

## Typography

Use the native system sans stack throughout. Page headings use headline, panel headings use title, and prose uses body. On screens at or below 600px, page headings shrink to 25px. Status labels use label; code blocks use code with the browser's monospace family. Form inputs are 16px below the 768px breakpoint and 14px from that breakpoint. The login code is 24px, medium weight, centered, uppercase, with 0.16em tracking and tabular numerals.

## Layout

The desktop shell has a sticky, full-height 224px left rail. An 80px topbar sits above a centered workspace capped at 1100px, with 52px 56px 60px padding. At 800px and below, the rail narrows to 184px and workspace padding becomes 36px 24px. At 600px and below, navigation moves above content, the topbar is 56px high, and workspace padding is 32px 20px.

Account rows sit inside one bordered surface. The empty Accounts view centers a phone icon, short explanation, and Add account action. Account linking uses a two-column instruction and QR panel, with a 280px QR column and 44px gap. At 800px and below it stacks with the QR first. The QR canvas is 264px square with a 232px code. Connect client is a separate navigation destination with labeled endpoint/token fields, copy controls, and configuration tabs.

## Elevation & Depth

Borders and surface tones carry most separation. Active desktop navigation adds `0 1px 3px #0000000a`; mobile navigation replaces this with an emerald bottom border. Inputs and outline buttons use shadcn's small shadow. Dialogs provide the elevated confirmation layer. Avoid adding decorative depth to account surfaces.

## Shapes

The extracted corner scale runs from small status chips through controls, surfaces, and the empty-state icon. Navigation has a local 7px radius, the brandmark 9px, and account icons 10px. Status dots and step numbers are circular. Keep corners consistent with the component rather than applying one radius everywhere.

## Components

Primary buttons use emerald fill and contrasting text, with a 90% primary hover fill. Outline, ghost, link, and destructive variants support secondary tasks, navigation, and confirmations. Standard controls are 36px high; icon and small variants follow the shadcn primitive. Icon-bearing default buttons reduce horizontal padding to 12px.

Inputs have a thin border, transparent light background, and a translucent input-colored dark background. Focus uses the primary border and a 3px half-opacity primary ring; invalid states use destructive color. Disabled controls lower opacity. Global keyboard focus uses a 2px primary outline with 4px offset where component styling does not replace it.

Status chips pair a dot with readable state text. Account rows expose a muted hover surface and an arrow. QR details keep instructions beside the scanning area on wide screens. Errors use a destructive border with actionable text. Client configuration uses underlined active tabs and muted code blocks. Confirmation dialogs place Cancel beside the destructive action.

Motion is limited to control transitions and loading spinners. The reduced-motion media query disables animations and transitions.

## Do's and Don'ts

- Do use the system color scheme and semantic CSS variables.
- Do keep labels, visible keyboard focus, and explicit loading and error states.
- Do keep code and tokens in monospace, with numeric entry using tabular figures.
- Don't fabricate account names or counts in empty states.
- Don't mix client configuration into the account-linking panel.
- Don't put destructive account controls in the account-list toolbar.
