# Fused runtime distribution snapshot

Engine owns the source at `Usefused/engine:runtime/sdk`. This directory contains
only its compiled npm distribution and provenance, embedded by `fused-cli`.

To refresh: run `npm ci && npm test && npm run pack` in Engine's `runtime/sdk`,
then replace this directory's `.tgz` and `manifest.json` with the two files from
`dist/package/`. Keep the archive filename and SHA-256 aligned with the manifest.
Do not edit compiled code here. Runtime API changes belong in Engine.

Download with `fused-cli app runtime-client --out ./vendor`, then use the printed
`npm install` command. No Engine login is needed to download the package.
