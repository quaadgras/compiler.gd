# compiler.gd

The static site served at https://compiler.gd: the fork on the main page and
the experimental Go to C11 compiler (github.com/quaadgras/gd-compiler) at `/c11`.

It is the Cloudflare Pages project `compiler-gd`, with `compiler.gd` and
`www.compiler.gd` attached as custom domains. To publish a change:

	npx wrangler@4 pages deploy doc/gd/site --project-name compiler-gd --branch main

(If `NODE_OPTIONS` sets `--use-openssl-ca`, clear it for wrangler.)
