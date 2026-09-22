import * as Dialog from "@radix-ui/react-dialog";
import { useState } from "react";
import { useQueryClient } from "@tanstack/react-query";
import { Button, DialogContent, Notice } from "@panasms/ui";
import { managed, type Job } from "@panasms/operations";
import { newID } from "@panasms/layout";
import { waitForJob } from "@panasms/completion";
import { tr } from "./i18n";

export function DeleteItems({
  items,
  inTrash,
  onClose,
  onRemoved,
}: {
  items: { path: string; name: string }[];
  inTrash: boolean;
  onClose: () => void;
  onRemoved: (paths: string[]) => void;
}) {
  const q = useQueryClient();
  const [remaining, setRemaining] = useState(items);
  const [busy, setBusy] = useState("");
  const [errors, setErrors] = useState<string[]>([]);
  async function remove(action: "file.trash" | "file.delete") {
    if (busy) return;
    const failures: string[] = [],
      removed: string[] = [];
    setErrors([]);
    for (const [index, item] of remaining.entries()) {
      setBusy(
        tr("delete.progress", {
          current: index + 1,
          count: remaining.length,
          name: item.name,
        }),
      );
      try {
        const params = { target: item.path };
        const plan = await managed<{
          fingerprint: string;
          confirmation: string;
        }>("plan", { action, params });
        const job = await managed<{ id: string }>("run", {
          id: newID(),
          action,
          params,
          fingerprint: plan.fingerprint,
          confirmation: plan.confirmation,
        });
        await waitForJob(async () => {
          const jobs = await managed<Job[]>("jobs");
          q.setQueryData(["jobs"], jobs);
          return jobs.find((j) => j.id === job.id);
        });
        removed.push(item.path);
      } catch (error) {
        failures.push(`${item.name}: ${(error as Error).message}`);
      }
    }
    onRemoved(removed);
    setRemaining((old) => old.filter((item) => !removed.includes(item.path)));
    setErrors(failures);
    setBusy("");
    void q.invalidateQueries({ queryKey: ["files"] });
    void q.invalidateQueries({ queryKey: ["file-places"] });
    if (!failures.length) onClose();
  }
  return (
    <Dialog.Root
      open
      onOpenChange={(open) => {
        if (!open && !busy) onClose();
      }}
    >
      <Dialog.Portal>
        <Dialog.Overlay className="dialog-overlay" />
        <DialogContent
          className="eject-confirm-dialog file-confirm-dialog"
          busy={!!busy}
          message={busy}
        >
          <Dialog.Title>{tr("delete.title")}</Dialog.Title>
          <Dialog.Description>
            {tr("delete.question", { count: remaining.length })}
          </Dialog.Description>
          <ul className="file-delete-targets">
            {remaining.map((item) => (
              <li key={item.path}>
                <strong>{item.name}</strong>
              </li>
            ))}
          </ul>
          {errors.map((error) => (
            <Notice key={error} error>
              {error}
            </Notice>
          ))}
          <div className="actions">
            {!inTrash && (
              <Button
                className="primary"
                disabled={!!busy}
                onClick={() => void remove("file.trash")}
              >
                {tr("move_to_trash_f8b39dea")}
              </Button>
            )}
            <Button
              disabled={!!busy}
              onClick={() => void remove("file.delete")}
            >
              {tr("delete.title")}
            </Button>
            <Button disabled={!!busy} autoFocus onClick={onClose}>
              {tr("delete.cancel")}
            </Button>
          </div>
        </DialogContent>
      </Dialog.Portal>
    </Dialog.Root>
  );
}
