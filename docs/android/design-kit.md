# DsxID Android design kit

This kit keeps the Android preview visually consistent with the Windows app and
the dsxid.org landing page. The implementation lives in
[`android/`](../../android/) and uses Jetpack Compose Material 3.

## Brand rules

- The real DsxID logo is used **only as the Android launcher icon**. It is not
  placed inside the app UI.
- In-app identity marks use an abstract key glyph in a soft blue tile.
- The app is light-only, with a calm Windows/Fluent feel: white cards, thin
  borders, compact type, one blue accent.
- English UI copy. Keep sentences short and factual; no marketing language in
  security dialogs.

## Color tokens

| Token | Value | Use |
|---|---|---|
| Background | `#F3F3F3` | app background |
| Surface | `#FFFFFF` | cards, fields, navigation bar |
| Surface variant | `#F8F8F8` | inset rows, metadata blocks |
| Accent | `#0067C0` | primary actions, links, active state |
| Accent hover/pressed | `#005A9E` / `#004578` | web/desktop equivalents |
| Accent soft | `#E8F2FC` | icon tiles, info banners, selected nav |
| Accent border | `#CFE4F7` | soft outlines |
| Text | `#1B1B1B` | headings and primary text |
| Text secondary | `#5C5C5C` | body copy |
| Text tertiary | `#8A8A8A` | captions, metadata |
| Border | `#CFCFCF` | field and button outlines |
| Border soft | `#EDEDED` | card separators |
| Success | `#0F7B0F` | approved state |
| Success soft | `#DFF6DD` | success badges |
| Danger | `#C42B1C` | delete identity, errors |
| Danger soft | `#FDE7E9` | error containers |

CSS equivalents are in [`../../landing/styles.css`](../../landing/styles.css).

## Typography

System sans (Roboto on Android; Segoe UI on Windows). No bundled fonts.

| Role | Size | Weight |
|---|---|---|
| Screen title | 32 sp | SemiBold, tight tracking |
| Section title | 24 sp | SemiBold |
| Card title | 15 sp | SemiBold |
| Body | 13 sp | Regular |
| Small body | 12 sp | Regular |
| Metadata | 11 sp | Medium |
| Badge | 10 sp | Regular |

Fingerprints and public keys use the platform monospace font, 12 sp.

## Spacing and shape

- Base spacing scale: 4, 8, 12, 16, 20, 24 dp.
- Screen horizontal padding: 20 dp.
- Auth screens: 24 dp, centered content, max width 520 dp.
- Card padding: 16 dp.
- Card radius: 10 dp; fields and buttons: 6 dp; icon tiles: 10 dp.
- Hairlines: 1 dp `#EDEDED`; card border: 1 dp `#EDEDED`.
- Primary/secondary button height: 44 dp.
- Bottom navigation: Material 3 `NavigationBar`, four destinations.
- Status/navigation bars are transparent; content is edge-to-edge with
  `safeDrawing` insets.

## Components

### Key mark

46 dp soft-blue tile, 10 dp radius, blue outlined key icon at 22 dp. Used on
auth screens, identity cards, account rows, and info cards. Never the real logo.

### Card

White surface, 10 dp radius, 1 dp soft border, 16 dp padding. No heavy shadows.
Cards group one idea; avoid nesting cards.

### Preview banner

Soft blue container with an info icon. The current Android build uses it to
state that cryptography is not implemented. Remove it only when the preview
status is no longer true.

### Fields

Outlined text fields, 6 dp radius, white container, blue focused border.
Labels above the field. Optional fields say “optional”. Password fields have a
visibility toggle. Validation text is small and appears under the field.

### Buttons

- **Primary:** blue fill, white label, 44 dp, optional leading icon.
- **Secondary:** white fill, 1 dp border, dark label.
- **Danger:** white fill, red border/text; used for delete identity.
- **Text button:** dialog actions only.
- One primary action per screen/card.

### Badge

Pill with 8 dp horizontal padding. “Unlocked” and “Approved” use the success
palette; “Development” uses a muted surface variant.

### Metadata

Label in 11 sp tertiary text above a 12 sp value. Fingerprints and keys use
monospace and wrap to at most two lines.

### Empty state

Card with a short title and one sentence. No illustrations or large empty
graphics.

### Dialogs

Material 3 `AlertDialog` with a warning icon for destructive actions. Destructive
confirmation copy must name exactly what is removed and what is required to
restore it.

## Screen inventory

1. **Setup** — brand/key mark, title, preview banner, name, passphrase, confirm,
   create button, restore button, recovery warning.
2. **Unlock** — identity chip, passphrase, unlock button, preview notice.
3. **Overview** — identity card with fingerprint, public key, copy/lock actions,
   “Only you can use it” card, three-step sign-in explainer.
4. **Profile** — e-shop fields in one column, save button, sharing note.
5. **Approved accounts** — one card per origin with status, last approval, and
   sign-in count; empty state.
6. **Settings** — application info, security actions, delete identity, roadmap
   card, lock action.

## Interaction rules

- The current preview starts at Setup. Any 8+ character passphrase unlocks the
  demo; this is deliberately labeled.
- Copy actions show a snackbar confirming what was copied.
- Lock returns to Unlock without deleting the identity.
- Delete identity requires a confirmation dialog and returns to Setup.
- No network calls, analytics, or advertising identifiers belong in the starter.
- The Android launcher icon uses the real logo; adaptive icons use a white
  background and the blue key mark.

## Assets

- Launcher icons: `android/app/src/main/res/mipmap-*`
- Adaptive icon: `mipmap-anydpi-v26/ic_launcher*.xml`
- Source mark: `build/logo-source.png`
- Theme tokens: `android/app/src/main/java/org/dsxid/app/ui/theme/Theme.kt`
