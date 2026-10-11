import { useState } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import * as Dialog from "@radix-ui/react-dialog";
import { mdiSourceBranch, mdiRefresh } from "@mdi/js";
import { managed, type Job } from "@panasms/operations";
import { newID } from "@panasms/layout";
import { waitForJob } from "@panasms/completion";
import {
  Button,
  Icon,
  Notice,
  DialogContent,
  WaitingSurface,
} from "@panasms/ui";
import { tr } from "./i18n";
export type GitState = {
  root: string;
  branch: string;
  head: string;
  branches: string[];
  changes: Record<string, string>;
  dirty: boolean;
  detached: boolean;
};
export function useGit(path: string) {
  return useQuery({
    queryKey: ["files-git", path],
    queryFn: () =>
      managed<{ git?: GitState }>("files", undefined, "git:" + path),
    enabled: path.startsWith("/") && !path.includes(".panasms-trash-"),
    retry: false,
    refetchOnWindowFocus: false,
    refetchOnReconnect: false,
    staleTime: Infinity,
    refetchOnMount: "always",
  });
}
export function GitStatus({ state, path }: { state?: GitState; path: string }) {
  if (!state) return null;
  const relative = path.slice(state.root.length + 1);
  const status =
    state.changes[relative] ??
    (Object.keys(state.changes).some((p) => p.startsWith(relative + "/"))
      ? "modified"
      : null);
  return status ? (
    <small className={"file-git-status git-" + status}>
      {tr("git." + status)}
    </small>
  ) : null;
}
export function GitPanel({
  state,
  error,
  refresh,
}: {
  state?: GitState;
  error: Error | null;
  refresh: () => void;
}) {
  const [open, setOpen] = useState(false),
    [branch, setBranch] = useState(""),
    [busy, setBusy] = useState(false),
    [failure, setFailure] = useState("");
  const q = useQueryClient();
  if (error)
    return (
      <Notice error>
        {tr("git.unavailable")} {error.message}
        <Button onClick={refresh} aria-label={tr("git.refresh")}>
          <Icon path={mdiRefresh} />
        </Button>
      </Notice>
    );
  if (!state) return null;
  const deleted = Object.entries(state.changes).filter(
    ([, s]) => s === "deleted",
  );
  async function change() {
    if (!state) return;
    setBusy(true);
    setFailure("");
    try {
      const action = "file.git-switch",
        params = {
          target: state.root,
          branch,
          expected: state.head + ":" + state.branch,
        };
      const plan = await managed<{ fingerprint: string; confirmation: string }>(
        "plan",
        { action, params },
      );
      const job = await managed<{ id: string }>("run", {
        id: newID(),
        action,
        params,
        ...plan,
      });
      await waitForJob(async () =>
        (await managed<Job[]>("jobs")).find((j) => j.id === job.id),
      );
      await q.invalidateQueries({ queryKey: ["files"] });
      await q.invalidateQueries({ queryKey: ["files-git"] });
      setOpen(false);
    } catch (e) {
      setFailure((e as Error).message);
    } finally {
      setBusy(false);
    }
  }
  return (
    <>
      <div className="file-git-bar">
        <Icon path={mdiSourceBranch} />
        <strong>{state.detached ? tr("git.detached") : state.branch}</strong>
        <span className="muted">
          {tr(state.dirty ? "git.dirty" : "git.clean")}
        </span>
        <Button
          onClick={() => {
            setBranch(state.branch);
            setFailure("");
            setOpen(true);
          }}
        >
          {tr("git.switch")}
        </Button>
        <Button
          onClick={refresh}
          aria-label={tr("git.refresh")}
          title={tr("git.refresh")}
        >
          <Icon path={mdiRefresh} />
        </Button>
      </div>
      {deleted.length > 0 && (
        <details className="file-git-deleted">
          <summary>
            {tr("git.deleted")} ({deleted.length})
          </summary>
          <ul>
            {deleted.map(([p]) => (
              <li key={p}>{p}</li>
            ))}
          </ul>
        </details>
      )}
      <Dialog.Root
        open={open}
        onOpenChange={(next) => {
          if (!busy) setOpen(next);
        }}
      >
        <Dialog.Portal>
          <Dialog.Overlay className="dialog-overlay" />
          <DialogContent
            className="settings-dialog"
            variant="form"
            intent="edit"
            busy={busy}
            header={
              <>
                <Dialog.Title>{tr("git.switch")}</Dialog.Title>
                <Dialog.Description>{tr("git.help")}</Dialog.Description>
              </>
            }
            footer={
              <div className="actions">
                <Button disabled={busy} onClick={() => setOpen(false)}>
                  {tr("git.cancel")}
                </Button>
                <Button
                  disabled={
                    busy || state.dirty || !branch || branch === state.branch
                  }
                  onClick={() => void change()}
                >
                  {tr("git.switch")}
                </Button>
              </div>
            }
          >
            <WaitingSurface busy={busy} message={tr("git.switching")}>
              <label>
                {tr("git.branch")}
                <select
                  value={branch}
                  onChange={(e) => setBranch(e.target.value)}
                  disabled={busy || state.dirty}
                >
                  <option value="" disabled>
                    {tr("git.branch")}
                  </option>
                  {state.branches.map((b) => (
                    <option key={b} value={b}>
                      {b}
                    </option>
                  ))}
                </select>
              </label>
              {state.dirty && <Notice>{tr("git.dirtyHelp")}</Notice>}
              {failure && <Notice error>{failure}</Notice>}
            </WaitingSurface>
          </DialogContent>
        </Dialog.Portal>
      </Dialog.Root>
    </>
  );
}
