# Syncwell docs site

VitePress source for the public documentation. Run it from the repository root:

```bash
make docs-dev      # local server, http://localhost:5173
make docs-build    # production build in website/.vitepress/dist
```

The build defaults to a root base. The Pages workflow sets
`DOCS_BASE=/syncwell/`; override it for a custom domain (`DOCS_BASE=/`).
`DOCS_ORIGIN` (default `https://nil-vaghani.github.io`) feeds the sitemap
and robots.txt URLs — set it when hosting elsewhere.

Content map:

- `index.md` — landing page
- `guide/` — quickstart, concepts, SDK, text/awareness, persistence, Python, recipes
- `self-hosting/` — deployment, configuration, operations, security, clustering
- `reference/` — wire protocol, compatibility, admin/webhooks
- `project/` — roadmap, governance, contributing, launch

Canonical protocol/operations detail remains under `../docs/`; link to it rather
than duplicating security-sensitive behavior.
