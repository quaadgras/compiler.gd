# compiler.gd

The static site served at https://compiler.gd: the fork on the main page and
the experimental Go to C11 compiler (github.com/quaadgras/gd-compiler) at `/c11`.

`/play` is a playground: the go command, built for GOOS=js, compiles and
runs programs in the page (play.html, play/worker.mjs). Its large files are
built, not kept here: `build.sh` makes the site in `dist/` with the go
command (stripped and gzipped, 24 MB: Pages takes files up to 25 MiB),
std's source, and a build cache of common packages made by
play/prebuild.mjs under node as the page would make it.

It is the Cloudflare Pages project `compiler-gd`, with `compiler.gd` and
`www.compiler.gd` attached as custom domains. To publish a change (after
src/make.bash, so that bin/go is the toolchain to publish):

	doc/gd/site/build.sh
	cd doc/gd/site/dist && npx wrangler@4 pages deploy . --project-name compiler-gd --branch main

(If `NODE_OPTIONS` sets `--use-openssl-ca`, clear it for wrangler.)
