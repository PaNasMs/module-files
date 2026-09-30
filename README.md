# PaNasMs Files module

Installable file manager for PaNasMs. Current manifest version: **0.3.0**.
Requires core `>=0.2.9,<0.3.0`, module API 1 and ARM64 Linux.

## Features

- Folder/device sidebar with expandable trees and automatic removable-volume mounting.
- Local and network-mounted locations, Google Drive and Dropbox accounts, folder navigation and list/tile views.
- Background upload, copy, move and deletion queue with progress/cancellation in the shell task menu, plus a drop menu for copying/moving with skip, rename or replace conflict policies. Matching folders merge without removing unrelated destination files.
- Trash, restore and permanent deletion, including mixed multi-selection of files and folders with one confirmation and per-item failures.
- File-type icons and lazy image thumbnails in tile view.
- Administrator permission editing for the current folder or selected entries.

File operations run under the requesting Linux identity where appropriate;
administrative ownership/permission changes are validated by the host. Filesystem
and mount management remain responsibilities of the core storage subsystem.

## Development

The frontend uses React/TypeScript and host-provided UI contracts. The server uses
Go and the pinned [module SDK](https://github.com/PaNasMs/module-sdk). Runtime file operations, permissions and thumbnail generation are implemented in Go. Cloud transport uses rclone under the requesting Linux identity; OAuth tokens come from the core grant broker through private inherited descriptors. Do not bundle another copy of the
host React/router/query runtime.

Use ARM64 Linux, Node.js 24, Go 1.26 or newer, Python 3, a C compiler and
`libpam0g-dev`. The release workflow pins Go 1.27.1. From this repository:

```sh
sh scripts/build.sh
```

This installs locked npm dependencies, builds the UI, runs PAM-enabled Go tests,
builds the server, checks translation keys and queue tests, then writes `dist/<id>-<version>-arm64.unsigned.zip`. This is an unsigned
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
Run `go test ./...` for native operation tests and `npm test` for locale and background-queue checks.

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

### Permissions of user files

New user files and directories use the system `UMASK` from `/etc/login.defs`
(`022` if unset), rather than the private service mask. Ownership remains with
the Linux user running the operation. Parent-directory setgid and default ACLs
still apply; existing files are not changed. Private module state and credentials
retain restrictive permissions. Terminal startup scripts can override the initial
shell mask.


## Cloud storage

Link Google/Dropbox accounts in the profile, then select **Connect cloud storage**
in Files. File access is granted to the `files` consumer independently of Cloud
Sync, but an existing matching account authorization is reused without another
provider redirect. When linking a new account, enable file access once; modules
installed later still require an explicit local grant and request provider consent
only for permissions not yet available. Each account appears as its own expandable place; tokens are never returned
to browser code or stored in module files. Multiple accounts per provider work
through independent grants.

Cross-location copies stream through the NAS. Moves verify copied content before
removing the source. Interrupted operations are not replayed automatically;
review the background task and retained copies before retrying. Destination
folders merge; skipped source files remain in place during a move.

Google Drive duplicate names are rejected rather than selecting an arbitrary
object. Provider-native documents with no downloadable byte size must be exported
in the provider first. Cloud previews currently fall back to file-type icons.
Permissions and SMB/NFS publication apply only to local storage.

## Native operation contract

`operations: bin/server` opts into the core native-module dispatcher. The core
validates panel access and operation confirmation, then invokes the executable
with `operations MODE USER` and a JSON request on stdin. Results are JSON on stdout;
progress/cancellation messages use stderr and `PANASMS_CONTROL_FD`. Ordinary file
operations re-exec under the Linux user's UID, primary and supplementary groups.
Only administrator permission edits retain root privileges, using pinned no-follow
file descriptors and revision checks. The archive contains no Python runtime.

## Sidebar bookmarks

Use the pin action for the current folder, or right-click a folder and choose
**Pin to sidebar**. Pinned local and cloud folders expand using the same tree and
accept file drops. The unpin action removes only the bookmark, never the folder.
Bookmarks are stored in the current user's NAS preferences and survive reloads
and device changes. They reference folder paths; unavailable locations remain
removable from the sidebar. Connected clouds appear above Trash.
