import { DialogContent, WaitingSurface } from "@panasms/ui";
import { useEffect, useRef, useState } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import * as Dialog from "@radix-ui/react-dialog";
import { mdiFolderKeyOutline, mdiClose } from "@mdi/js";
import { request, type Identity } from "@panasms/client";
import { managed, type Job } from "@panasms/operations";
import { newID } from "@panasms/layout";
import { waitForJob } from "@panasms/completion";
import { Button, Icon, Notice } from "@panasms/ui";
import { tr } from "./i18n";
import "./folder-permissions.css";

type Permissions = {
  target: string;
  directory: boolean;
  owner: string;
  group: string;
  mode: string;
  revision: string;
  acl: boolean;
  defaultAcl: boolean;
};
export function PermissionsMenuAction({
  onClick,
  disabled,
}: {
  onClick: () => void;
  disabled: boolean;
}) {
  const session = useQuery({
    queryKey: ["session"],
    queryFn: () => request<Identity>("session"),
  });
  if (session.data?.role !== "admin") return null;
  return (
    <Button
      disabled={disabled}
      title={tr("permissions.title")}
      aria-label={tr("permissions.title")}
      onClick={onClick}
    >
      <Icon path={mdiFolderKeyOutline} />
    </Button>
  );
}
export function FolderPermissions({
  path,
  disabled = false,
  targets,
  onClose,
}: {
  path: string;
  disabled?: boolean;
  targets?: string[];
  onClose?: () => void;
}) {
  const session = useQuery({
    queryKey: ["session"],
    queryFn: () => request<Identity>("session"),
  });
  const admin = session.data?.role === "admin";
  const q = useQueryClient();
  const [localOpen, setLocalOpen] = useState(false);
  const open = targets !== undefined || localOpen;
  const setOpen = (next: boolean) => {
    setLocalOpen(next);
    if (!next) onClose?.();
  };
  const paths = targets ?? [path];
  const [mask, setMask] = useState(0);
  const [owner, setOwner] = useState("");
  const [group, setGroup] = useState("");
  const [mode, setMode] = useState(0);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [discard, setDiscard] = useState(false);
  const data = useQuery({
    queryKey: ["folder-permissions", paths],
    queryFn: () =>
      managed<{ items: Permissions[]; users: string[]; groups: string[] }>(
        "files",
        undefined,
        "permissions-selection:" + JSON.stringify(paths),
      ),
    enabled: open && admin,
    retry: false,
    refetchOnWindowFocus: false,
  });
  const initialized = useRef("");
  useEffect(() => {
    if (!open) initialized.current = "";
    if (data.data && initialized.current !== JSON.stringify(paths)) {
      initialized.current = JSON.stringify(paths);
      setOwner("");
      setGroup("");
      setMask(0);
      setMode(parseInt(data.data.items[0].mode, 8));
    }
  }, [data.data, open, JSON.stringify(paths)]);
  const dirty = !!owner || !!group || !!data.data?.items.some(item => (parseInt(item.mode, 8) & mask) !== (mode & mask));
  async function save() {
    if (!data.data) return;
    setBusy(true);
    setError("");
    try {
      const action = "file.permissions";
      const params = {
        items: data.data.items.map(({ target, revision }) => ({
          target,
          revision,
        })),
        owner,
        group,
        mask,
        bits: mode & mask,
      };
      const plan = await managed<{ fingerprint: string; confirmation: string }>(
        "plan",
        { action, params },
      );
      const job = await managed<{ id: string }>("run", {
        id: newID(),
        action,
        params,
        fingerprint: plan.fingerprint,
        confirmation: plan.confirmation,
      });
      await waitForJob(async () =>
        (await managed<Job[]>("jobs")).find((item) => item.id === job.id),
      );
      await Promise.all([
        q.invalidateQueries({ queryKey: ["files"] }),
        q.invalidateQueries({ queryKey: ["file-places"] }),
        q.invalidateQueries({ queryKey: ["folder-permissions", paths] }),
      ]);
      setOpen(false);
    } catch (e) {
      setError((e as Error).message);
    } finally {
      setBusy(false);
    }
  }
  if (!admin) return null;
  return (
    <>
    <Dialog.Root
      open={open}
      onOpenChange={(next) => {
        if (!busy) {
          if (!next && dirty) { setDiscard(true); return; }
          setOpen(next);
          setError("");
        }
      }}
    >
      {!targets && (
        <Dialog.Trigger asChild>
          <Button
            disabled={disabled}
            title={tr("permissions.title")}
            aria-label={tr("permissions.title")}
          >
            <Icon path={mdiFolderKeyOutline} />
          </Button>
        </Dialog.Trigger>
      )}
      <Dialog.Portal>
        <Dialog.Overlay className="dialog-overlay" />
        <DialogContent busy={busy || data.isPending} className="settings-dialog folder-permissions-dialog">
          <div className="folder-permissions-heading">
            <Dialog.Title>{tr("permissions.title")}</Dialog.Title>
            <Dialog.Close asChild>
              <Button
                disabled={busy}
                title={tr("permissions.cancel")}
                aria-label={tr("permissions.cancel")}
              >
                <Icon path={mdiClose} />
              </Button>
            </Dialog.Close>
          </div>
          <Dialog.Description>
            {tr("permissions.description")}
          </Dialog.Description>
          <strong className="folder-permissions-path">
            {paths.length === 1
              ? paths[0]
              : tr("permissions.selected", { v0: paths.length })}
          </strong>
          {(error || data.error) && (
            <Notice error>{error || data.error?.message}</Notice>
          )}
          {data.isPending ? (
            <p>{tr("permissions.loading")}</p>
          ) : (
            data.data && (
              <form
                onSubmit={(e) => {
                  e.preventDefault();
                  void save();
                }}
              >
                <fieldset disabled={busy}>
                  <label>
                    {tr("permissions.owner")}
                    <select
                      value={owner}
                      onChange={(e) => setOwner(e.target.value)}
                    >
                      <option value="">
                        {tr("permissions.keep")} ·{" "}
                        {Array.from(
                          new Set(data.data.items.map((i) => i.owner)),
                        ).join(", ")}
                      </option>
                      {data.data.users.map((name) => (
                        <option key={name} value={name}>
                          {name}
                        </option>
                      ))}
                    </select>
                  </label>
                  <label>
                    {tr("permissions.group")}
                    <select
                      value={group}
                      onChange={(e) => setGroup(e.target.value)}
                    >
                      <option value="">
                        {tr("permissions.keep")} ·{" "}
                        {Array.from(
                          new Set(data.data.items.map((i) => i.group)),
                        ).join(", ")}
                      </option>
                      {data.data.groups.map((name) => (
                        <option key={name} value={name}>
                          {name}
                        </option>
                      ))}
                    </select>
                  </label>
                  <div className="table-wrap"><table>
                    <thead>
                      <tr>
                        <th></th>
                        {["read", "write", "enter"].map((name) => (
                          <th key={name}>{tr("permissions." + name)}</th>
                        ))}
                      </tr>
                    </thead>
                    <tbody>
                      {[
                        ["owner", 6],
                        ["group", 3],
                        ["others", 0],
                      ].map(([name, shift]) => (
                        <tr key={name}>
                          <th scope="row">{tr("permissions." + name)}</th>
                          {[4, 2, 1].map((bit, index) => {
                            const flag = bit << Number(shift);
                            const mixed =
                              !(mask & flag) &&
                              data.data!.items.some(
                                (i) =>
                                  !!(parseInt(i.mode, 8) & flag) !==
                                  !!(mode & flag),
                              );
                            return (
                              <td key={bit}>
                                <input
                                  type="checkbox"
                                  aria-label={
                                    tr("permissions." + name) +
                                    ": " +
                                    tr(
                                      "permissions." +
                                        ["read", "write", "enter"][index],
                                    )
                                  }
                                  ref={(el) => {
                                    if (el) el.indeterminate = mixed;
                                  }}
                                  checked={!!(mode & flag)}
                                  onChange={(e) => {
                                    setMask((value) => value | flag);
                                    setMode((value) =>
                                      e.target.checked
                                        ? value | flag
                                        : value & ~flag,
                                    );
                                  }}
                                />
                              </td>
                            );
                          })}
                        </tr>
                      ))}
                    </tbody>
                  </table></div>
                  {data.data.items.some((i) => i.directory) && (
                    <label className="folder-permissions-check">
                      <input
                        type="checkbox"
                        checked={!!(mode & 0o2000)}
                        ref={(el) => {
                          if (el)
                            el.indeterminate =
                              !(mask & 0o2000) &&
                              data
                                .data!.items.filter((i) => i.directory)
                                .some(
                                  (i) =>
                                    !!(parseInt(i.mode, 8) & 0o2000) !==
                                    !!(mode & 0o2000),
                                );
                        }}
                        onChange={(e) => {
                          setMask((value) => value | 0o2000);
                          setMode((value) =>
                            e.target.checked ? value | 0o2000 : value & ~0o2000,
                          );
                        }}
                      />
                      {tr("permissions.inheritGroup")}
                    </label>
                  )}
                  {data.data.items.some((i) => i.acl || i.defaultAcl) && (
                    <Notice>{tr("permissions.acl")}</Notice>
                  )}
                </fieldset>
                <div className="actions">
                  <Button disabled={busy || !dirty}>
                    {tr(busy ? "permissions.saving" : "permissions.save")}
                  </Button>
                  <Dialog.Close asChild>
                    <Button type="button" disabled={busy}>
                      {tr("permissions.cancel")}
                    </Button>
                  </Dialog.Close>
                </div>
              </form>
            )
          )}
        </DialogContent>
      </Dialog.Portal>
    </Dialog.Root>
    <Dialog.Root open={discard} onOpenChange={setDiscard}>
      <Dialog.Portal>
        <Dialog.Overlay className="dialog-overlay" />
        <DialogContent className="dialog compact-confirm">
          <Dialog.Title>{tr("permissions.discardTitle")}</Dialog.Title>
          <Dialog.Description>{tr("permissions.discardHelp")}</Dialog.Description>
          <div className="dialog-actions">
            <Button onClick={() => setDiscard(false)}>{tr("permissions.cancel")}</Button>
            <Button onClick={() => { setDiscard(false); setOpen(false); }}>{tr("permissions.discard")}</Button>
          </div>
        </DialogContent>
      </Dialog.Portal>
    </Dialog.Root>
    </>
  );
}
