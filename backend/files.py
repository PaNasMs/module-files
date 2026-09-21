import transfer
import pwd
import grp
from contextlib import contextmanager, ExitStack
import stat
import signal
from common import *

ACTIONS = {
    "file.mkdir",
    "file.permissions",
    "file.rename",
    "file.copy",
    "file.move",
    "file.trash",
    "file.restore",
    "file.delete",
}


def identity(user):
    u = pwd.getpwnam(user)
    require(u.pw_uid > 0, "Invalid user")
    return u


def roots(user, mounts=None):
    u = identity(user)
    items = ["/"]
    if u.pw_dir.startswith(("/home/", "/srv/", "/mnt/")):
        items.append(u.pw_dir)
    if mounts is None:
        mounts = json_command(
            [
                "findmnt",
                "--json",
                "--list",
                "--real",
                "-o",
                "TARGET,SOURCE,FSTYPE,MAJ:MIN",
            ]
        )["filesystems"]
    for row in mounts:
        if (
            row.get("source", "").startswith("/dev/")
            and not (Path("/sys/dev/block") / row["maj:min"]).exists()
        ):
            continue
        if row["target"].startswith(("/srv/", "/mnt/", "/media/")) and row[
            "fstype"
        ] not in ("autofs",):
            items.append(row["target"])
    return sorted(set(items), key=lambda r: (-len(r), r))


def drop(user):
    u = identity(user)
    if os.geteuid() == 0:
        os.initgroups(u.pw_name, u.pw_gid)
        os.setgid(u.pw_gid)
        os.setuid(u.pw_uid)


def path(value, allowed, root_ok=False):
    p = clean_path(value)
    real = p.resolve()
    require(str(real) == str(p), "Symbolic links in the path are not supported")
    require(
        any((root_ok and real == Path(r)) or Path(r) in real.parents for r in allowed),
        "Path is outside accessible mounted storage",
    )
    return p


def mount_names():
    names = {}
    aliases = {
        str(alias.resolve()): alias.name
        for alias in sorted(Path("/dev/md").glob("*"), reverse=True)
    }

    def visit(device, model="", multiple=False):
        if device.get("type", "").startswith("raid"):
            multiple = False
            model = aliases.get(
                device.get("path") or "/dev/" + device["name"], device["name"]
            )
        else:
            model = (device.get("model") or "").strip() or model
        label = (device.get("label") or "").strip()
        name = model or label or device["name"]
        if model and multiple:
            name += " · " + (label or device["name"])
        for point in device.get("mountpoints") or []:
            if point:
                names[point] = name
        children = device.get("children", [])
        for child in children:
            visit(child, model, len(children) > 1)

    for device in json_command(
        ["lsblk", "--json", "--output", "NAME,PATH,TYPE,MODEL,LABEL,MOUNTPOINTS"]
    )["blockdevices"]:
        visit(device)
    return names


def permission_admin(user):
    account = identity(user)
    require(grp.getgrnam("sudo").gr_gid in os.getgrouplist(user, account.pw_gid), "Administrator permissions required")


@contextmanager
def permission_folder(user, value):
    permission_admin(user)
    require(isinstance(value, str) and value.startswith("/") and "\x00" not in value, "Invalid folder path")
    target = Path(value)
    require(str(target) == value and not any(part in (".", "..") for part in target.parts), "Invalid folder path")
    require(any(value.startswith(root) for root in ("/home/", "/srv/", "/mnt/", "/media/")), "Choose a home or data folder; system folders are protected")
    fd = os.open("/", os.O_RDONLY | os.O_DIRECTORY)
    try:
        try:
            for index, part in enumerate(target.parts[1:], 1):
                flags = os.O_RDONLY | os.O_NOFOLLOW | os.O_NONBLOCK
                if index < len(target.parts) - 1:
                    flags |= os.O_DIRECTORY
                nextfd = os.open(part, flags, dir_fd=fd)
                os.close(fd)
                fd = nextfd
        except OSError:
            raise Rejected("Folder unavailable or path contains a symbolic link")
        require(stat.S_ISDIR(os.fstat(fd).st_mode) or stat.S_ISREG(os.fstat(fd).st_mode), "Choose a regular file or folder")
        fs = json_command(["findmnt", "--json", "--target", value, "--output", "FSTYPE"])["filesystems"][0]["fstype"]
        require(fs in ("ext2", "ext3", "ext4", "xfs", "btrfs"), "Permissions can be edited only on local Linux filesystems")
        yield fd
    finally:
        os.close(fd)


