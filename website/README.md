# AxiaOps documentation site

The public docs site (Astro + [Starlight](https://starlight.astro.build)) — architecture overview, self-hosted deployment guides (Docker Compose / Helm / AWS ECS Express via Terraform).

## Commands

Run from this directory:

| Command | Action |
|---|---|
| `npm install` | Install dependencies |
| `npm run dev` | Start local dev server at `localhost:4321` |
| `npm run build` | Build the production site to `./dist/` |
| `npm run preview` | Preview a production build locally before deploying |

## Structure

```
website/
├── public/                  # Static assets (favicon, etc.)
├── src/
│   ├── assets/               # Images embedded in docs pages
│   ├── content/docs/         # The actual pages -- .md/.mdx, one route per file
│   └── styles/custom.css     # Theme overrides
└── astro.config.mjs          # Site title, logo, sidebar structure
```

Add a new page by dropping a `.md`/`.mdx` file under `src/content/docs/` and adding it to the `sidebar` in `astro.config.mjs`.
