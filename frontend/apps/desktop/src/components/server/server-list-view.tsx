import { errorMessage } from "@/lib/errors";
import { ResourceWorkspace, useResourceWorkspace } from "@/components/workspace/resource-workspace";
import { resourceKey } from "@/components/workspace/workspace-model";
import { useI18n } from "@/i18n";
import { useState } from "react";
import { CheckCircle2, Pencil, Plus, RefreshCw, Server, Trash2 } from "lucide-react";
import { toast } from "sonner";
import { EmptyState } from "@/components/shared/empty-state";
import { PageShell } from "@/components/shared/page-shell";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card } from "@/components/ui/card";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Spinner } from "@/components/ui/spinner";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { cn } from "@/lib/utils";
import type { ServerProfile } from "@/types";

export function ServerListView({
  profiles,
  activeProfileId,
  authenticated,
  busy,
  error,
  onSelect,
  onAdd,
  onRetest,
  onEdit,
  onRemove,
}: {
  profiles: ServerProfile[];
  activeProfileId?: string;
  authenticated: boolean;
  busy: boolean;
  error?: string;
  onSelect(id: string): Promise<void> | void;
  onAdd(address: string): Promise<void>;
  onRetest(profile: ServerProfile): Promise<void>;
  onEdit(profile: ServerProfile, displayName: string, address: string): Promise<void>;
  onRemove(id: string): Promise<void>;
}) {
  const { t } = useI18n();
  const workspace = useResourceWorkspace();
  const [addOpen, setAddOpen] = useState(false);
  const [address, setAddress] = useState("");
  const [removeTarget, setRemoveTarget] = useState<ServerProfile>();
  const [editTarget, setEditTarget] = useState<ServerProfile>();
  const [editName, setEditName] = useState("");
  const [editAddress, setEditAddress] = useState("");

  async function add() {
    if (!address.trim() || busy) return;
    try {
      await onAdd(address.trim());
      setAddress("");
      setAddOpen(false);
    } catch {
      // The parent exposes the actionable error in the page alert.
    }
  }

  async function remove() {
    if (!removeTarget || busy) return;
    try {
      await onRemove(removeTarget.id);
      setRemoveTarget(undefined);
    } catch {
      // Keep the confirmation open so the user can retry after reviewing the error.
    }
  }

  function openEdit(profile: ServerProfile) {
    setEditTarget(profile);
    setEditName(profile.displayName || "");
    setEditAddress(profile.baseUrl);
  }

  async function edit() {
    if (!editTarget || !editAddress.trim() || busy) return;
    try {
      await onEdit(editTarget, editName.trim(), editAddress.trim());
      setEditTarget(undefined);
    } catch {
      // Keep the editor open so the user can correct the values.
    }
  }

  async function retest(profile: ServerProfile) {
    try {
      await onRetest(profile);
      toast.success("Server is reachable", { description: profile.baseUrl });
    } catch (reason) {
      toast.error("Server test failed", {
        description: errorMessage(reason),
      });
    }
  }

  const workspaceResources = profiles.map(item => ({
    key: resourceKey({ profileId: item.id, namespace: "", kind: "server", id: item.id }), label: item.displayName || item.id, namespace: "",
    fields: [[t("workspace.address"), item.baseUrl], [t("workspace.identity"), item.lastUserName], [t("network.colNamespace"), item.lastNamespace]] as Array<[string, React.ReactNode]>,
    actions: <>
      <Button variant="outline" size="sm" disabled={busy || item.id === activeProfileId} onClick={() => void onSelect(item.id)}>{t("workspace.useServer")}</Button>
      <Button variant="ghost" size="sm" disabled={busy} onClick={() => openEdit(item)}>{t("workspace.edit")}</Button>
      <Button variant="ghost" size="sm" disabled={busy} onClick={() => void retest(item)}>{t("network.refresh")}</Button>
    </>,
  }));
  return (
    <PageShell
      title="Servers"
      description="Manage Gateway service addresses and choose the Server used by this client."
      action={(
        <Button type="button" size="sm" disabled={busy} onClick={() => setAddOpen(true)}>
          <Plus size={14} data-icon="inline-start" />
          Add server
        </Button>
      )}
    >
      <ResourceWorkspace workspace={workspace} resources={workspaceResources} settled={!busy}>
      {profiles.length === 0 ? (
        <EmptyState icon={Server} title="No Servers" detail="Add the Gateway service address provided by your administrator." />
      ) : (
        <Card className="gap-0 overflow-hidden py-0 shadow-none">
          <Table>
            <TableHeader>
              <TableRow className="hover:bg-transparent">
                <TableHead>Server</TableHead>
                <TableHead>Service address</TableHead>
                <TableHead>Identity</TableHead>
                <TableHead>Status</TableHead>
                <TableHead className="w-[230px] text-right">Actions</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {profiles.map((profile) => {
                const active = profile.id === activeProfileId;
                return (
                  <TableRow
                    key={profile.id}
                    className={cn("cursor-pointer", active && "bg-muted/50")}
                    onClick={() => workspace.open(workspaceResources.find(item => item.key === resourceKey({ profileId: profile.id, namespace: "", kind: "server", id: profile.id }))!)}
                  >
                    <TableCell className="font-medium">
                      <div className="flex items-center gap-2">
                        <span className="truncate">{profile.displayName || profile.id}</span>
                        {active ? <Badge className="rounded-md bg-primary/10 text-[10px] text-primary">Active</Badge> : null}
                      </div>
                    </TableCell>
                    <TableCell className="max-w-[360px] truncate font-mono text-[11px] text-muted-foreground" title={profile.baseUrl}>
                      {profile.baseUrl}
                    </TableCell>
                    <TableCell className="text-muted-foreground">{profile.lastUserName || "—"}</TableCell>
                    <TableCell>
                      {active && authenticated ? (
                        <span className="inline-flex items-center gap-1.5 text-[11px] text-success"><CheckCircle2 size={13} />Signed in</span>
                      ) : (
                        <span className="text-[11px] text-muted-foreground">Saved</span>
                      )}
                    </TableCell>
                    <TableCell className="text-right">
                      <div className="flex items-center justify-end gap-1">
                        <Button
                          type="button"
                          variant="ghost"
                          size="icon-sm"
                          disabled={busy}
                          aria-label={`Edit ${profile.displayName || profile.id}`}
                          onClick={(event) => { event.stopPropagation(); openEdit(profile); }}
                        >
                          <Pencil size={14} />
                        </Button>
                        <Button
                          type="button"
                          variant="ghost"
                          size="icon-sm"
                          disabled={busy}
                          aria-label={`Retest ${profile.displayName || profile.id}`}
                          title="Retest"
                          onClick={(event) => { event.stopPropagation(); void retest(profile); }}
                        >
                          <RefreshCw size={14} />
                        </Button>
                        {!active ? (
                          <Button
                            type="button"
                            variant="outline"
                            size="sm"
                            disabled={busy}
                            onClick={(event) => { event.stopPropagation(); void onSelect(profile.id); }}
                          >
                            Use
                          </Button>
                        ) : null}
                        <Button
                          type="button"
                          variant="ghost"
                          size="icon-sm"
                          disabled={busy}
                          aria-label={`Remove ${profile.displayName || profile.id}`}
                          onClick={(event) => { event.stopPropagation(); setRemoveTarget(profile); }}
                        >
                          <Trash2 size={14} />
                        </Button>
                      </div>
                    </TableCell>
                  </TableRow>
                );
              })}
            </TableBody>
          </Table>
        </Card>
      )}

      {error ? <p role="alert" className="rounded-md border border-destructive/30 bg-destructive/5 p-3 text-sm text-destructive">{error}</p> : null}

      </ResourceWorkspace>
      <Dialog open={addOpen} onOpenChange={(open) => !busy && setAddOpen(open)}>
        <DialogContent showCloseButton={!busy}>
          <DialogHeader>
            <DialogTitle>Add server</DialogTitle>
            <DialogDescription>Enter the complete HTTP or HTTPS Gateway service address.</DialogDescription>
          </DialogHeader>
          <div className="space-y-2">
            <Label htmlFor="new-server-address">Service address</Label>
            <Input
              id="new-server-address"
              type="url"
              inputMode="url"
              placeholder="https://gateway.example.com"
              value={address}
              disabled={busy}
              autoFocus
              onChange={(event) => setAddress(event.target.value)}
              onKeyDown={(event) => { if (event.key === "Enter") void add(); }}
            />
          </div>
          <DialogFooter>
            <Button type="button" variant="outline" disabled={busy} onClick={() => setAddOpen(false)}>Cancel</Button>
            <Button type="button" disabled={busy || !address.trim()} onClick={() => void add()}>
              {busy ? <Spinner data-icon="inline-start" /> : <Plus size={14} data-icon="inline-start" />}
              Add server
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>

      <Dialog open={Boolean(removeTarget)} onOpenChange={(open) => !busy && !open && setRemoveTarget(undefined)}>
        <DialogContent showCloseButton={!busy}>
          <DialogHeader>
            <DialogTitle>Remove server?</DialogTitle>
            <DialogDescription>
              {removeTarget ? `${removeTarget.displayName || removeTarget.id} (${removeTarget.baseUrl}) will be removed from this device.` : ""}
            </DialogDescription>
          </DialogHeader>
          <DialogFooter>
            <Button type="button" variant="outline" disabled={busy} onClick={() => setRemoveTarget(undefined)}>Cancel</Button>
            <Button type="button" variant="destructive" disabled={busy} onClick={() => void remove()}>
              {busy ? <Spinner data-icon="inline-start" /> : <Trash2 size={14} data-icon="inline-start" />}
              Remove
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>

      <Dialog open={Boolean(editTarget)} onOpenChange={(open) => !busy && !open && setEditTarget(undefined)}>
        <DialogContent showCloseButton={!busy}>
          <DialogHeader>
            <DialogTitle>Edit server</DialogTitle>
            <DialogDescription>
              The address must still resolve to the same Gateway service ID.
            </DialogDescription>
          </DialogHeader>
          <div className="space-y-4">
            <div className="space-y-2">
              <Label htmlFor="edit-server-name">Display name</Label>
              <Input
                id="edit-server-name"
                value={editName}
                disabled={busy}
                onChange={(event) => setEditName(event.target.value)}
              />
            </div>
            <div className="space-y-2">
              <Label htmlFor="edit-server-address">Service address</Label>
              <Input
                id="edit-server-address"
                type="url"
                inputMode="url"
                value={editAddress}
                disabled={busy || (editTarget?.id === activeProfileId && authenticated)}
                onChange={(event) => setEditAddress(event.target.value)}
                onKeyDown={(event) => { if (event.key === "Enter") void edit(); }}
              />
              {editTarget?.id === activeProfileId && authenticated ? (
                <p className="text-xs text-muted-foreground">Sign out before changing the active Server address.</p>
              ) : null}
            </div>
          </div>
          <DialogFooter>
            <Button type="button" variant="outline" disabled={busy} onClick={() => setEditTarget(undefined)}>Cancel</Button>
            <Button type="button" disabled={busy || !editAddress.trim()} onClick={() => void edit()}>
              {busy ? <Spinner data-icon="inline-start" /> : <Pencil size={14} data-icon="inline-start" />}
              Save changes
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </PageShell>
  );
}
