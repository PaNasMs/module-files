# PaNasMs Files module

Installable file manager for PaNasMs. Current manifest version: **0.2.17**.
Requires core `>=0.2.5,<0.3.0`, module API 1 and ARM64 Linux.

## Features

- Folder/device sidebar with expandable trees and automatic removable-volume mounting.
- Local and network-mounted locations, folder navigation and list/tile views.
- Background upload, copy, move and deletion queue with progress/cancellation in the shell task menu, plus drag-and-drop moves, copy, rename and folder creation.
- Trash, restore and permanent deletion, including mixed multi-selection of files and folders with one confirmation and per-item failures.
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

## Ordinary-user access

Version 0.2.13 requires core 0.2.3 or newer. It accepts explicitly enabled ordinary
panel accounts through the SDK's `LookupPanel`. Filesystem operations still execute
with the selected Linux user's UID, GID and supplementary groups. Permission editing
remains administrator-only; installing Files does not grant access to other homes.

## Transfer integrity (0.2.15)

- Uploads use bounded 1 MiB reads and private staging directories. Only a complete,
  synced upload is published; a competing upload cannot overwrite the destination.
- Final upload permissions follow the process umask, inherited group and default
  ACL of the destination directory. Staging remains inaccessible to other users.
- Copies validate every source entry, including nested files, before publication.
  Cross-filesystem moves check again before deleting the source. If that check or
  cleanup fails, both copies and a transfer journal remain for inspection.
- Downloads check the opened file's size and revision. The server withholds its
  final chunk until validation succeeds, so a detected concurrent modification
  fails the download rather than silently reporting success.
- Interrupted requests clean up staging. An uncatchable process termination can
  leave a private `.panasms-upload-*` or `.panasms-copy-*` directory; it is not a
  completed destination. Retry starts a new transfer, not a byte-range resume.

The test suite covers competing uploads, cancellation, disconnects, process kill,
source mutation, simulated storage exhaustion, bounded streaming and access under
separate Linux identities. Identity/ACL tests require root and `setfacl`; run them
only in a disposable test environment. They create temporary fixtures and do not
change existing user accounts. No live filesystem snapshot is provided: pause
external writers before moving actively edited data between filesystems. Power-loss
and physical media-removal qualification remain separate hardware acceptance work.

## Background file operations (0.2.17)

Requires core 0.2.7 or newer. Upload, copy, move and deletion batches continue
across SPA navigation and appear in Tasks and the top bar. Copy and move accept
multiple selected files and folders; their destination uses the shared folder tree.

Name collisions pause the batch for a decision in Tasks: replace, rename or skip.
A decision can apply to subsequent collisions in the same batch. Replacement
validates the destination revision and atomically exchanges a complete staged
copy with the old entry. If the filesystem cannot perform atomic exchange, the
operation fails without deleting the original. Replacing a directory replaces
its contents; it does not merge directories. Symbolic links and different entry
types cannot be replaced. Concurrent changes fail safely; inspect and retry.

The module publishes session-local snapshots in the host QueryClient under
`['file-uploads']`. Snapshots include kind, item/byte counters, current job ID,
status, errors, cancellation and an optional collision-resolution callback.
This in-memory contract must not be persisted or transmitted. File objects stay
exclusively in the queue. Individual failures do not block remaining items.

Cancellation stops uploads and skips remaining entries. Server operations are
cancelled when supported; otherwise the current entry finishes first. Clearing
the session cache stops scheduling further work. Reloading or closing the tab
interrupts uploads and loses the browser batch queue; an already submitted server
job can continue and remains visible in Tasks. A browser confirmation guards
accidental reloads. Resumable uploads and persistent batch queues are not implemented.

Run `npm test` for filesystem and queue tests. Tests cover route-independent
execution, multiple items, collisions, replacement races, progress, partial
failures, cancellation and session changes. Native identity/ACL tests also run
on the NAS before packaging.
