import { useState } from "react";
import { mdiArrowLeft, mdiChevronRight, mdiContentCopy, mdiContentCut, mdiFileReplaceOutline, mdiRenameBox, mdiSkipNext } from "@mdi/js";
import { Button, Icon } from "@panasms/ui";
import { tr } from "./i18n";
import type { ConflictMode } from "./uploads";

export function DropActions({ onChoose }: { onChoose: (action: "copy" | "move", conflict: ConflictMode) => void }) {
  const [action, setAction] = useState<"copy" | "move" | null>(null);
  if (!action) return <>{(["copy", "move"] as const).map(kind => <Button key={kind} autoFocus={kind === "copy"} role="menuitem" aria-haspopup="menu" aria-label={tr(`drop.${kind}`)} onClick={() => setAction(kind)} onKeyDown={event => { if (event.key === "ArrowRight") { event.preventDefault(); setAction(kind); } }}><Icon path={kind === "copy" ? mdiContentCopy : mdiContentCut} /><Icon path={mdiChevronRight} /></Button>)}</>;
  return <div role="menu" aria-label={tr(`drop.${action}`)} onKeyDown={event => { if (event.key === "ArrowLeft") { event.preventDefault(); setAction(null); } }}>
    <Button role="menuitem" aria-label={tr(`drop.${action}`)} onClick={() => setAction(null)}><Icon path={mdiArrowLeft} /></Button>
    <hr />
    {(["skip", "rename", "replace"] as const).map((mode, index) => <Button key={mode} role="menuitem" autoFocus={index === 0} aria-label={tr(`drop.${mode}`)} onClick={() => onChoose(action, mode)}><Icon path={{ skip: mdiSkipNext, rename: mdiRenameBox, replace: mdiFileReplaceOutline }[mode]} /></Button>)}
  </div>;
}