def permission_revision(fd):
    s = os.fstat(fd)
    return fingerprint("file.permissions", {}, [s.st_dev, s.st_ino, s.st_uid, s.st_gid, s.st_mode, s.st_ctime_ns])


def permission_info(user, value):
    with permission_folder(user, value) as fd:
        s = os.fstat(fd)
        try:
            owner = pwd.getpwuid(s.st_uid).pw_name
        except KeyError:
            owner = str(s.st_uid)
        try:
            group = grp.getgrgid(s.st_gid).gr_name
        except KeyError:
            group = str(s.st_gid)
        attrs = os.listxattr(fd)
        return {"target": value, "directory": stat.S_ISDIR(s.st_mode), "owner": owner, "group": group, "mode": format(stat.S_IMODE(s.st_mode), "04o"),
                "revision": permission_revision(fd), "acl": "system.posix_acl_access" in attrs,
                "defaultAcl": "system.posix_acl_default" in attrs}


def permission_values(p):
    require(isinstance(p.get("mode"), str) and re.fullmatch(r"[0-3][0-7]{3}", p["mode"]), "Invalid permissions")
    try:
        owner = pwd.getpwuid(int(p["owner"])) if p["owner"].isdigit() else pwd.getpwnam(p["owner"])
        group = grp.getgrgid(int(p["group"])) if p["group"].isdigit() else grp.getgrnam(p["group"])
    except (KeyError, ValueError, AttributeError):
        raise Rejected("Unknown owner or group")
    return owner.pw_uid, group.gr_gid, int(p["mode"], 8)


def permission_selection(user, targets):
    permission_admin(user)
    require(isinstance(targets, list) and 0 < len(targets) <= 100 and all(isinstance(t, str) for t in targets) and len(set(targets)) == len(targets), "Invalid selection")
    return {"items": [permission_info(user, t) for t in targets],
            "users": sorted([u.pw_name for u in pwd.getpwall()]),
            "groups": sorted([g.gr_name for g in grp.getgrall()])}


@contextmanager
def permission_batch(p, user):
    permission_admin(user)
    items = p.get("items")
    require(isinstance(items, list) and 0 < len(items) <= 100 and all(isinstance(i, dict) and isinstance(i.get("target"), str) for i in items), "Invalid selection")
    require(len({i["target"] for i in items}) == len(items), "Invalid selection")
    mask = p.get("mask", 0)
    bits = p.get("bits", 0)
    require(type(mask) is int and type(bits) is int and 0 <= mask <= 0o2777 and mask & ~0o2777 == 0 and bits & ~mask == 0, "Invalid permissions")
    uid = gid = -1
    try:
        if p.get("owner"):
            uid = pwd.getpwnam(p["owner"]).pw_uid
        if p.get("group"):
            gid = grp.getgrnam(p["group"]).gr_gid
    except (KeyError, TypeError):
        raise Rejected("Unknown owner or group")
    with ExitStack() as stack:
        changes = []
        for item in items:
            fd = stack.enter_context(permission_folder(user, item["target"]))
            require(item.get("revision") == permission_revision(fd), "Folder permissions changed. Reopen the dialog and try again.")
            info = os.fstat(fd)
            effective_mask = mask if stat.S_ISDIR(info.st_mode) else mask & ~0o2000
            mode = (stat.S_IMODE(info.st_mode) & ~effective_mask) | (bits & effective_mask)
            require(not mode & 0o4000, "Setuid files are protected")
            changes.append((fd, uid, gid, mode))
        yield changes


def permission_plan(p, user):
    if "items" in p:
        with permission_batch(p, user):
            return {"target": p["items"][0]["target"], "details": [i["target"] for i in p["items"]],
                    "confirmation": p["items"][0]["target"], "fingerprint": fingerprint("file.permissions", p, [])}
    with permission_folder(user, p.get("target")) as fd:
        revision = permission_revision(fd)
        require(p.get("revision") == revision, "Folder permissions changed. Reopen the dialog and try again.")
        permission_values(p)
        return {"target": p["target"], "details": [p["target"], "Apply only to the selected folder, without recursion"],
                "confirmation": p["target"], "fingerprint": fingerprint("file.permissions", p, revision)}


