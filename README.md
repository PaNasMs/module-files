# PaNasMs Files module

Installable file manager for PaNasMs. Current manifest version: **0.2.11**.
Requires core `>=0.2.0,<0.3.0`, module API 1 and ARM64 Linux.

## Features

- Folder/device sidebar with expandable trees and automatic removable-volume mounting.
- Local and network-mounted locations, folder navigation and list/tile views.
- Uploads, drag-and-drop moves, copy, rename and folder creation.
- Trash, restore and permanent deletion, with confirmation dialogs.
- File-type icons and lazy image thumbnails in tile view.
- Administrator permission editing for the current folder or selected entries.

File operations run under the requesting Linux identity where appropriate;
administrative ownership/permission changes are validated by the host. Filesystem
and mount management remain responsibilities of the core storage subsystem.

## Development

The frontend uses React/TypeScript and host-provided UI contracts. The server uses
Go and the pinned [module SDK](https://github.com/PaNasMs/module-sdk). Python helpers
are included where the module requires them. Do not bundle another copy of the
host React/router/query runtime.

Use ARM64 Linux, Node.js 24, Go 1.26 or newer, Python 3, a C compiler and
`libpam0g-dev`. The release workflow pins Go 1.27.1. From this repository:

```sh
sh scripts/build.sh
```

This installs locked npm dependencies, builds the UI, runs PAM-enabled Go tests,
builds the server, checks Python syntax/translation keys and available Python
tests, then writes `dist/<id>-<version>-arm64.unsigned.zip`. This is an unsigned
build payload and cannot be installed directly. The script labels output ARM64;
build on ARM64 rather than treating it as a cross-compilation command.

## Install and release

Install the signed version from the PaNasMs **Modules** catalog, or upload a signed
`.panasms` archive from the [registry](https://github.com/PaNasMs/module-registry).

For a new release, update `manifest.json`, `package.json` and the npm lockfile
consistently, commit, then push the matching `vX.Y.Z` tag. The workflow also builds
branches/PRs, but only a version tag publishes a source release. Its unsigned
payload is imported and signed by the registry, which publishes the installable
archive and updates the catalog. Signing keys are not stored in this repository.
Publish a new version instead of replacing an existing release.

## Documentation and license

Public documentation is maintained in English. Original code uses
[PolyForm Noncommercial 1.0.0](LICENSE); see [NOTICE](NOTICE) for third-party scope.

## Interrupted transfers

Version 0.2.12 requires core 0.2.2. Copies use a sibling staging directory and
cooperative cancellation checkpoints; the destination becomes visible only after
the copy completes. Cross-filesystem moves publish the destination before deleting
the source. Failed source cleanup retains both copies and a transfer journal for
review. This does not provide snapshots of files being changed by other clients.
Run `python3 scripts/check.py` for syntax, locale and transfer unit checks.
