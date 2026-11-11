# Design Mockups

This folder holds HTML mockups that support milestones. The operator or the lead adds them. They are usual files. The app shows them on the Design page and does not change them.

## Package convention

```text
design/
  checkout-a/
    index.html
    README.md        optional context
    manifest.json    optional, see below
    assets/
```

You can also put one complete `.html` file directly in this folder.

Write the name of the mockup in the Notes section of a milestone. Then the agent can find it.

## The manifest

All keys of `manifest.json` are optional:

```json
{
  "title": "Checkout, variant A",
  "description": "The summary is next to the form.",
  "group": "checkout",
  "themes": ["light", "dark"]
}
```

| Key | Function |
| --- | --- |
| `title` | The name on the card. Without it, the app uses the folder name. |
| `description` | One sentence on the card. Without it, the app uses the first line of `README.md`. |
| `group` | The name of a decision. Give the same group to the variants of one decision, for example A, B and C. The Design page compares them. |
| `themes` | The colour themes of the mockup: `light`, `dark`, or both. |

Without `themes`, the app looks for `prefers-color-scheme` or `data-theme` in the HTML and CSS files. If it finds one of them, the mockup gets both themes.

## Light and dark

The Design page can force a theme on a mockup with both themes. The app then does these steps:

- It makes each `prefers-color-scheme` media query match the selected theme.
- It sets `data-theme` and `color-scheme` on the root element.

Thus a mockup can use a media query or a `[data-theme="dark"]` selector.

## Rules for a mockup

- Keep each file of the mockup in its folder. Use relative links.
- The page runs in a sandbox. It can read only the files under `design/`. It cannot read the data of the app.
- Links to the internet can work, but a mockup that works offline is better.