def permission_apply(p, user):
    if "items" in p:
        with permission_batch(p, user) as changes:
            for fd, uid, gid, mode in changes:
                if uid != -1 or gid != -1:
                    os.fchown(fd, uid, gid)
                os.fchmod(fd, mode)
        return {"message": "Ownership and permissions updated"}
    with permission_folder(user, p.get("target")) as fd:
        require(p.get("revision") == permission_revision(fd), "Folder permissions changed. Reopen the dialog and try again.")
        uid, gid, mode = permission_values(p)
        os.fchown(fd, uid, gid)
        os.fchmod(fd, mode)
    return {"message": "Folder ownership and permissions updated"}


def query(user, target):
    if target and target.startswith("permissions-selection:"):
        permission_admin(user)
        return permission_selection(user, json.loads(target[len("permissions-selection:"):]))
    if target and target.startswith("permissions:"):
        return permission_info(user, target[len("permissions:"):])
    mounts = json_command(
        ["findmnt", "--json", "--list", "--real", "-o", "TARGET,SOURCE,FSTYPE,MAJ:MIN"]
    )["filesystems"]
    allowed = roots(user, mounts)
    network = {
        row["target"]
        for row in mounts
        if row.get("fstype") in ("nfs", "nfs4", "cifs", "smb3")
    }
    drop(user)
    names = mount_names()
    home = identity(user).pw_dir
    places = []
    trash_roots = []
    for r in allowed:
        places.append(
            {
                "path": r,
                "name": (
                    "System"
                    if r == "/"
                    else "Home folder" if r == home else names.get(r, Path(r).name)
                ),
                "kind": "home" if r == home else "device",
                **({"network": True} if r in network else {}),
            }
        )
        if not os.access(r, os.R_OK | os.X_OK):
            continue
        trash = Path(r) / (".panasms-trash-" + str(os.getuid()))
        if (
            trash.is_dir()
            and not trash.is_symlink()
            and os.access(trash, os.R_OK | os.X_OK)
        ):
            trash_roots.append(str(trash))
    places.append({"path": "trash:", "name": "Trash", "kind": "trash"})
    result = {
        "roots": allowed,
        "places": places,
        "trashRoots": trash_roots,
        "entries": [],
    }
    if not target:
        return result
    if target == "trash:":
        for folder in trash_roots:
            for item in Path(folder).iterdir():
                container = (
                    item.is_dir()
                    and not item.is_symlink()
                    and re.fullmatch(r"\d{19,}-[a-z0-9_]{8}", item.name)
                )
                for child in item.iterdir() if container else [item]:
                    require(len(result["entries"]) < 10000, "Too many items in trash")
                    try:
                        s = child.lstat()
                        label = (
                            child.name
                            if container
                            else re.sub(r"^\d+-", "", child.name)
                        )
                        result["entries"].append(
                            {
                                "name": label,
                                "path": str(child),
                                "directory": child.is_dir() and not child.is_symlink(),
                                "link": child.is_symlink(),
                                "size": s.st_size,
                                "modified": s.st_mtime,
                            }
                        )
                    except OSError:
                        continue
        return {**result, "path": target}
    require(
        os.access(target, os.R_OK | os.X_OK),
        "Permission denied to view this folder: " + target,
    )
    p = path(target, allowed, True)
    require(p.is_dir(), "Directory unavailable")
    require(os.access(p, os.R_OK | os.X_OK), "Permission denied to view this folder")
    entries = []
    for child in p.iterdir():
        if len(entries) >= 10000:
            raise Rejected("Too many items; open a subdirectory")
        try:
            s = child.lstat()
            entries.append(
                {
                    "name": child.name,
                    "path": str(child),
                    "directory": child.is_dir() and not child.is_symlink(),
                    "link": child.is_symlink(),
                    "size": s.st_size,
                    "modified": s.st_mtime,
                }
            )
        except OSError:
            continue
    space = os.statvfs(p)
    return {
        **result,
        "path": str(p),
        "freeBytes": space.f_bavail * space.f_frsize,
        "entries": sorted(
            entries, key=lambda e: (not e["directory"], e["name"].lower())
        ),
    }


