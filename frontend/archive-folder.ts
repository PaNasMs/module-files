const archiveName = /\.(zip|rar|7z|tar|tgz|tbz2|txz|tar\.(gz|bz2|xz))$/i;
export function folderTarget(entry: {name: string; path: string; directory: boolean; link: boolean}) {
  if (entry.link) return undefined;
  if (entry.directory) return entry.path;
  if (archiveName.test(entry.name)) return "arc:" + encodeURIComponent(entry.path).replace(/[!'()*]/g, c => "%" + c.charCodeAt(0).toString(16).toUpperCase());
}
export function archiveSource(path: string) {
  if (!path.startsWith("arc:")) return "";
  try { return decodeURIComponent(path.slice(4).split("/")[0]); } catch { return ""; }
}
export function displayPath(path: string): string {
  const source = archiveSource(path);
  const slash = path.indexOf("/");
  return source ? displayPath(source) + (slash < 0 ? "" : path.slice(slash)) : path;
}

export function rarFolder(path: string): boolean {
  const source = archiveSource(path);
  return !!source && (/\.rar$/i.test(source) || rarFolder(source));
}

export async function resolveAddress(value: string, list: (folder: string) => Promise<{ name: string; path: string; directory: boolean; link: boolean }[]>) {
  if (!value.startsWith("/") && !value.startsWith("cloud:")) return value;
  const [first, ...parts] = value.split("/");
  let folder = first || "/";
  for (const name of parts.filter(Boolean)) {
    const next = folder.replace(/\/$/, "") + "/" + name;
    if (archiveName.test(name)) {
      const entry = (await list(folder).catch(() => [])).find(e => e.name === name);
      folder = entry && folderTarget(entry) || next;
    } else folder = next;
  }
  return folder;
}
