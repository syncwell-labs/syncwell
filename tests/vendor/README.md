# Vendored test assets

## `axe.min.js`

- Project: [axe-core](https://github.com/dequelabs/axe-core)
- Version: 4.14.0
- License: Mozilla Public License 2.0 — see [`axe.LICENSE`](./axe.LICENSE)
- Used by: `tests/a11y/axe_a11y.py`

Vendored so the accessibility scan runs offline and reproducibly in CI. The
file is unmodified from the upstream release. To update it, replace both
`axe.min.js` and `axe.LICENSE` from the same tagged release.
