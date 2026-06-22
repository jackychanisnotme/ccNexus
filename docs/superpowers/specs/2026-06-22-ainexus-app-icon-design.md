# AINexus App Icon Design

## Goal

Replace the current AINexus application icon and the rocket icon in the desktop
interface header with the selected light "Radial Hub" artwork.

The icon should communicate:

- multiple inputs and multiple outputs;
- a central routing and orchestration hub;
- automatic endpoint rotation and failover;
- a clean, modern, trustworthy desktop product.

## Selected Artwork

Source:

`generated-images/ainexus-icon-hub-radial.png`

The selected artwork uses a pearl-white rounded-square background, translucent
blue and coral routing channels, and a luminous central crystal. Its radial
layout makes the multi-input, multi-output behavior visible without text or
letterforms.

## Asset Outputs

The source artwork will be normalized to a square image and used to produce:

- `cmd/desktop/build/appicon.png` for Wails, macOS/Linux, and the non-Windows
  tray icon;
- `cmd/desktop/build/windows/icon.ico` with multiple embedded Windows icon
  sizes;
- a frontend PNG asset for the desktop header.

The existing `cmd/desktop/build/appicon.svg` will be updated only if a faithful
vector-compatible wrapper can be produced without redrawing or degrading the
selected artwork.

## Desktop Interface

The current rocket emoji before `AINexus` in the top-left header will be
replaced by the new frontend image asset.

The header icon will:

- use the same artwork as the application icon;
- have a stable square size;
- preserve the existing title and subtitle layout;
- include appropriate alternative text;
- remain legible on all existing themes.

## Packaging And Runtime

Existing Wails embed paths and application metadata will remain unchanged.
Replacing the current files at their existing paths allows desktop packaging
and tray setup to continue using the same code.

## macOS Menu Bar Icon

The macOS menu bar must not reuse the full-color application icon. The
application artwork has an opaque pearl-white tile, which appears as a white
square when reduced to the 18-point status-item size.

A dedicated macOS menu bar asset will be added with:

- a transparent background;
- a simplified monochrome routing-hub silhouette;
- no rounded-square application tile;
- broad shapes that remain legible at 18 points;
- enough interior negative space to preserve the multi-input, multi-output
  hub concept.

The macOS tray implementation will load this dedicated asset and mark its
`NSImage` as a template image. macOS will then render the symbol with the
appropriate foreground color for light and dark menu bars.

Windows will continue using the multi-resolution color ICO because Windows
notification-area icons do not use the macOS template-image convention.

## Verification

Implementation verification will include:

- confirming PNG and ICO dimensions and formats;
- confirming the macOS menu bar PNG has transparency and no opaque tile;
- building the frontend;
- running relevant frontend tests;
- building or compiling the desktop Go package where supported;
- visually checking the desktop header at the normal window size;
- verifying that the macOS tray code marks the image as a template image;
- checking a 32-pixel rendering of the final icon for recognizability.

## Scope

This change does not redesign other interface icons, the website logo, QR-code
assets, or product screenshots.
