# PaNasMs Files module

Files is the file manager module for [PaNasMs](https://github.com/PaNasMs/panasms),
a browser panel for managing a NAS on Debian-based Linux. It browses local storage,
mounted devices and linked Google Drive or Dropbox accounts, and runs uploads,
copies, moves and deletions as background tasks. Project website:
<https://panasms.github.io/>.

The current version is 0.3.13. It requires PaNasMs core `>=0.2.15,<0.3.0` and
module API 1, and is published for ARM64 and AMD64.

## Install

Open **Modules** in the PaNasMs panel and install Files from the catalog. The
module manager picks the package for your architecture. Signed packages and the
catalog are in the [module registry](https://panasms.github.io/module-registry/);
an administrator can also upload a signed `.panasms` archive from there.

## Features

- A sidebar with storage devices, expandable folder trees, pinned folders, linked
  cloud accounts and Trash. Removable volumes mount automatically.
- List and tile views. Tiles show lazy image thumbnails for local files and
  file-type icons for everything else.
- Upload, download, create, rename, copy, move, trash, restore and permanently
  delete files and folders, including mixed multi-selections with one
  confirmation and per-item error reporting.
- Drag and drop with a choice of copy or move, and of skip, rename or replace for
  matching names.
- Administrator editing of ownership and permissions for the current folder or
  selected entries.

The UI is available in English, Russian and Ukrainian.

## Access model

Panel administrators and ordinary panel accounts that an administrator has
enabled can use Files. The server checks the account through the SDK's
`LookupPanel`. Every ordinary file operation runs with the selected Linux user's
UID, GID and supplementary groups, so a user sees only what that Linux account can
access. Installing Files does not grant access to other users' homes. Only
permission editing runs as root, and only for administrators.

New files and directories use the system `UMASK` from `/etc/login.defs` (`022`
if unset) instead of the service's private mask. Ownership stays with the Linux
user who ran the operation, and parent setgid bits and default ACLs still apply.
Existing files are not changed. Private module state and credentials keep
restrictive permissions. Terminal startup scripts can set a different mask for
shell sessions.

Disk, filesystem and mount management belong to the core Storage page, not to
this module.

## Background operations

Upload, copy, move and deletion batches keep running when you navigate to
another page. They appear in Tasks and in the top bar with item and byte
progress. A failed item does not stop the rest of the batch.

When a name collides, the batch pauses and Tasks asks whether to replace, rename
or skip. The answer can apply to later collisions in the same batch. Folder copies
and moves merge into a matching destination folder and leave unrelated files
there in place. Replacing a single entry checks the destination revision, then
swaps in a complete staged copy with an atomic exchange. If the filesystem cannot
do an atomic exchange, the operation fails and the original stays. Symbolic links
and entries of a different type cannot be replaced.

Cancelling stops uploads and skips remaining entries. The server cancels its
current operation where it can; otherwise the current entry finishes first.
Reloading or closing the tab interrupts browser uploads and drops the browser
queue. A server job that was already submitted keeps running and stays visible in
Tasks. The browser asks for confirmation before a reload. Resumable uploads and a
persistent batch queue are not implemented.

The module keeps session-local batch snapshots in the host QueryClient under
`['file-uploads']`. This in-memory contract must not be persisted or sent
anywhere, and `File` objects stay inside the queue.

## Transfer integrity

- Uploads are read in 1 MiB chunks into a private staging directory next to the
  destination. Only a complete, synced upload is published, and a competing
  upload cannot overwrite it.
- Copies validate every source entry, including nested files, before they publish
  the destination. A cross-filesystem move checks the copy again before it deletes
  the source. If that check or the cleanup fails, both copies and a transfer
  journal remain for inspection.
- Downloads check the open file's size and revision. The server holds back the
  last chunk until that check passes, so a file changed during the download fails
  the download instead of reporting success.
- An interrupted request removes its staging directory. A process killed with
  `SIGKILL` can leave a private `.panasms-copy-*` directory on local storage, or a
  `.panasms-upload-*` or `.panasms-transfer-*` entry in a cloud account. It is
  never a completed destination. A retry starts a new transfer; there is no
  byte-range resume.
- There is no live filesystem snapshot. Stop other writers before moving data
  that is being edited across filesystems.

Power-loss and physical media-removal testing is separate hardware acceptance
work and is not covered by this repository's tests.

## Cloud storage

Link a Google or Dropbox account in the panel profile, then choose **Connect
cloud storage** in Files. Files has its own file-access grant, separate from Cloud
Sync. If the account already has a matching provider authorization, Files reuses
it without another provider redirect. Modules installed later still need their own
local grant, and the provider is asked for consent only for permissions it has not
given yet. Each account is a separate sidebar place, and several accounts per
provider work through independent grants. OAuth tokens come from the core grant
broker through inherited private descriptors; they never reach browser code and
are not stored in module files. Cloud transfers run through rclone under the
requesting Linux identity.

- Copies between locations stream through the NAS. Moves verify the copied
  content before removing the source, and skipped files stay in the source.
- Interrupted operations are not replayed. Check the task and any retained copies
  before retrying.
- Google Drive paths with duplicate names are rejected instead of picking one of
  the objects.
- Google Docs, Sheets and Slides are exported to a private temporary file on
  download (up to 2 GiB) and sent with their real size.
- Dropbox downloads can use a short-lived provider link so the browser downloads
  directly from Dropbox. Other providers stream through the NAS.
- Cloud places show file-type icons, not thumbnails. Permission editing and
  SMB/NFS publication apply only to local storage.

## Sidebar bookmarks

Use the pin action for the current folder, or right-click a folder and choose
**Pin to sidebar**. Pinned local and cloud folders expand like other trees and
accept dropped files. Unpinning removes only the bookmark. Bookmarks are stored as
folder paths in the user's NAS preferences, so they follow the user across reloads
and devices. A bookmark to an unavailable location can still be removed.

## Trash

Moving an item to Trash puts it at
`<storage root>/.panasms-trash-<uid>/<id>/<name>`, where `<id>` is the deletion
time in nanoseconds plus a random suffix. Next to the item, the module atomically
writes `.panasms-trash-info.json` (`{"version":1,"original":"<absolute path>"}`)
as the requesting Linux user.

The Trash view shows each item's original folder and deletion time. **Restore**
returns selected items to their original folders with the usual name-collision
choices. **Restore to...** suggests the original folder as the destination. A
`file.restore` request without a destination restores to the recorded path and
fails with a clear error if that path is unknown, taken or missing.

The module treats the note as untrusted input. It reads the note without
following links and uses it only if it is a small regular file owned by the user
that names the same item with a clean absolute path on the storage root holding
that trash. Items trashed by versions before the note existed have no original
location; restore them with **Restore to...**. After an item is restored or
permanently deleted, its `<id>` folder is removed if only the note remains. No
other folders are swept.

## Native operation contract

The manifest entry `operations: bin/server` opts into the core native-module
dispatcher. The core checks panel access and operation confirmation, then runs the
executable as `operations MODE USER` with a JSON request on stdin. Results are
JSON on stdout. Progress and cancellation messages use stderr and
`PANASMS_CONTROL_FD`. Ordinary operations re-exec under the Linux user's UID,
primary group and supplementary groups. Administrator permission edits keep root
privileges and use pinned no-follow file descriptors and revision checks. The
package contains no Python runtime.

## Development

The UI is React and TypeScript built with Vite against the host-provided UI
contracts. Do not bundle a second copy of the host React, router or query
runtime. The server, file operations, permissions and thumbnails are Go, built on
the pinned [module SDK](https://github.com/PaNasMs/module-sdk).

| Path | Contents |
| --- | --- |
| `frontend/` | UI, background queue (`uploads.ts`) and `locales/` (`en`, `ru`, `uk`) |
| `cmd/server/` | Module service and native operation entry point |
| `internal/localfs/` | Local file operations, staging, trash and thumbnails |
| `internal/cloudfs/` | rclone-based cloud access |
| `internal/operations/` | Native operation handlers |
| `scripts/` | `build.sh`, translation check and payload packaging |
| `tests/` | Node tests for the background queue |

You need Linux on the target architecture (ARM64 or AMD64), Node.js 24, Go 1.26
or newer, Python 3, a C compiler and `libpam0g-dev`. CI uses Go 1.27.1. To build:

```sh
sh scripts/build.sh
```

The script runs `npm ci`, builds the UI, runs `go test -tags pam ./...`, builds
`dist/bin/server`, runs `npm test` and writes
`dist/files-<version>-<arch>.unsigned.zip`. The architecture comes from
`go env GOARCH`, and packaging fails if the server binary does not match it, so
build on the architecture you are packaging for. The unsigned payload cannot be
installed directly.

To run checks separately:

- `go test -tags pam ./...` runs the native file-operation tests.
- `npm test` checks that `ru` and `uk` have the same translation keys as `en` and
  runs the background-queue tests.

The tests cover competing uploads, cancellation, disconnects, process kill,
source changes during a transfer, simulated full storage, bounded streaming,
collisions, replacement races and partial failures. The identity and ACL tests
need root and `setfacl`. Run them only in a disposable test environment. They
create temporary fixtures and do not change existing user accounts.

## Release

Update the version in `manifest.json`, `package.json` and `package-lock.json`
together, commit, then push a matching `vX.Y.Z` tag. The
[build workflow](.github/workflows/build.yml) builds and tests both architectures
on every push to `main` and on pull requests. Only a version tag publishes a
GitHub release with the two unsigned payloads, and packaging fails if the tag does
not match the manifest version. The module registry imports and signs the
payloads, publishes the installable archives and updates the catalog. Signing keys
are not stored in this repository. Publish a new version instead of replacing an
existing release.

## License

Original code is licensed under
[PolyForm Noncommercial 1.0.0](LICENSE). See [NOTICE](NOTICE) for the scope of
the license and for third-party components.
