import { useLayoutEffect, useMemo, useRef, useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { managed } from "@panasms/operations";
import { Button, Icon, Notice, bytes } from "@panasms/ui";
import { mdiArrowUp } from "@mdi/js";
import { fileIcon } from "./file-visual";
import { tr } from "./i18n";

export const isArchive = (name: string) =>
  /\.(zip|rar|7z|tar|tgz|tbz2|txz|tar\.(gz|bz2|xz))$/i.test(name);

type Member = { name: string; directory?: boolean; size?: number };

export function ArchivePreview({ path }: { path: string }) {
  const listing = useQuery({
    queryKey: ["files-archive", path],
    queryFn: () =>
      managed<{ entries: Member[] }>("files", undefined, "archive:" + path),
    retry: false,
    gcTime: 0,
    refetchOnWindowFocus: false,
  });
  const [folder, setFolder] = useState("");
  const [limit, setLimit] = useState(100);
  const location = useRef<HTMLHeadingElement>(null);
  const moving = useRef(false);
  function navigate(next: string) {
    moving.current = true;
    setLimit(100);
    setFolder(next);
  }
  useLayoutEffect(() => {
    if (moving.current) location.current?.focus();
    moving.current = false;
  }, [folder]);
  const children = useMemo(() => {
    const folders = new Set<string>();
    const files: Member[] = [];
    for (const item of listing.data?.entries ?? []) {
      const name = item.name
        .split("/")
        .filter((part) => part && part !== ".")
        .join("/");
      if (!name || !name.startsWith(folder)) continue;
      const relative = name.slice(folder.length);
      const slash = relative.indexOf("/");
      if (slash >= 0) folders.add(relative.slice(0, slash));
      else if (item.directory) folders.add(relative);
      else files.push({ ...item, name: relative });
    }
    return [
      ...Array.from(folders)
        .sort()
        .map((name) => ({ name, directory: true }) as Member),
      ...files.sort((a, b) => a.name.localeCompare(b.name)),
    ];
  }, [folder, listing.data]);
  if (listing.isPending) return <p role="status">{tr("loading_b6819e91")}</p>;
  if (listing.error)
    return (
      <>
        <Notice error>{listing.error.message}</Notice>
        <Button
          onClick={() => void listing.refetch()}
          disabled={listing.isFetching}
        >
          {tr("retry_9e506acb")}
        </Button>
      </>
    );
  return (
    <section className="archive-preview">
      <h3 ref={location} tabIndex={-1}>
        {folder || tr("archive.contents")}
      </h3>
      {folder && (
        <Button
          onClick={() => navigate(folder.slice(0, -1).replace(/[^/]+$/, ""))}
        >
          <Icon path={mdiArrowUp} />
          {tr("up_one_level_25ee7583")}
        </Button>
      )}
      {children.length ? (
        <ul>
          {children.slice(0, limit).map((item, i) => (
            <li key={i}>
              {item.directory ? (
                <Button onClick={() => navigate(folder + item.name + "/")}>
                  <Icon path={fileIcon({ name: item.name, directory: true })} />
                  <bdi>{item.name}</bdi>
                </Button>
              ) : (
                <>
                  <Icon
                    path={fileIcon({ name: item.name, directory: false })}
                  />
                  <bdi>{item.name}</bdi>
                  {item.size != null && <small>{bytes(item.size)}</small>}
                </>
              )}
            </li>
          ))}
        </ul>
      ) : (
        <p>{tr("archive.empty")}</p>
      )}
      {children.length > limit && (
        <Button onClick={() => setLimit(limit + 100)}>
          {tr("archive.more")}
        </Button>
      )}
    </section>
  );
}
