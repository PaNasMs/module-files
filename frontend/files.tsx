import { request, type Identity } from "@panasms/client";
import { DialogContent, WaitingSurface } from "@panasms/ui";
import { useQueryValue } from "@panasms/navigation";
import { useNavigate } from "react-router-dom";
import { tr, locale } from "./i18n";
import { FolderPermissions, PermissionsMenuAction } from "./folder-permissions";
import { FileVisual } from "./file-visual";
import {
  deviceVolumes,
  singleVolume,
  useVolumeAccess,
  volumeMountParams,
  type Removable,
} from "@panasms/removable";
import { registerModule } from "@panasms/runtime";
import {
  mdiUsbFlashDrive,
  mdiMicroSd,
  mdiFolderOutline,
  mdiHomeOutline,
  mdiHarddisk,
  mdiFolderNetworkOutline,
  mdiTrashCanOutline,
  mdiArrowLeft,
  mdiArrowRight,
  mdiArrowUp,
  mdiRefresh,
  mdiViewGridOutline,
  mdiFormatListBulleted,
  mdiFolderPlusOutline,
  mdiUpload,
  mdiDownload,
  mdiEyeOutline,
  mdiPencilOutline,
  mdiContentCopy,
  mdiContentCut,
  mdiRestore,
  mdiDeleteOutline,
  mdiShareVariantOutline,
  mdiClose,
  mdiEyeOffOutline,
  mdiChevronRight,
  mdiCheckboxMultipleMarkedOutline,
} from "@mdi/js";
import * as Dialog from "@radix-ui/react-dialog";
import { useEffect, useRef, useState, type DragEvent } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { managed, OperationButton, type Job } from "@panasms/operations";
import { newID } from "@panasms/layout";
import { waitForJob } from "@panasms/completion";
import { Button, Icon, Notice, bytes } from "@panasms/ui";
type Entry = {
  name: string;
  path: string;
  directory: boolean;
  link: boolean;
  size: number;
  modified: number;
};
type Place = {
  name: string;
  path: string;
  kind: "home" | "device" | "trash";
  network?: boolean;
};
type Listing = {
  roots: string[];
  places: Place[];
  path?: string;
  entries: Entry[];
  trashRoots?: string[];
  freeBytes?: number;
};
export function FilesPage() {
  const session = useQuery({ queryKey: ["session"], queryFn: () => request<Identity>("session") });
  const access = useVolumeAccess();
  const q = useQueryClient();
  const browserNavigate = useNavigate();
  const [path, setPath] = useQueryValue("path");
  const [progress, setProgress] = useState<number | null>(null);
  const [uploadLabel, setUploadLabel] = useState("");
  const [dragging, setDragging] = useState(false);
  const [error, setError] = useState("");
  const [sort, setSort] = useQueryValue("sort", "name", [
    "name",
    "size",
    "modified",
  ]);
  const [preview, setPreview] = useState<Entry | null>(null);
  const [view, setView] = useQueryValue("view", "list", ["list", "grid"]);
  const [hiddenValue, setHiddenValue] = useQueryValue("hidden", "0", [
    "0",
    "1",
  ]);
  const hidden = hiddenValue === "1";
  const setHidden = (value: boolean) => setHiddenValue(value ? "1" : "0");
  const [permissionTargets, setPermissionTargets] = useState<string[] | null>(
    null,
  );
  const [focusedPath, setFocusedPath] = useState("");
  const typeahead = useRef({ text: "", time: 0 });
  const [selection, setSelection] = useState<string[]>([]);
  const [editing, setEditing] = useState(false);
  const [address, setAddress] = useState("");
  const [menu, setMenu] = useState<{
    x: number;
    y: number;
    kind: "folder" | "item";
  } | null>(null);
  const menuRef = useRef<HTMLDivElement>(null);
  const moveLock = useRef(false);
  const dragged = useRef<Entry[]>([]);
  const [dropTarget, setDropTarget] = useState("");
  const [moveStatus, setMoveStatus] = useState("");
  const uploading = useRef(false);
  const dragDepth = useRef(0);
  const uploadInput = useRef<HTMLInputElement>(null);
  const addressInput = useRef<HTMLInputElement>(null);
  const anchor = useRef<string | null>(null);
  const deviceView = path.startsWith("device:");
  const devicesQuery = useQuery({
    queryKey: ["management-storage", "all"],
    queryFn: () =>
      managed<{
        devices: Removable[];
      }>("storage-options"),
    refetchInterval: 10000,
  });
  const devices = devicesQuery.data?.devices ?? [];
  const removable = devices.filter((d) => d.ejectable);
  const selectedDevice = removable.find((d) => "device:" + d.path === path);
  const data = useQuery({
    enabled: !deviceView,
    queryKey: ["files", path],
    queryFn: () => managed<Listing>("files", undefined, path),
    refetchInterval: 10000,
  });
  const locations = useQuery({
    queryKey: ["file-places"],
    queryFn: () => managed<Listing>("files"),
    refetchInterval: 10000,
  });
  const allPlaces = (locations.data?.places ?? data.data?.places ?? []).map(
    (place) => ({
      ...place,
      name:
        place.kind === "home"
          ? tr("server_f76b7ba107bc")
          : place.kind === "trash"
            ? tr("trash_d9e801f1")
            : place.path === "/"
              ? tr("server_3ac98f278c8c")
              : place.name,
    }),
  );
  const removablePoints = new Set(
    removable.flatMap((d) =>
      deviceVolumes(d, devices).flatMap((v) => v.mountpoints ?? []),
    ),
  );
  const places = allPlaces.filter((p) => !removablePoints.has(p.path));
  const roots = locations.data?.roots ?? data.data?.roots ?? [];
  const root = [...roots]
    .sort((a, b) => b.length - a.length)
    .find((r) => path === r || path.startsWith(r.replace(/\/$/, "") + "/"));
  const entries = [...(data.data?.entries ?? [])]
    .filter((e) => path === "trash:" || hidden || !e.name.startsWith("."))
    .sort(
      (a, b) =>
        Number(b.directory) - Number(a.directory) ||
        (sort === "name"
          ? a.name.localeCompare(b.name, locale(), { numeric: true })
          : sort === "size"
            ? b.size - a.size
            : b.modified - a.modified),
    );
  const selected = entries.filter((e) => selection.includes(e.path));
  const single = selected.length === 1 ? selected[0] : undefined;
  const trashRoot = (
    data.data?.trashRoots ??
    locations.data?.trashRoots ??
    []
  ).find((p) => path === p || path.startsWith(p + "/"));
  const inTrash = path === "trash:" || !!trashRoot;
  async function prepareVolume(volume: Removable) {
    const point = await access.open(volume);
    await Promise.all([
      q.invalidateQueries({ queryKey: ["file-places"] }),
      q.invalidateQueries({ queryKey: ["files"] }),
      q.invalidateQueries({ queryKey: ["management-storage"] }),
      q.invalidateQueries({ queryKey: ["jobs"] }),
    ]);
    return point;
  }
  async function enterVolume(volume: Removable) {
    try {
      navigate(await prepareVolume(volume));
    } catch (e) {
      setError((e as Error).message);
    }
  }
  function enterDevice(device: Removable) {
    const single = singleVolume(device, devices);
    if (single) void enterVolume(single);
    else navigate("device:" + device.path);
  }
  function navigate(next: string) {
    if (path !== next) setPath(next);
    setEditing(false);
  }
  useEffect(() => {
    setSelection([]);
    setPreview(null);
    setEditing(false);
    anchor.current = null;
    setMenu(null);
    setError("");
    setAddress(path);
  }, [path]);
  useEffect(() => {
    if (editing) addressInput.current?.select();
  }, [editing]);
  useEffect(() => {
    if (!menu) return;
    const origin = document.activeElement instanceof HTMLElement ? document.activeElement : null;
    menuRef.current?.focus();
    const popup = menuRef.current;
    const close = () => setMenu(null);
    window.addEventListener("click", close);
    window.addEventListener("resize", close);
    return () => {
      window.removeEventListener("click", close);
      window.removeEventListener("resize", close);
      if (origin?.isConnected && (document.activeElement === document.body || popup?.contains(document.activeElement))) origin.focus();
    };
  }, [menu]);
  function choose(
    e: Entry,
    event: {
      ctrlKey: boolean;
      metaKey: boolean;
      shiftKey: boolean;
    },
  ) {
    if (event.shiftKey && anchor.current) {
      const from = entries.findIndex((v) => v.path === anchor.current),
        to = entries.indexOf(e);
      setSelection(
        entries
          .slice(Math.max(0, Math.min(from, to)), Math.max(from, to) + 1)
          .map((v) => v.path),
      );
    } else if (event.ctrlKey || event.metaKey)
      setSelection((s) =>
        s.includes(e.path) ? s.filter((p) => p !== e.path) : [...s, e.path],
      );
    else setSelection([e.path]);
    anchor.current = e.path;
  }
  function open(e: Entry) {
    if (e.link) return;
    if (e.directory) navigate(e.path);
    else setPreview(e);
  }
  const canUpload =
    !!path &&
    !deviceView &&
    !inTrash &&
    !data.error &&
    !data.isPending &&
    !uploading.current &&
    !moveLock.current;
  useEffect(() => {
    const prevent = (e: globalThis.DragEvent) => {
      if (e.dataTransfer?.types.includes("Files")) e.preventDefault();
    };
    window.addEventListener("dragover", prevent);
    window.addEventListener("drop", prevent);
    return () => {
      window.removeEventListener("dragover", prevent);
      window.removeEventListener("drop", prevent);
    };
  }, []);
  async function upload(files: File[]) {
    if (!canUpload || !files.length) return;
    const destination = path;
    uploading.current = true;
    setError("");
    setProgress(0);
    const failures: string[] = [];
    try {
      for (const [index, file] of files.entries()) {
        setUploadLabel(
          `${index + 1}/${files.length} · ${file.name} → ${destination}`,
        );
        setProgress(0);
        try {
          await new Promise<void>((resolve, reject) => {
            const xhr = new XMLHttpRequest();
            xhr.open(
              "PUT",
              "/api/v1/files/content?target=" +
                encodeURIComponent(
                  destination.replace(/\/$/, "") + "/" + file.name,
                ),
            );
            xhr.setRequestHeader("X-PaNasMs-Request", "1");
            xhr.upload.onprogress = (e) => {
              if (e.lengthComputable)
                setProgress(Math.round((e.loaded / e.total) * 100));
            };
            xhr.onload = () =>
              xhr.status === 204
                ? resolve()
                : reject(
                    Error(
                      tr(
                        "check_permissions_and_ensure_the_file_name_is_not__5eb4d1e2",
                      ),
                    ),
                  );
            xhr.onerror = () =>
              reject(Error(tr("transfer_interrupted_74125ab8")));
            xhr.onabort = () =>
              reject(Error(tr("transfer_cancelled_40cfcf6a")));
            xhr.send(file);
          });
        } catch (e) {
          failures.push(`${file.name}: ${(e as Error).message}`);
        }
      }
    } finally {
      uploading.current = false;
      setProgress(null);
      setUploadLabel("");
      setError(failures.join("; "));
      void q.invalidateQueries({ queryKey: ["files"] });
    }
  }
  function dropFiles(e: DragEvent<HTMLDivElement>) {
    if (!e.dataTransfer.types.includes("Files")) return;
    e.preventDefault();
    e.stopPropagation();
    dragDepth.current = 0;
    setDragging(false);
    if (!canUpload) {
      setError(
        uploading.current
          ? tr("wait_for_the_current_upload_to_finish_8b92a1d9")
          : tr("open_an_accessible_folder_to_upload_files_b4cfcaa0"),
      );
      return;
    }
    if (
      Array.from(e.dataTransfer.items).some(
        (item) => item.webkitGetAsEntry?.()?.isDirectory,
      )
    ) {
      setError(
        tr("drag_files_individually_folder_uploads_are_not_sup_7fb9a618"),
      );
      return;
    }
    void upload(Array.from(e.dataTransfer.files));
  }
  function canMoveTo(destination: string) {
    return (
      !!destination &&
      !inTrash &&
      !moveLock.current &&
      !uploading.current &&
      dragged.current.length > 0 &&
      dragged.current.every(
        (item) =>
          !item.link &&
          item.path !== destination &&
          !destination.startsWith(item.path + "/") &&
          item.path !== destination.replace(/\/$/, "") + "/" + item.name,
      )
    );
  }
  function startMove(event: DragEvent, entry: Entry) {
    const items = selection.includes(entry.path) ? selected : [entry];
    if (
      inTrash ||
      moveLock.current ||
      uploading.current ||
      items.some((item) => item.link)
    ) {
      event.preventDefault();
      return;
    }
    dragged.current = items;
    setSelection(items.map((item) => item.path));
    setMenu(null);
    event.dataTransfer.effectAllowed = "move";
    event.dataTransfer.setData("application/x-panasms-files", "move");
  }
  function overFolder(event: DragEvent, destination: string) {
    if (!event.dataTransfer.types.includes("application/x-panasms-files"))
      return;
    event.preventDefault();
    event.stopPropagation();
    const allowed = canMoveTo(destination);
    event.dataTransfer.dropEffect = allowed ? "move" : "none";
    setDropTarget(allowed ? destination : "");
  }
  async function dropMove(event: DragEvent, destination: string) {
    if (!event.dataTransfer.types.includes("application/x-panasms-files"))
      return;
    event.preventDefault();
    event.stopPropagation();
    setDropTarget("");
    if (!canMoveTo(destination)) return;
    const items = [...dragged.current];
    dragged.current = [];
    moveLock.current = true;
    setError("");
    let completed = 0;
    try {
      for (const item of items) {
        setMoveStatus(
          tr("moving_7af17ce3", {
            v0: completed + 1,
            v1: items.length,
            v2: item.name,
          }),
        );
        const params = {
          target: item.path,
          destination: destination.replace(/\/$/, "") + "/" + item.name,
        };
        const plan = await managed<{
          fingerprint: string;
          confirmation: string;
        }>("plan", {
          action: "file.move",
          params,
        });
        const job = await managed<{
          id: string;
        }>("run", {
          id: newID(),
          action: "file.move",
          params,
          fingerprint: plan.fingerprint,
          confirmation: plan.confirmation,
        });
        await waitForJob(async () => {
          const jobs = await managed<Job[]>("jobs");
          q.setQueryData(["jobs"], jobs);
          return jobs.find((j) => j.id === job.id);
        });
        completed++;
      }
    } catch (e) {
      setError(
        tr("moved_of_cf4c7ae0", {
          v0: completed,
          v1: items.length,
          v2: items[completed]?.name ?? "",
          v3: (e as Error).message,
        }),
      );
    } finally {
      moveLock.current = false;
      setMoveStatus("");
      setSelection([]);
      void q.invalidateQueries({ queryKey: ["files"] });
      void q.invalidateQueries({ queryKey: ["jobs"] });
    }
  }
  const folderDrop = (destination: string) => ({
    onDragOver: (event: DragEvent) => overFolder(event, destination),
    onDragLeave: () => setDropTarget(""),
    onDrop: (event: DragEvent) => void dropMove(event, destination),
  });
  const action = (name: string, label: string, icon: string) => (
    <OperationButton
      key={name + single?.path}
      icon={icon}
      label={label}
      actions={[name]}
      disabled={!single || single.link}
      initial={{ target: single?.path, destination: single?.path }}
      context={
        single
          ? [{ key: "target", label: tr("item_1f85d203"), value: single.path }]
          : []
      }
      autoReview
    />
  );
  const itemActions = (
    <>
      <Button
        title={tr("open_1259571a")}
        aria-label={tr("open_1259571a")}
        disabled={!single || single.link}
        onClick={() => single && open(single)}
      >
        <Icon path={mdiEyeOutline} />
      </Button>
      {single && !single.directory && !single.link ? (
        <a
          className="button"
          title={tr("download_fe8f79f2")}
          aria-label={tr("download_fe8f79f2")}
          href={
            "/api/v1/files/content?target=" + encodeURIComponent(single.path)
          }
        >
          <Icon path={mdiDownload} />
        </a>
      ) : (
        <Button
          title={tr("download_file_b421556b")}
          aria-label={tr("download_file_b421556b")}
          disabled
        >
          <Icon path={mdiDownload} />
        </Button>
      )}
      {!inTrash &&
        action("file.rename", tr("rename_715e8f0c"), mdiPencilOutline)}
      {action("file.copy", tr("copy_to_40d0eeb3"), mdiContentCopy)}
      {!inTrash && action("file.move", tr("move_to_44eb7965"), mdiContentCut)}
      {inTrash
        ? action("file.restore", tr("restore_to_f1fd2c89"), mdiRestore)
        : action(
            "file.trash",
            tr("move_to_trash_f8b39dea"),
            mdiTrashCanOutline,
          )}
      {inTrash &&
        action(
          "file.delete",
          tr("delete_permanently_34fd08c7"),
          mdiDeleteOutline,
        )}
    </>
  );
  const crumbPaths = root
    ? [
        root,
        ...path
          .slice(root.length)
          .split("/")
          .filter(Boolean)
          .map(
            (_, i, parts) =>
              root.replace(/\/$/, "") + "/" + parts.slice(0, i + 1).join("/"),
          ),
      ]
    : [];
  return (
    <WaitingSurface busy={progress !== null || !!moveStatus} message={progress !== null ? `${uploadLabel} · ${progress}%` : moveStatus}>
      {access.dialog}
      <div className="page-heading">
        <div>
          <h1>{tr("files_cdea63f8")}</h1>
          <p className="muted">{tr("folders_and_mounted_storage_bc031f69")}</p>
        </div>
      </div>
      <div
        className="file-manager"
        onKeyDown={(e) => {
          if (e.key === "Escape") {
            setMenu(null);
            setEditing(false);
            setSelection([]);
          }
          if ((e.ctrlKey || e.metaKey) && e.key === "l") {
            e.preventDefault();
            setAddress(path);
            setEditing(true);
          }
        }}
      >
        <aside
          className="file-sidebar"
          aria-label={tr("places_and_devices_2859156d")}
        >
          <h3>{tr("places_a6615904")}</h3>
          <button
            className={!path ? "active" : ""}
            onClick={() => navigate("")}
          >
            <Icon path={mdiFolderOutline} />
            <span>{tr("all_places_d0a7b29c")}</span>
          </button>
          <ul className="file-tree">
            {places
              .filter((p) => p.kind === "home")
              .map((p) => (
                <FolderTree
                  key={p.path}
                  folder={p.path}
                  label={p.name}
                  icon={mdiHomeOutline}
                  current={path}
                  hidden={hidden}
                  navigate={navigate}
                  dropTarget={dropTarget}
                  folderDrop={folderDrop}
                />
              ))}
          </ul>
          <button
            className={inTrash ? "active" : ""}
            onClick={() => navigate("trash:")}
          >
            <Icon path={mdiTrashCanOutline} />
            <span>{tr("trash_d9e801f1")}</span>
          </button>
          <h3>{tr("devices_7ab03d60")}</h3>
          <ul className="file-tree">
            {removable.map((device) => (
              <DeviceTree
                key={device.path}
                device={device}
                devices={devices}
                current={path}
                hidden={hidden}
                navigate={navigate}
                dropTarget={dropTarget}
                folderDrop={folderDrop}
                prepare={prepareVolume}
              />
            ))}
            {places
              .filter((p) => p.kind === "device")
              .map((p) => (
                <FolderTree
                  key={p.path}
                  folder={p.path}
                  label={p.name}
                  icon={p.network ? mdiFolderNetworkOutline : mdiHarddisk}
                  current={path}
                  hidden={hidden}
                  navigate={navigate}
                  dropTarget={dropTarget}
                  folderDrop={folderDrop}
                />
              ))}
          </ul>
          {!removable.length && !places.some((p) => p.kind === "device") && (
            <p className="small muted">
              {tr("no_accessible_mounted_volumes_6bc3ed78")}
            </p>
          )}
        </aside>
        <div className="file-main">
          <div className="file-navigation">
            <Button
              title={tr("back_f6dab074")}
              aria-label={tr("back_f6dab074")}
              onClick={() => browserNavigate(-1)}
            >
              <Icon path={mdiArrowLeft} />
            </Button>
            <Button
              title={tr("forward_5729ed0a")}
              aria-label={tr("forward_5729ed0a")}
              onClick={() => browserNavigate(1)}
            >
              <Icon path={mdiArrowRight} />
            </Button>
            <Button
              title={tr("up_one_level_25ee7583")}
              aria-label={tr("up_one_level_25ee7583")}
              disabled={!root || path === root}
              onClick={() => {
                const parent = path.slice(0, path.lastIndexOf("/")) || "/";
                navigate(
                  trashRoot &&
                    (parent === trashRoot ||
                      /^\d{19,}-[a-z0-9_]{8}$/.test(
                        parent.slice(trashRoot.length + 1),
                      ))
                    ? "trash:"
                    : parent,
                );
              }}
            >
              <Icon path={mdiArrowUp} />
            </Button>
            <Button
              title={tr("refresh_c2f668e5")}
              aria-label={tr("refresh_c2f668e5")}
              onClick={() => {
                void data.refetch();
                void locations.refetch();
              }}
            >
              <Icon path={mdiRefresh} />
            </Button>
            {editing ? (
              <form
                className="file-address"
                onSubmit={(e) => {
                  e.preventDefault();
                  navigate(address.trim().replace(/\/+$/, "") || "/");
                }}
              >
                <input
                  ref={addressInput}
                  aria-label={tr("folder_path_b03fafe2")}
                  value={address}
                  onChange={(e) => setAddress(e.target.value)}
                />
              </form>
            ) : (
              <nav
                className="file-breadcrumbs"
                aria-label={tr("current_folder_de813f16")}
              >
                {!path ? (
                  <span>{tr("all_places_d0a7b29c")}</span>
                ) : path === "trash:" ? (
                  <span>{tr("trash_d9e801f1")}</span>
                ) : deviceView ? (
                  <span>
                    {selectedDevice?.model?.trim() ||
                      selectedDevice?.name ||
                      tr("device_disconnected_03b2f26c")}
                  </span>
                ) : (
                  crumbPaths.map((p, i) => (
                    <span key={p}>
                      {i > 0 && <Icon path={mdiChevronRight} />}
                      <button
                        {...folderDrop(p)}
                        className={dropTarget === p ? "file-move-target" : ""}
                        onClick={() => navigate(p)}
                        title={p}
                      >
                        {i === 0
                          ? allPlaces.find((v) => v.path === p)?.name || p
                          : p.split("/").at(-1)}
                      </button>
                    </span>
                  ))
                )}
              </nav>
            )}
            <Button
              title={tr("enter_path_ctrl_l_0fbbc6a5")}
              aria-label={tr("enter_path_b6a0717e")}
              onClick={() => {
                setAddress(path);
                setEditing((v) => !v);
              }}
            >
              <Icon path={mdiPencilOutline} />
            </Button>
          </div>
          <div
            className="file-toolbar"
            aria-label={tr("file_actions_90dbb13a")}
          >
            {!!path && !deviceView && <><OperationButton
              icon={mdiFolderPlusOutline}
              label={tr("create_folder_944b559c")}
              actions={["file.mkdir"]}
              initial={{ parent: path }}
              disabled={deviceView || inTrash || !path || !!data.error}
            />
            <Button
              title={tr("upload_file_6212feb0")}
              aria-label={tr("upload_file_6212feb0")}
              disabled={!canUpload}
              onClick={() => uploadInput.current?.click()}
            >
              <Icon path={mdiUpload} />
            </Button>
            <input
              ref={uploadInput}
              type="file"
              multiple
              hidden
              onChange={(e) => {
                if (e.target.files) void upload(Array.from(e.target.files));
                e.target.value = "";
              }}
            />
            {itemActions}
            <span className="file-toolbar-spacer" />
            <FolderPermissions
              key={path}
              path={path}
              disabled={deviceView || inTrash || !path}
            />
            {session.data?.role === "admin" && <Button title={tr('share_folder')} aria-label={tr('share_folder')} disabled={inTrash || !path} onClick={() => browserNavigate('/sharing?folder=' + encodeURIComponent(path))}><Icon path={mdiShareVariantOutline} /></Button>}
            </>}

            <Button
              title={
                hidden
                  ? tr("hide_hidden_files_1019f837")
                  : tr("show_hidden_files_d8786d06")
              }
              aria-label={tr("hidden_files_f6384287")}
              aria-pressed={hidden}
              onClick={() => setHidden(!hidden)}
            >
              <Icon path={mdiEyeOffOutline} />
            </Button>
            <Button
              title={tr("list_a21729ad")}
              aria-label={tr("list_a21729ad")}
              aria-pressed={view === "list"}
              onClick={() => setView("list")}
            >
              <Icon path={mdiFormatListBulleted} />
            </Button>
            <Button
              title={tr("icons_4f97c256")}
              aria-label={tr("icons_4f97c256")}
              aria-pressed={view === "grid"}
              onClick={() => setView("grid")}
            >
              <Icon path={mdiViewGridOutline} />
            </Button>
            <select
              aria-label={tr("sort_ed030118")}
              title={tr("sort_ed030118")}
              value={sort}
              onChange={(e) => setSort(e.target.value)}
            >
              <option value="name">{tr("by_name_48b527cb")}</option>
              <option value="size">{tr("by_size_2b036b5a")}</option>
              <option value="modified">{tr("by_date_9fb930e4")}</option>
            </select>
          </div>
          {moveStatus && <Notice>{moveStatus}</Notice>}
          {progress !== null && (
            <Notice>
              <span className="file-upload-label">
                {tr("upload_03a9c8fb") + " "}
                {uploadLabel}
              </span>
              <progress
                className="file-upload-progress"
                value={progress}
                max={100}
              />
              {progress}%
            </Notice>
          )}
          {error && <Notice error>{error}</Notice>}
          {data.error && <Notice error>{data.error.message}</Notice>}
          {locations.error && !data.data && (
            <Notice error>{locations.error.message}</Notice>
          )}
          <div
            className={`file-content ${dragging ? "file-drop-active" : ""}`}
            onContextMenu={(event) => {
              event.preventDefault();
              setSelection([]);
              setMenu({
                kind: "folder",
                x: Math.max(
                  8,
                  Math.min(event.clientX, window.innerWidth - 260),
                ),
                y: Math.max(
                  8,
                  Math.min(event.clientY, window.innerHeight - 390),
                ),
              });
            }}
            onDragEnter={(e) => {
              if (e.dataTransfer.types.includes("Files")) {
                e.preventDefault();
                dragDepth.current++;
                setDragging(true);
              }
            }}
            onDragOver={(e) => {
              if (e.dataTransfer.types.includes("Files")) {
                e.preventDefault();
                e.dataTransfer.dropEffect = canUpload ? "copy" : "none";
              }
            }}
            onDragLeave={(e) => {
              if (e.dataTransfer.types.includes("Files")) {
                dragDepth.current = Math.max(0, dragDepth.current - 1);
                if (!dragDepth.current) setDragging(false);
              }
            }}
            onDrop={dropFiles}
            onClick={(e) => {
              if (e.target === e.currentTarget) setSelection([]);
            }}
          >
            {dragging && (
              <div className="file-drop-overlay">
                <Icon path={mdiUpload} />
                <strong>
                  {canUpload
                    ? tr("drop_files_to_upload_993a0092")
                    : uploading.current
                      ? tr("wait_for_the_upload_to_finish_239ff75c")
                      : tr("open_a_folder_first_4327ac8a")}
                </strong>
                {canUpload && <span>{path}</span>}
              </div>
            )}
            {!path ? (
              <div className="file-places">
                {removable.map((d) => (
                  <button key={d.path} onClick={() => enterDevice(d)}>
                    <Icon
                      path={
                        d.media?.kind === "sd" ? mdiMicroSd : mdiUsbFlashDrive
                      }
                    />
                    <strong>
                      {d.model?.trim() || d.media?.name || d.name}
                    </strong>
                    <small>{bytes(d.size)}</small>
                  </button>
                ))}
                {places.map((p) => (
                  <button key={p.path} onClick={() => navigate(p.path)}>
                    <Icon
                      path={
                        p.kind === "home"
                          ? mdiHomeOutline
                          : p.kind === "trash"
                            ? mdiTrashCanOutline
                            : p.network
                              ? mdiFolderNetworkOutline
                              : mdiHarddisk
                      }
                    />
                    <strong>{p.name}</strong>
                    <small>
                      {p.kind === "trash"
                        ? tr("deleted_files_33fa378a")
                        : p.path}
                    </small>
                  </button>
                ))}
              </div>
            ) : deviceView ? (
              <div className="file-places">
                {selectedDevice ? (
                  deviceVolumes(selectedDevice, devices).map((v) => (
                    <button key={v.path} onClick={() => void enterVolume(v)}>
                      <Icon path={mdiHarddisk} />
                      <strong>
                        {v.name} · {v.fstype}
                      </strong>
                      <small>{bytes(v.size)}</small>
                    </button>
                  ))
                ) : (
                  <p>{tr("device_disconnected_03b2f26c")}</p>
                )}
                {selectedDevice &&
                  !deviceVolumes(selectedDevice, devices).length && (
                    <p>{tr("no_accessible_file_systems_df581231")}</p>
                  )}
              </div>
            ) : (
              <>
                <div
                  className={view === "grid" ? "file-grid" : "file-list"}
                  role="listbox"
                  aria-label={tr("folder_contents_4c7c9220")}
                  aria-multiselectable="true"
                >
                  {view === "list" && (
                    <div className="file-list-header" aria-hidden="true">
                      <span>{tr("name_3de49828")}</span>
                      <span>{tr("size_98713e88")}</span>
                      <span>{tr("modified_440e2b5d")}</span>
                    </div>
                  )}
                  {entries.map((e, index) => (
                    <div
                      key={e.path}
                      role="option"
                      aria-selected={selection.includes(e.path)}
                      tabIndex={focusedPath === e.path || (!entries.some(v => v.path === focusedPath) && index === 0) ? 0 : -1}
                      onFocus={() => setFocusedPath(e.path)}
                      className={`file-entry ${selection.includes(e.path) ? "selected" : ""} ${dropTarget === e.path ? "file-move-target" : ""}`}
                      draggable={
                        !inTrash &&
                        !e.link &&
                        !moveLock.current &&
                        !uploading.current
                      }
                      onDragStart={(event) => startMove(event, e)}
                      onDragEnd={() => {
                        dragged.current = [];
                        setDropTarget("");
                      }}
                      {...(e.directory && !e.link ? folderDrop(e.path) : {})}
                      title={
                        e.link
                          ? tr(
                              "is_a_symbolic_link_navigation_is_unavailable_8e910d29",
                              { v0: e.name },
                            )
                          : e.name
                      }
                      onClick={(event) => choose(e, event)}
                      onDoubleClick={() => open(e)}
                      onKeyDown={(event) => {
                        const nodes = Array.from(event.currentTarget.parentElement!.querySelectorAll<HTMLElement>('[role="option"]'));
                        let nextIndex = -1;
                        if (["ArrowDown", "ArrowRight"].includes(event.key)) nextIndex = Math.min(entries.length - 1, index + 1);
                        if (["ArrowUp", "ArrowLeft"].includes(event.key)) nextIndex = Math.max(0, index - 1);
                        if (event.key === "Home") nextIndex = 0;
                        if (event.key === "End") nextIndex = entries.length - 1;
                        if (event.key.length === 1 && event.key !== " " && !event.ctrlKey && !event.metaKey && !event.altKey) {
                          const now = Date.now();
                          typeahead.current = { text: (now - typeahead.current.time < 700 ? typeahead.current.text : "") + event.key.toLocaleLowerCase(), time: now };
                          nextIndex = entries.findIndex(v => v.name.toLocaleLowerCase().startsWith(typeahead.current.text));
                        }
                        if (nextIndex >= 0) {
                          event.preventDefault(); nodes[nextIndex]?.focus();
                          if (event.shiftKey) {
                            const from = entries.findIndex(v => v.path === anchor.current);
                            const start = from < 0 ? index : from;
                            if (from < 0) anchor.current = e.path;
                            setSelection(entries.slice(Math.min(start, nextIndex), Math.max(start, nextIndex) + 1).map(v => v.path));
                          }
                        }
                        if (event.key === "ContextMenu" || (event.shiftKey && event.key === "F10")) {
                          event.preventDefault();
                          if (!selection.includes(e.path)) setSelection([e.path]);
                          const box = event.currentTarget.getBoundingClientRect();
                          setMenu({ kind: "item", x: Math.max(8, Math.min(box.left, innerWidth - 260)), y: Math.max(8, Math.min(box.bottom, innerHeight - 390)) });
                        }
                        if (event.key === "Enter") {
                          event.preventDefault();
                          open(e);
                        }
                        if (event.key === " ") {
                          event.preventDefault();
                          choose(e, event);
                        }
                        if (
                          (event.ctrlKey || event.metaKey) &&
                          event.key === "a"
                        ) {
                          event.preventDefault();
                          setSelection(entries.map((v) => v.path));
                        }
                      }}
                      onContextMenu={(event) => {
                        event.preventDefault();
                        event.stopPropagation();
                        if (!selection.includes(e.path)) setSelection([e.path]);
                        setMenu({
                          kind: "item",
                          x: Math.max(
                            8,
                            Math.min(event.clientX, window.innerWidth - 260),
                          ),
                          y: Math.max(
                            8,
                            Math.min(event.clientY, window.innerHeight - 390),
                          ),
                        });
                      }}
                    >
                      <span className="file-entry-name">
                        <FileVisual entry={e} tiles={view === "grid"} />
                        <span>
                          {e.name}
                          {e.link ? " ↗" : ""}
                        </span>
                      </span>
                      <span className="file-entry-size">
                        {e.directory ? "—" : bytes(e.size)}
                      </span>
                      <span className="file-entry-date">
                        {new Date(e.modified * 1000).toLocaleString(locale(), {
                          dateStyle: "short",
                          timeStyle: "short",
                        })}
                      </span>
                    </div>
                  ))}
                </div>
                {data.isPending && (
                  <p className="file-empty muted">{tr("loading_b6819e91")}</p>
                )}
                {!data.isPending && !data.error && !entries.length && (
                  <p className="file-empty muted">
                    {path === "trash:"
                      ? tr("trash_is_empty_5deff986")
                      : tr("folder_is_empty_428efbdb", {
                          v0: hidden
                            ? ""
                            : " " +
                              tr("or_contains_only_hidden_files_2ae466f1"),
                        })}
                  </p>
                )}
              </>
            )}
          </div>
          <footer className="file-status">
            <span>
              {selected.length
                ? tr("selected_ccd1c631", {
                    v0: selected.length,
                    v1: selected.every((e) => !e.directory)
                      ? " · " + bytes(selected.reduce((n, e) => n + e.size, 0))
                      : "",
                  })
                : path
                  ? data.error ? "—" : data.isPending ? tr("loading_b6819e91", { defaultValue: "…" }) : tr("items_cb84f921", { v0: entries.length })
                  : tr("places_ea24302a", {
                      v0: places.length + removable.length,
                    })}
              {selected.length > 1
                ? " " + tr("drag_the_selection_onto_a_folder_4a092343")
                : ""}
            </span>
            {data.data?.freeBytes != null && (
              <span>
                {tr("free_a2f50530") + " "}
                {bytes(data.data.freeBytes)}
              </span>
            )}
          </footer>
        </div>
      </div>
      {permissionTargets && (
        <FolderPermissions
          path={path}
          targets={permissionTargets}
          onClose={() => setPermissionTargets(null)}
        />
      )}
      {menu && (
        <div
          ref={menuRef}
          tabIndex={-1}
          className="file-context-menu"
          onClick={(e) => e.stopPropagation()}
          onContextMenu={(e) => e.preventDefault()}
          onKeyDown={(e) => {
            if (!e.currentTarget.contains(e.target as Node)) return;
            if (e.key === "Escape") {
              e.stopPropagation();
              setMenu(null);
            }
            if (e.key === "ArrowDown" || e.key === "ArrowUp") {
              e.preventDefault();
              const buttons = Array.from(
                e.currentTarget.querySelectorAll<HTMLElement>(
                  "button:not(:disabled),a[href]",
                ),
              );
              const index = buttons.indexOf(
                document.activeElement as HTMLElement,
              );
              buttons[
                (index +
                  (e.key === "ArrowDown" ? 1 : buttons.length - 1) +
                  buttons.length) %
                  buttons.length
              ]?.focus();
            }
          }}
          aria-label={
            menu.kind === "folder"
              ? tr("folder_actions_71835020")
              : tr("selected_file_actions_1562ad83")
          }
          style={{ left: menu.x, top: menu.y }}
        >
          {menu.kind === "item" ? (
            <>
              {itemActions}
              <PermissionsMenuAction
                disabled={
                  inTrash || !selected.length || selected.some((e) => e.link)
                }
                onClick={() => {
                  setPermissionTargets(selected.map((e) => e.path));
                  setMenu(null);
                }}
              />
            </>
          ) : (
            <>
              <OperationButton
                icon={mdiFolderPlusOutline}
                label={tr("create_folder_944b559c")}
                actions={["file.mkdir"]}
                initial={{ parent: path }}
                disabled={deviceView || inTrash || !path || !!data.error}
              />
              <Button
                aria-label={tr("upload_files_9906a6ad")}
                disabled={!canUpload}
                onClick={() => {
                  uploadInput.current?.click();
                  setMenu(null);
                }}
              >
                <Icon path={mdiUpload} />
              </Button>
              <Button
                aria-label={tr("refresh_c2f668e5")}
                onClick={() => {
                  void data.refetch();
                  void locations.refetch();
                  setMenu(null);
                }}
              >
                <Icon path={mdiRefresh} />
              </Button>
              <hr />
              <Button
                aria-label={tr("select_all_978abfe2")}
                disabled={!entries.length || !path}
                onClick={() => {
                  setSelection(entries.map((e) => e.path));
                  setMenu(null);
                }}
              >
                <Icon path={mdiCheckboxMultipleMarkedOutline} />
              </Button>
              <Button
                aria-label={
                  hidden
                    ? tr("hide_hidden_files_1019f837")
                    : tr("show_hidden_files_d8786d06")
                }
                onClick={() => {
                  setHidden(!hidden);
                  setMenu(null);
                }}
              >
                <Icon path={mdiEyeOffOutline} />
              </Button>
              <hr />
              <Button
                aria-label={tr("list_a21729ad")}
                aria-pressed={view === "list"}
                onClick={() => {
                  setView("list");
                  setMenu(null);
                }}
              >
                <Icon path={mdiFormatListBulleted} />
              </Button>
              <Button
                aria-label={tr("icons_4f97c256")}
                aria-pressed={view === "grid"}
                onClick={() => {
                  setView("grid");
                  setMenu(null);
                }}
              >
                <Icon path={mdiViewGridOutline} />
              </Button>
            </>
          )}
        </div>
      )}
      <Dialog.Root
        open={!!preview}
        onOpenChange={(open) => {
          if (!open) setPreview(null);
        }}
      >
        {preview && <Preview entry={preview} />}
      </Dialog.Root>
    </WaitingSurface>
  );
}
type FolderTreeProps = {
  dropAllowed?: boolean;
  prepare?: () => Promise<string>;
  folder: string;
  label: string;
  icon?: string;
  current: string;
  hidden: boolean;
  navigate: (path: string) => void;
  dropTarget: string;
  folderDrop: (path: string) => {
    onDragOver: (event: DragEvent) => void;
    onDragLeave: () => void;
    onDrop: (event: DragEvent) => void;
  };
};
function FolderTree({
  folder: initialFolder,
  dropAllowed = true,
  prepare,
  label,
  icon = mdiFolderOutline,
  current,
  hidden,
  navigate,
  dropTarget,
  folderDrop,
}: FolderTreeProps) {
  const [expanded, setExpanded] = useState(false);
  const [resolved, setResolved] = useState("");
  const [busy, setBusy] = useState(false);
  const [failure, setFailure] = useState("");
  const lock = useRef(false);
  const folder = resolved || initialFolder;
  useEffect(() => {
    if (!dropAllowed) setExpanded(false);
  }, [dropAllowed]);
  async function activate(expand: boolean) {
    if (expand && expanded) {
      setExpanded(false);
      return;
    }
    if (lock.current) return;
    lock.current = true;
    setBusy(true);
    setFailure("");
    try {
      const destination = prepare ? await prepare() : folder;
      setResolved(destination);
      if (expand) setExpanded(true);
      else navigate(destination);
    } catch (e) {
      setFailure((e as Error).message);
    } finally {
      lock.current = false;
      setBusy(false);
    }
  }
  const listing = useQuery({
    queryKey: ["files", folder],
    queryFn: () => managed<Listing>("files", undefined, folder),
    enabled: expanded,
    refetchInterval: expanded ? 10000 : false,
  });
  const children = (listing.data?.entries ?? [])
    .filter(
      (e) => e.directory && !e.link && (hidden || !e.name.startsWith(".")),
    )
    .sort((a, b) => a.name.localeCompare(b.name, locale(), { numeric: true }));
  return (
    <li>
      <div
        className={`file-tree-row ${current === folder ? "active" : ""} ${dropTarget === folder ? "file-move-target" : ""}`}
        {...(dropAllowed ? folderDrop(folder) : {})}
      >
        <button
          className="file-tree-toggle"
          aria-label={`${expanded ? tr("collapse_4faa82f5") : tr("expand_fd3ae0ea")} ${label}`}
          aria-expanded={expanded}
          title={expanded ? tr("collapse_4faa82f5") : tr("expand_fd3ae0ea")}
          disabled={busy}
          onClick={() => void activate(true)}
        >
          <Icon path={mdiChevronRight} />
        </button>
        <button
          className="file-tree-link"
          title={folder}
          aria-current={current === folder ? "page" : undefined}
          disabled={busy}
          onClick={() => void activate(false)}
        >
          <Icon path={icon} />
          <span>{label}</span>
        </button>
      </div>
      {busy && <p className="file-tree-message">{tr("connecting_40b27edf")}</p>}
      {failure && <p className="file-tree-message error-text">{failure}</p>}
      {expanded && (
        <ul className="file-tree-children">
          {listing.isPending ? (
            <li className="file-tree-message">{tr("loading_b6819e91")}</li>
          ) : listing.error ? (
            <li className="file-tree-message error-text">
              {listing.error.message}
              <button
                title={tr("retry_9e506acb")}
                aria-label={tr("retry_loading_folders_0203864a")}
                onClick={() => void listing.refetch()}
              >
                <Icon path={mdiRefresh} />
              </button>
            </li>
          ) : children.length ? (
            children.map((child) => (
              <FolderTree
                key={child.path}
                folder={child.path}
                label={child.name}
                current={current}
                hidden={hidden}
                navigate={navigate}
                dropTarget={dropTarget}
                folderDrop={folderDrop}
              />
            ))
          ) : (
            <li className="file-tree-message">
              {tr("no_subfolders_2d70ca1b")}
            </li>
          )}
        </ul>
      )}
    </li>
  );
}
function DeviceTree({
  device,
  devices,
  prepare,
  ...props
}: Omit<FolderTreeProps, "folder" | "label" | "icon" | "prepare"> & {
  device: Removable;
  devices: Removable[];
  prepare: (volume: Removable) => Promise<string>;
}) {
  const [expanded, setExpanded] = useState(false);
  const label = device.model?.trim() || device.media?.name || device.name;
  const single = singleVolume(device, devices);
  const icon = device.media?.kind === "sd" ? mdiMicroSd : mdiUsbFlashDrive;
  const volumeTree = (v: Removable, name: string, volumeIcon: string) => (
    <FolderTree
      key={v.path + v.uuid}
      {...props}
      folder={v.mountpoints?.find(Boolean) || volumeMountParams(v).point}
      label={name}
      icon={volumeIcon}
      dropAllowed={!!v.mountpoints?.some(Boolean)}
      prepare={() => prepare(v)}
    />
  );
  if (single) return volumeTree(single, label, icon);
  const volumes = deviceVolumes(device, devices);
  return (
    <li>
      <div className="file-tree-row">
        <button
          className="file-tree-toggle"
          title={expanded ? tr("collapse_4faa82f5") : tr("expand_fd3ae0ea")}
          aria-label={`${expanded ? tr("collapse_4faa82f5") : tr("expand_fd3ae0ea")} ${label}`}
          aria-expanded={expanded}
          onClick={() => setExpanded(!expanded)}
        >
          <Icon path={mdiChevronRight} />
        </button>
        <button
          className="file-tree-link"
          title={device.path}
          onClick={() => {
            setExpanded(true);
            props.navigate("device:" + device.path);
          }}
        >
          <Icon path={icon} />
          <span>{label}</span>
        </button>
      </div>
      {expanded && (
        <ul className="file-tree-children">
          {volumes.map((v) =>
            volumeTree(v, `${v.name} · ${v.fstype}`, mdiHarddisk),
          )}
          {!volumes.length && (
            <li className="file-tree-message">
              {tr("no_accessible_file_systems_df581231")}
            </li>
          )}
        </ul>
      )}
    </li>
  );
}
registerModule({
  id: "files",
  title: tr("files_cdea63f8"),
  path: "/files",
  icon: mdiFolderOutline,
  component: FilesPage,
  widgets: {
    files: {
      title: tr("files_cdea63f8"),
      width: 1,
      height: 1,
      module: tr("files_cdea63f8"),
      href: "/files",
    },
  },
});
function Preview({ entry }: { entry: Entry }) {
  const [loading, setLoading] = useState(true);
  const [text, setText] = useState("");
  const [image, setImage] = useState("");
  const [error, setError] = useState("");
  useEffect(() => {
    const controller = new AbortController();
    setLoading(true);
    void (async () => {
      try {
        const ext = entry.name.split(".").at(-1)?.toLowerCase() ?? "";
        const images: Record<string, string> = {
          png: "image/png",
          jpg: "image/jpeg",
          jpeg: "image/jpeg",
          webp: "image/webp",
          gif: "image/gif",
        };
        const mime = images[ext];
        if (
          !mime &&
          ![
            "txt",
            "md",
            "json",
            "log",
            "csv",
            "yaml",
            "yml",
            "ini",
            "conf",
          ].includes(ext)
        )
          throw Error(tr("this_file_type_can_be_downloaded_40d0d6f7"));
        const limit = mime ? 8 * 1024 * 1024 : 1024 * 1024;
        if (entry.size > limit)
          throw Error(tr("the_file_is_too_large_to_preview_d4e2dd3d"));
        const r = await fetch(
          "/api/v1/files/content?target=" + encodeURIComponent(entry.path),
          {
            signal: controller.signal,
          },
        );
        if (!r.ok || !r.body) throw Error(tr("could_not_read_file_6d02ec2f"));
        const reader = r.body.getReader();
        const chunks: Uint8Array<ArrayBuffer>[] = [];
        let size = 0;
        while (true) {
          const { done, value } = await reader.read();
          if (done) break;
          size += value.length;
          if (size > limit) {
            await reader.cancel();
            throw Error(tr("preview_size_limit_exceeded_e60974d1"));
          }
          chunks.push(value);
        }
        const blob = new Blob(chunks, { type: mime ?? "text/plain" });
        if (mime) {
          const f = new FileReader();
          f.onload = () => {
            if (!controller.signal.aborted) { setImage(String(f.result)); setLoading(false); }
          };
          f.readAsDataURL(blob);
        } else { setText(await blob.text()); setLoading(false); }
      } catch (e) {
        if (!controller.signal.aborted) { setError((e as Error).message); setLoading(false); }
      }
    })();
    return () => controller.abort();
  }, [entry]);
  return (
    <Dialog.Portal>
      <Dialog.Overlay className="dialog-overlay" />
      <DialogContent busy={loading} className="settings-dialog">
        <div className="dialog-heading">
          <Dialog.Title>{entry.name}</Dialog.Title>
          <Dialog.Close asChild>
            <Button
              title={tr("close_4ae50d30")}
              aria-label={tr("close_4ae50d30")}
            >
              <Icon path={mdiClose} />
            </Button>
          </Dialog.Close>
        </div>
        <Dialog.Description className="muted small">
          {entry.path}
        </Dialog.Description>
        {error ? (
          <Notice error>{error}</Notice>
        ) : image ? (
          <img src={image} alt={entry.name} style={{ maxWidth: "100%" }} />
        ) : (
          <pre style={{ whiteSpace: "pre-wrap", overflowWrap: "anywhere" }}>
            {text}
          </pre>
        )}
      </DialogContent>
    </Dialog.Portal>
  );
}
