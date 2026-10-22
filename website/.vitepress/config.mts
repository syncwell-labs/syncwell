import { defineConfig } from "vitepress";

const base = process.env.DOCS_BASE || "/";
const origin = process.env.DOCS_ORIGIN || "https://syncwell-labs.github.io";
const repo = "https://github.com/Nil-Vaghani/syncwell";

export default defineConfig({
  lang: "en-US",
  title: "Syncwell",
  titleTemplate: ":title · Syncwell",
  description:
    "Self-hostable real-time collaboration: conflict-free shared state, collaborative text, presence, offline sync, and a single Go binary.",
  base,
  sitemap: { hostname: `${origin}${base}` },
  srcExclude: ["README.md"],
  cleanUrls: true,
  lastUpdated: true,
  head: [
    ["link", { rel: "icon", href: `${base}logo.svg` }],
    ["meta", { name: "theme-color", content: "#f2eee6" }],
    ["meta", { property: "og:type", content: "website" }],
    ["meta", { property: "og:title", content: "Syncwell" }],
    [
      "meta",
      {
        property: "og:description",
        content: "Self-hostable real-time collaboration engine in a single Go binary.",
      },
    ],
  ],
  themeConfig: {
    logo: "/logo.svg",
    siteTitle: "Syncwell",
    nav: [
      { text: "Guide", link: "/guide/getting-started" },
      { text: "Self-hosting", link: "/self-hosting/quickstart" },
      { text: "Reference", link: "/reference/protocol" },
      {
        text: "Project",
        items: [
          { text: "Roadmap", link: "/project/roadmap" },
          { text: "Governance", link: "/project/governance" },
          { text: "Contributing", link: "/project/contributing" },
          { text: "Launch runbook", link: "/project/launch" },
        ],
      },
    ],
    sidebar: {
      "/guide/": [
        {
          text: "Guide",
          items: [
            { text: "Getting started", link: "/guide/getting-started" },
            { text: "Core concepts", link: "/guide/concepts" },
            { text: "JavaScript SDK", link: "/guide/sdk" },
            { text: "Text and awareness", link: "/guide/text-and-awareness" },
            { text: "Persistence and offline", link: "/guide/persistence" },
            { text: "Framework recipes", link: "/guide/recipes" },
            { text: "Python client", link: "/guide/python" },
          ],
        },
      ],
      "/self-hosting/": [
        {
          text: "Self-hosting",
          items: [
            { text: "Quickstart", link: "/self-hosting/quickstart" },
            { text: "Configuration", link: "/self-hosting/configuration" },
            { text: "Operations", link: "/self-hosting/operations" },
            { text: "Security", link: "/self-hosting/security" },
            { text: "Clustering", link: "/self-hosting/clustering" },
          ],
        },
      ],
      "/reference/": [
        {
          text: "Reference",
          items: [
            { text: "Wire protocol", link: "/reference/protocol" },
            { text: "Compatibility policy", link: "/reference/compatibility" },
            { text: "Admin and webhooks", link: "/reference/admin-and-webhooks" },
          ],
        },
      ],
      "/project/": [
        {
          text: "Project",
          items: [
            { text: "Roadmap", link: "/project/roadmap" },
            { text: "Governance", link: "/project/governance" },
            { text: "Contributing", link: "/project/contributing" },
            { text: "Launch runbook", link: "/project/launch" },
          ],
        },
      ],
    },
    search: {
      provider: "local",
      options: {
        translations: {
          button: {
            // visible text and accessible name are identical (WCAG 2.5.3);
            // the CSS keycap cluster is hidden in custom.css because its
            // kbd text was not part of the accessible name (axe 4.4.13).
            buttonText: "Search (Ctrl+K)",
            buttonAriaLabel: "Search (Ctrl+K)",
          },
        },
      },
    },
    editLink: {
      pattern: `${repo}/edit/main/website/:path`,
      text: "Edit this page",
    },
    socialLinks: [{ icon: "github", link: repo }],
    footer: {
      message: "Released under the MIT License.",
      copyright: "Copyright © 2026 Syncwell contributors",
    },
    outline: { level: [2, 3] },
  },
});
