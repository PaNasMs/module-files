import { tr } from "./i18n";
import { useEffect, useRef, useState } from "react";
import { useQuery } from "@tanstack/react-query";
import {
  mdiFolderOutline,
  mdiFileOutline,
  mdiFileImageOutline,
  mdiFilePdfBox,
  mdiFileWordOutline,
  mdiFileExcelOutline,
  mdiFilePowerpointOutline,
  mdiFileMusicOutline,
  mdiFileVideoOutline,
  mdiFileCodeOutline,
  mdiFileCogOutline,
  mdiFileDocumentOutline,
  mdiFolderZipOutline,
  mdiApplicationOutline,
  mdiDisc,
  mdiLinkVariant,
} from "@mdi/js";
import { Icon } from "@ostojaos/ui";
type FileEntry = {
  name: string;
  path: string;
  directory: boolean;
  link: boolean;
  modified: number;
  size: number;
};
const groups: [string, string][] = [
  [
    "png jpg jpeg gif webp bmp tif tiff avif heic heif svg ico",
    mdiFileImageOutline,
  ],
  ["pdf", mdiFilePdfBox],
  ["doc docx odt rtf pages", mdiFileWordOutline],
  ["xls xlsx ods csv tsv numbers", mdiFileExcelOutline],
  ["ppt pptx odp key", mdiFilePowerpointOutline],
  ["mp3 flac wav ogg opus aac m4a wma aiff", mdiFileMusicOutline],
  ["mp4 mkv mov avi webm m4v mpg mpeg ts mts", mdiFileVideoOutline],
  ["zip rar 7z tar gz bz2 xz zst tgz", mdiFolderZipOutline],
  ["iso img dmg vhd vhdx qcow2", mdiDisc],
  [
    "js jsx tsx py go rs java c h cpp hpp cs rb php html css scss sql sh bash ps1 vue",
    mdiFileCodeOutline,
  ],
  ["json yaml yml toml ini conf cfg env xml", mdiFileCogOutline],
  ["txt md log rst tex", mdiFileDocumentOutline],
  ["exe msi appimage deb rpm apk bin", mdiApplicationOutline],
];
const icons = new Map(
  groups.flatMap(([extensions, icon]) =>
    extensions.split(" ").map((extension) => [extension, icon] as const),
  ),
);
const extension = (name: string) => name.toLowerCase().split(".").at(-1) ?? "";
export const fileIcon = (entry: Pick<FileEntry, "name" | "directory">) =>
  entry.directory
    ? mdiFolderOutline
    : (icons.get(extension(entry.name)) ?? mdiFileOutline);
export const hasThumbnail = (entry: FileEntry) =>
  !entry.directory &&
  !entry.link &&
  entry.size <= 64 * 1024 * 1024 &&
  /\.(jpe?g|png|gif|webp|bmp|tiff?)$/i.test(entry.name);
const observers = new Map<
  Element,
  {
    observer: IntersectionObserver;
    callbacks: Map<Element, (visible: boolean) => void>;
  }
>();
function observe(element: Element, callback: (visible: boolean) => void) {
  const root = element.closest(".file-content") ?? document.documentElement;
  let shared = observers.get(root);
  if (!shared) {
    const callbacks = new Map<Element, (visible: boolean) => void>();
    const observer = new IntersectionObserver(
      (entries) => {
        for (const entry of entries)
          callbacks.get(entry.target)?.(entry.isIntersecting);
      },
      { root, rootMargin: "160px" },
    );
    shared = { observer, callbacks };
    observers.set(root, shared);
  }
  shared.callbacks.set(element, callback);
  shared.observer.observe(element);
  return () => {
    shared.callbacks.delete(element);
    shared.observer.unobserve(element);
    if (!shared.callbacks.size) {
      shared.observer.disconnect();
      observers.delete(root);
    }
  };
}
function ThumbnailImage({ entry }: { entry: FileEntry }) {
  const [url, setUrl] = useState("");
  const [failed, setFailed] = useState(false);
  const image = useQuery({
    queryKey: ["thumbnail", entry.path, entry.modified, entry.size],
    staleTime: Infinity,
    gcTime: 30000,
    retry: false,
    queryFn: async ({ signal }) => {
      const response = await fetch(
        "/api/v1/files/content?thumbnail=1&target=" +
          encodeURIComponent(entry.path),
        { signal },
      );
      if (!response.ok) throw Error(tr("thumbnail_unavailable_7e25dc75"));
      return response.blob();
    },
  });
  useEffect(() => {
    setFailed(false);
    if (!image.data) {
      setUrl("");
      return;
    }
    const reader = new FileReader();
    reader.onload = () => setUrl(String(reader.result));
    reader.readAsDataURL(image.data);
    return () => {
      reader.onload = null;
      if (reader.readyState === FileReader.LOADING) reader.abort();
    };
  }, [image.data]);
  return (
    <>
      {url && !failed ? (
        <img
          src={url}
          alt=""
          decoding="async"
          draggable={false}
          onError={() => setFailed(true)}
        />
      ) : (
        <Icon path={fileIcon(entry)} />
      )}
    </>
  );
}
function Thumbnail({ entry }: { entry: FileEntry }) {
  const ref = useRef<HTMLSpanElement>(null);
  const [visible, setVisible] = useState(false);
  useEffect(
    () => (ref.current ? observe(ref.current, setVisible) : undefined),
    [],
  );
  return (
    <span ref={ref} className="file-visual file-thumbnail" aria-hidden="true">
      {visible ? (
        <ThumbnailImage entry={entry} />
      ) : (
        <Icon path={fileIcon(entry)} />
      )}
    </span>
  );
}
export function FileVisual({
  entry,
  tiles,
}: {
  entry: FileEntry;
  tiles: boolean;
}) {
  if (tiles && hasThumbnail(entry)) return <Thumbnail entry={entry} />;
  return (
    <span className="file-visual" aria-hidden="true">
      <Icon path={fileIcon(entry)} />
      {entry.link && (
        <span className="file-link-mark">
          <Icon path={mdiLinkVariant} />
        </span>
      )}
    </span>
  );
}
