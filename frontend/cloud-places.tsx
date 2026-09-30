import { useState } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import * as Dialog from "@radix-ui/react-dialog";
import { ProviderConnect, type ExternalGrant } from "@panasms/external";
import { request } from "@panasms/client";
import { Button, DialogContent, Notice } from "@panasms/ui";
import { tr } from "./i18n";
type Connection = { id: string; provider: string; email: string; name: string };
export function useCloudPlaces() {
  const q = useQueryClient();
  const [open, setOpen] = useState(false);
  const [selected, setSelected] = useState("");
  const connections = useQuery({ queryKey: ["external-connections"], queryFn: () => request<Connection[]>("external/connections"), staleTime: 60000 });
  const grants = useQuery({ queryKey: ["external-grants"], queryFn: () => request<ExternalGrant[]>("external/grants"), staleTime: 30000 });
  const available = (connections.data ?? []).filter(c => c.provider === "google" || c.provider === "dropbox");
  const chosen = available.find(c => c.id === selected) ?? available[0];
  const places = (grants.data ?? []).filter(g => g.consumer === "files").flatMap(grant => {
    const account = available.find(c => c.id === grant.connectionId);
    return account ? [{ path: "cloud:" + grant.id, kind: "cloud" as const, provider: account.provider, name: `${account.provider === "google" ? "Google Drive" : "Dropbox"} · ${account.email || account.name}` }] : [];
  });
  const dialog = <Dialog.Root open={open} onOpenChange={setOpen}><Dialog.Portal><Dialog.Overlay className="dialog-overlay" /><DialogContent className="settings-dialog" variant="form" intent="edit" header={<><Dialog.Title>{tr("cloud.connect")}</Dialog.Title><Dialog.Description>{tr("cloud.permission")}</Dialog.Description></>} footer={<Button onClick={() => setOpen(false)}>{tr("delete.cancel")}</Button>}>
    {(connections.error || grants.error) && <Notice>{(connections.error || grants.error)?.message}</Notice>}
    {connections.isPending ? <p>{tr("loading_b6819e91")}</p> : chosen ? <>
      <label>{tr("cloud.account")}<select value={chosen.id} onChange={e => setSelected(e.target.value)}>{available.map(c => <option key={c.id} value={c.id}>{c.provider === "google" ? "Google Drive" : "Dropbox"} · {c.email || c.name}</option>)}</select></label>
      <ProviderConnect key={chosen.id} providerId={chosen.provider as "google" | "dropbox"} grant={{connectionId: chosen.id, consumer: "files", capability: chosen.provider === "google" ? "google-drive" : "dropbox-files"}} onComplete={id => { if (id) { void q.invalidateQueries({ queryKey: ["external-grants"] }); setOpen(false); } }} />
    </> : <p>{tr("cloud.linkFirst")} <a href="/profile/connections">{tr("cloud.profile")}</a></p>}
  </DialogContent></Dialog.Portal></Dialog.Root>;
  return { places, dialog, connect: () => setOpen(true) };
}
