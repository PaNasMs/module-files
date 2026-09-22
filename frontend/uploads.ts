import type { QueryClient } from "@tanstack/react-query";
import { newID } from "@panasms/layout";
import { tr } from "./i18n";

// The shell observes this cache entry; transfers belong to the session, not a route.
export const uploadKey = ["file-uploads"] as const;
export type UploadTask = {
  id: string;
  destination: string;
  name: string;
  count: number;
  completed: number;
  loaded: number;
  total: number;
  status: "queued" | "running" | "succeeded" | "failed" | "cancelled";
  errors: string[];
  cancel: () => void;
};
const queues = new WeakMap<QueryClient, Promise<void>>();
export function enqueueUpload(
  q: QueryClient,
  files: File[],
  destination: string,
) {
  if (!files.length) return;
  q.setQueryDefaults(uploadKey, { gcTime: Infinity, staleTime: Infinity });
  const id = newID();
  let cancelled = false;
  let xhr: XMLHttpRequest | undefined;
  const cancel = () => {
    cancelled = true;
    xhr?.abort();
    publish({ status: "cancelled" });
    window.removeEventListener("beforeunload", beforeUnload);
  };
  let task: UploadTask = {
    id,
    destination,
    name: files[0].name,
    count: files.length,
    completed: 0,
    loaded: 0,
    total: files.reduce((sum, file) => sum + file.size, 0),
    status: "queued",
    errors: [],
    cancel,
  };
  const publish = (patch: Partial<UploadTask>) => {
    task = { ...task, ...patch };
    q.setQueryData<UploadTask[]>(uploadKey, (old) =>
      old?.map((item) => (item.id === id ? task : item)),
    );
  };
  q.setQueryData<UploadTask[]>(uploadKey, (old) => [
    ...(old ?? []).filter(
      (item) => item.status === "queued" || item.status === "running",
    ),
    ...(old ?? [])
      .filter((item) => item.status !== "queued" && item.status !== "running")
      .slice(-19),
    task,
  ]);
  const beforeUnload = (e: BeforeUnloadEvent) => {
    e.preventDefault();
    e.returnValue = "";
  };
  window.addEventListener("beforeunload", beforeUnload);
  const unsubscribe = q.getQueryCache().subscribe((event) => {
    if (event.type === "removed" && event.query.queryKey[0] === uploadKey[0])
      cancel();
  });
  const run = async () => {
    let processedBytes = 0;
    try {
      for (const file of files) {
        if (cancelled) break;
        publish({ status: "running", name: file.name });
        try {
          await new Promise<void>((resolve, reject) => {
            xhr = new XMLHttpRequest();
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
                publish({
                  loaded: processedBytes + Math.min(file.size, e.loaded),
                });
            };
            xhr.onload = () =>
              xhr?.status === 204
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
          publish({ completed: task.completed + 1 });
          void q.invalidateQueries({ queryKey: ["files"] });
        } catch (error) {
          if (!cancelled)
            publish({
              errors: [
                ...task.errors,
                `${file.name}: ${(error as Error).message}`,
              ],
            });
        } finally {
          xhr = undefined;
        }
        processedBytes += file.size;
        if (!cancelled) publish({ loaded: processedBytes });
      }
      publish({
        status: cancelled
          ? "cancelled"
          : task.errors.length
            ? "failed"
            : "succeeded",
        cancel: () => {},
      });
      if (!cancelled)
        window.dispatchEvent(
          new CustomEvent("panasms:toast", {
            detail: tr(
              task.errors.length ? "upload.failed" : "upload.completed",
              { completed: task.completed, count: task.count, destination },
            ),
          }),
        );
    } finally {
      files = [];
      unsubscribe();
      window.removeEventListener("beforeunload", beforeUnload);
      void q.invalidateQueries({ queryKey: ["files"] });
    }
  };
  const pending = (queues.get(q) ?? Promise.resolve()).then(run);
  queues.set(q, pending);
  void pending.finally(() => {
    if (queues.get(q) === pending) queues.delete(q);
  });
}
