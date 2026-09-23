import * as Dialog from "@radix-ui/react-dialog";
import { enqueueFiles } from "./uploads";
import { useQueryClient } from "@tanstack/react-query";
import { Button, DialogContent } from "@panasms/ui";
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
  function remove(kind: "trash" | "delete") {
    const destination =
      items[0].path.slice(0, items[0].path.lastIndexOf("/")) || "/";
    enqueueFiles(
      q,
      kind,
      items.map((item) => ({ ...item, directory: false })),
      destination,
    );
    onRemoved(items.map((item) => item.path));
    onClose();
  }
  return (
    <Dialog.Root
      open
      onOpenChange={(open) => {
        if (!open) onClose();
      }}
    >
      <Dialog.Portal>
        <Dialog.Overlay className="dialog-overlay" />
        <DialogContent
          className="eject-confirm-dialog file-confirm-dialog"
          header={
            <>
              {" "}
              <Dialog.Title>{tr("delete.title")}</Dialog.Title>
              <Dialog.Description>
                {tr("delete.question", { count: items.length })}
              </Dialog.Description>{" "}
            </>
          }
          footer={
            <div className="actions">
              {!inTrash && (
                <Button
                  className="primary"
                  onClick={() => void remove("trash")}
                >
                  {tr("move_to_trash_f8b39dea")}
                </Button>
              )}
              <Button className="danger" onClick={() => void remove("delete")}>
                {tr("delete.title")}
              </Button>
              <Button data-dialog-cancel autoFocus onClick={onClose}>
                {tr("delete.cancel")}
              </Button>
            </div>
          }
          variant="compact"
          intent="confirm"
          dirty={false}
        >
          <ul className="file-delete-targets">
            {items.map((item) => (
              <li key={item.path}>
                <strong>{item.name}</strong>
              </li>
            ))}
          </ul>
        </DialogContent>
      </Dialog.Portal>
    </Dialog.Root>
  );
}
