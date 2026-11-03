import { managed } from "@panasms/operations";
import { archiveSource } from "./archive-folder";
import { tr } from "./i18n";
import messages from "./server-messages.json";

export async function listFolder<T>(folder: string, signal?: AbortSignal): Promise<T> {
  let source = folder;
  while (source.startsWith("arc:")) source = archiveSource(source);
  if (!folder.startsWith("arc:") || !source.startsWith("cloud:")) {
    return managed<T>("files", undefined, folder);
  }
  // Management queries time out after 30s. Preparing a remote archive is a file transfer.
  const params = new URLSearchParams({target: "archive-list:" + folder});
  const response = await fetch("/api/v1/files/content?" + params, { signal });
  if (!response.ok) throw Error(tr("native_144bf3a62d0f"));
  const listing = await response.json();
  if (listing.error) {
    const message = messages.find(row => row.en === listing.error);
    throw Error(message ? tr(message.key) : listing.error);
  }
  return listing;
}
