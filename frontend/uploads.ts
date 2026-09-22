import type { QueryClient } from "@tanstack/react-query";
import { newID } from "@panasms/layout";
import { managed, type Job } from "@panasms/operations";
import { tr } from "./i18n";

// The shell observes this session-local contract, independently of the Files route.
export const uploadKey = ["file-uploads"] as const;
export type FileItem = { name: string; path: string; directory: boolean; revision?: string; link?: boolean };
export type FileAction = "upload" | "copy" | "move" | "trash" | "delete";
type Decision = { mode: "replace" | "rename" | "skip"; name?: string; all?: boolean };
export type FileTask = {
  id: string; kind: FileAction; destination: string; name: string;
  count: number; completed: number; processed: number; skipped: number;
  loaded: number; total: number; status: "queued" | "running" | "waiting" | "succeeded" | "failed" | "cancelled";
  errors: string[]; stage?: string; jobId?: string; jobIds: string[]; cancelling?: boolean; cancel: () => void;
  conflict?: { name: string; destination: string; suggested: string; directory: boolean; replaceAllowed: boolean; resolve: (choice: Decision) => void };
};
const queues = new WeakMap<QueryClient, Promise<void>>();
const terminal = new Set(["succeeded", "failed", "cancelled", "interrupted"]);
const join = (folder: string, name: string) => folder.replace(/\/$/, "") + "/" + name;
export function renamed(name: string, names: Set<string>, directory: boolean) {
  const dot = directory ? -1 : name.lastIndexOf(".");
  const stem = dot > 0 ? name.slice(0, dot) : name, extension = dot > 0 ? name.slice(dot) : "";
  for (let i = 1; ; i++) {
    const candidate = `${stem} (${i})${extension}`;
    if (!names.has(candidate)) return candidate;
  }
}
export const validName = (name: string) => !!name && name === name.trim() && !/[\/\x00-\x1f\x7f]/.test(name) && ![".", ".."].includes(name) && new TextEncoder().encode(name).length <= 255;
export function enqueueUpload(q: QueryClient, files: File[], destination: string) {
  enqueue(q, "upload", files.map(file => ({ name: file.name, path: "", directory: false })), destination, files);
}
export function enqueueFiles(q: QueryClient, kind: Exclude<FileAction, "upload">, items: FileItem[], destination: string) {
  enqueue(q, kind, items, destination);
}
function enqueue(q: QueryClient, kind: FileAction, items: FileItem[], destination: string, files: File[] = []) {
  if (!items.length) return;
  q.setQueryDefaults(uploadKey, { gcTime: Infinity, staleTime: Infinity });
  const id = newID();
  let cancelled = false, sessionAlive = true;
  let xhr: XMLHttpRequest | undefined;
  let decide: ((choice: Decision) => void) | undefined;
  let preference: Decision["mode"] | undefined;
  const beforeUnload = (e: BeforeUnloadEvent) => { e.preventDefault(); e.returnValue = ""; };
  const cancel = () => {
    cancelled = true;
    window.removeEventListener("beforeunload", beforeUnload);
    xhr?.abort();
    decide?.({ mode: "skip" });
    publish(task.status === "queued" || task.status === "waiting" ? { status: "cancelled", conflict: undefined } : { cancelling: true });
  };
  let task: FileTask = {
    id, kind, destination, name: items[0].name, count: items.length,
    completed: 0, processed: 0, skipped: 0, loaded: 0,
    total: files.reduce((sum, file) => sum + file.size, 0), status: "queued", errors: [], jobIds: [], cancel,
  };
  const publish = (patch: Partial<FileTask>) => {
    task = { ...task, ...patch };
    if (sessionAlive) q.setQueryData<FileTask[]>(uploadKey, old => old?.map(item => item.id === id ? task : item));
  };
  q.setQueryData<FileTask[]>(uploadKey, old => [
    ...(old ?? []).filter(item => !terminal.has(item.status)),
    ...(old ?? []).filter(item => terminal.has(item.status)).slice(-19), task,
  ]);
  window.addEventListener("beforeunload", beforeUnload);
  const unsubscribe = q.getQueryCache().subscribe(event => {
    if (event.type === "removed" && event.query.queryKey[0] === uploadKey[0]) { sessionAlive = false; cancel(); }
  });
  async function listing() {
    const data = await managed<{ entries: FileItem[] }>("files", undefined, destination);
    return data.entries;
  }
  async function resolveDestination(item: FileItem, forceAsk = false): Promise<{ target: string; revision?: string } | null> {
    let name = item.name;
    while (!cancelled) {
      const entries = await listing();
      if (cancelled) return null;
      const existing = entries.find(entry => entry.name === name);
      if (!existing) return { target: join(destination, name) };
      const names = new Set(entries.map(entry => entry.name));
      const suggested = renamed(item.name, names, item.directory);
      const replaceAllowed = !existing.link && !!existing.revision && existing.directory === item.directory && existing.path !== item.path;
      let decision: Decision;
      if (preference && !forceAsk && (preference !== "replace" || replaceAllowed)) decision = { mode: preference };
      else {
        decision = await new Promise<Decision>(resolve => {
          decide = resolve;
          publish({ status: "waiting", conflict: { name, destination: join(destination, name), suggested, directory: item.directory, replaceAllowed, resolve } });
          window.dispatchEvent(new CustomEvent("panasms:toast", { detail: tr("task.conflictNotice", { name }) }));
        });
        decide = undefined;
        publish({ status: "running", conflict: undefined });
      }
      if (cancelled) return null;
      if (decision.all) preference = decision.mode;
      if (decision.mode === "skip") { publish({ skipped: task.skipped + 1 }); return null; }
      if (decision.mode === "replace") {
        if (!replaceAllowed) throw Error(tr("task.replaceUnavailable"));
        return { target: join(destination, name), revision: existing.revision };
      }
      name = decision.name || suggested;
      if (!validName(name)) throw Error(tr("task.invalidName"));
      forceAsk = true;
    }
    return null;
  }
  async function waitJob(id: string) {
    let sentCancel = false, missing = 0;
    while (sessionAlive) {
      let jobs: (Job & { canCancel?: boolean })[];
      try { jobs = await managed<(Job & { canCancel?: boolean })[]>("jobs"); }
      catch {
        publish({ stage: tr("task.reconnecting") });
        await new Promise(resolve => setTimeout(resolve, 2000));
        continue;
      }
      if (!sessionAlive) return;
      q.setQueryData(["jobs"], jobs);
      const job = jobs.find(job => job.id === id);
      if (!job) { if (++missing > 5) throw Error(tr("task.unknownResult")); }
      else {
        missing = 0;
        if (terminal.has(job.status)) {
          if (job.status !== "succeeded") throw Error(typeof job.result?.error === "string" ? job.result.error : job.stage);
          return;
        }
        publish({ stage: cancelled ? tr("task.stopping") : job.stage });
        if (cancelled && job.canCancel && !sentCancel) {
          try { await managed("cancel", { id }); sentCancel = true; } catch { /* Retry while the job remains cancellable. */ }
        }
      }
      await new Promise(resolve => setTimeout(resolve, 500));
    }
  }
  async function operation(item: FileItem, target?: string, revision?: string) {
    const action = "file." + kind;
    const params = { target: item.path, ...(target ? { destination: target } : {}), ...(revision ? { replace_revision: revision } : {}) };
    const plan = await managed<{ fingerprint: string; confirmation: string }>("plan", { action, params });
    if (cancelled) return false;
    const job = await managed<{ id: string }>("run", { id: newID(), action, params, fingerprint: plan.fingerprint, confirmation: plan.confirmation });
    publish({ jobId: job.id, jobIds: [...task.jobIds, job.id] });
    await waitJob(job.id);
    publish({ jobId: undefined, stage: undefined });
    return sessionAlive;
  }
  function upload(file: File, target: string, revision: string | undefined, processedBytes: number) {
    return new Promise<void>((resolve, reject) => {
      xhr = new XMLHttpRequest();
      const query = new URLSearchParams({ target, ...(revision ? { replace_revision: revision } : {}) });
      xhr.open("PUT", "/api/v1/files/content?" + query);
      xhr.setRequestHeader("X-PaNasMs-Request", "1");
      xhr.upload.onprogress = e => { if (e.lengthComputable) publish({ loaded: processedBytes + Math.min(file.size, e.loaded) }); };
      xhr.onload = () => {
        if (xhr?.status === 204) resolve();
        else reject(Object.assign(Error(tr("check_permissions_and_ensure_the_file_name_is_not__5eb4d1e2")), { conflict: xhr?.status === 409 }));
      };
      xhr.onerror = () => reject(Error(tr("transfer_interrupted_74125ab8")));
      xhr.onabort = () => reject(Error(tr("transfer_cancelled_40cfcf6a")));
      xhr.send(file);
    });
  }
  const run = async () => {
    let processedBytes = 0;
    try {
      for (const [index, item] of items.entries()) {
        if (cancelled) break;
        publish({ status: "running", name: item.name });
        try {
          if (kind === "upload" || kind === "copy" || kind === "move") {
            let destinationInfo = await resolveDestination(item);
            if (destinationInfo && !cancelled) {
              if (kind === "upload") {
                try { await upload(files[index], destinationInfo.target, destinationInfo.revision, processedBytes); }
                catch (error) {
                  if (cancelled || !(error as { conflict?: boolean }).conflict) throw error;
                  const entries = await listing();
                  if (!entries.some(entry => entry.path === destinationInfo!.target)) throw error;
                  destinationInfo = await resolveDestination({ ...item, name: destinationInfo.target.split('/').at(-1)! }, true);
                  if (destinationInfo && !cancelled) await upload(files[index], destinationInfo.target, destinationInfo.revision, processedBytes);
                }
                if (destinationInfo && !cancelled) publish({ completed: task.completed + 1 });
              } else if (await operation(item, destinationInfo.target, destinationInfo.revision)) publish({ completed: task.completed + 1 });
            }
          } else if (await operation(item)) publish({ completed: task.completed + 1 });
        } catch (error) {
          if (!cancelled) publish({ errors: [...task.errors, `${item.name}: ${(error as Error).message}`] });
        } finally { xhr = undefined; publish({ jobId: undefined, stage: undefined }); }
        processedBytes += files[index]?.size ?? 0;
        publish({ processed: task.processed + 1, ...(cancelled ? {} : { loaded: processedBytes }) });
        if (sessionAlive) void q.invalidateQueries({ queryKey: ["files"] });
      }
      publish({ status: cancelled ? "cancelled" : task.errors.length ? "failed" : "succeeded", conflict: undefined, cancelling: false, cancel: () => {} });
      if (sessionAlive && !cancelled) window.dispatchEvent(new CustomEvent("panasms:toast", { detail: tr(task.errors.length ? "task.failed" : "task.completed", { completed: task.completed, skipped: task.skipped, count: task.count }) }));
    } finally {
      files = [];
      unsubscribe();
      window.removeEventListener("beforeunload", beforeUnload);
      if (sessionAlive) void q.invalidateQueries({ queryKey: ["files"] });
    }
  };
  const pending = (queues.get(q) ?? Promise.resolve()).then(run);
  queues.set(q, pending);
  void pending.finally(() => { if (queues.get(q) === pending) queues.delete(q); });
}