def plan(action, p, user):
    if action == "file.permissions":
        return permission_plan(p, user)
    require(action in ACTIONS, "Unknown file operation")
    allowed = roots(user)
    drop(user)
    target = path(p.get("target"), allowed)
    if action == "file.mkdir":
        require(not target.exists(), "Already exists")
        state = {
            "parent": str(target.parent),
            "mtime": target.parent.stat().st_mtime_ns,
        }
    else:
        require(target.exists(), "Item does not exist")
        s = target.stat()
        state = {
            "inode": s.st_ino,
            "device": s.st_dev,
            "mtime": s.st_mtime_ns,
            "size": s.st_size,
        }
        if action in ("file.copy", "file.move", "file.rename", "file.restore"):
            destination = path(p.get("destination"), allowed)
            require(not destination.exists(), "Destination already exists")
            require(
                target not in destination.parents, "Cannot move a folder into itself"
            )
            state["parent"] = destination.parent.stat().st_mtime_ns
    return {
        "target": str(target),
        "details": [
            str(target),
            p.get("destination", ""),
            (
                "Permanent deletion"
                if action == "file.delete"
                else "The operation uses your Linux permissions"
            ),
        ],
        "confirmation": str(target),
        "fingerprint": fingerprint(action, p, state),
    }


def execute(action, p, user):
    if action == "file.permissions":
        return permission_apply(p, user)
    allowed = roots(user)
    drop(user)
    target = path(p["target"], allowed)
    if action == "file.mkdir":
        target.mkdir()
    elif action == "file.delete":
        if target.is_dir():
            shutil.rmtree(target)
        else:
            target.unlink()
    elif action == "file.trash":
        base = next(Path(r) for r in allowed if Path(r) in target.parents)
        trash = base / (".panasms-trash-" + str(os.getuid()))
        require(not trash.is_symlink(), "Invalid trash folder")
        trash.mkdir(mode=0o700, exist_ok=True)
        require(
            trash.stat().st_uid == os.getuid(), "This trash belongs to another user"
        )
        container = Path(tempfile.mkdtemp(prefix=str(time.time_ns()) + "-", dir=trash))
        destination = container / target.name
        try:
            transfer.transfer(target, destination, move=True)
        except Exception:
            if not any(container.iterdir()):
                container.rmdir()
            raise
        return {
            "message": "Moved to trash",
            "path": str(destination),
            "original": str(target),
        }
    else:
        destination = path(p["destination"], allowed)
        transfer.transfer(target, destination, move=action != 'file.copy')
    return {"message": "File operation complete"}


def download(target, output):
    fd = os.open(target, os.O_RDONLY | os.O_NOFOLLOW | os.O_NONBLOCK)
    with os.fdopen(fd, "rb") as source:
        info = os.fstat(fd)
        require(stat.S_ISREG(info.st_mode), "Not a regular file")
        output.write(f"OK {info.st_size}\n".encode())
        output.flush()
        shutil.copyfileobj(source, output, 1024 * 1024)


if __name__ == "__main__":
    import sys

    def interrupted(*_):
        raise InterruptedError("transfer interrupted")

    signal.signal(signal.SIGTERM, interrupted)
    try:
        mode, user, target = sys.argv[1:4]
        expected = int(sys.argv[4]) if len(sys.argv) > 4 else -1
        allowed = roots(user)
        drop(user)
        p = path(target, allowed)
        if mode == "thumbnail":
            from thumbnails import render

            thumbnail = render(p)
            sys.stdout.buffer.write(f"OK {len(thumbnail)}\n".encode() + thumbnail)
            sys.stdout.buffer.flush()
        elif mode == "download":
            download(p, sys.stdout.buffer)
        elif mode == "upload":
            require(not p.exists(), "File already exists")
            fd, tmp = tempfile.mkstemp(prefix=".panasms-upload-", dir=p.parent)
            try:
                with os.fdopen(fd, "wb") as f:
                    count = 0
                    while True:
                        chunk = sys.stdin.buffer.read(1024 * 1024)
                        if not chunk:
                            break
                        f.write(chunk)
                        count += len(chunk)
                    require(expected >= 0 and count == expected, "Incomplete transfer")
                    f.flush()
                    os.fsync(f.fileno())
                os.link(tmp, p, follow_symlinks=False)
            finally:
                Path(tmp).unlink(missing_ok=True)
            print("OK")
    except Exception:
        sys.exit(1)
