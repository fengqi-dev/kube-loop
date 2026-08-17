import { FormEvent, useCallback, useEffect, useState } from "react";
import { Plus, RefreshCw } from "lucide-react";
import { Button, ConfirmDialog, Empty, Loading, Notice, PageHeader } from "../components";
import { mutation, request } from "../api";
import type {
  ListResponse,
  LocalUser,
  OAuthClient,
} from "../types";
import {
  Dialog,
  DialogContent,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";

const copy = {
  "zh-CN": {
    create: "新建",
    refresh: "刷新",
    empty: "暂无数据",
    reason: "变更原因（至少 8 个字符）",
    save: "保存",
    cancel: "取消",
  },
  "en-US": {
    create: "Create",
    refresh: "Refresh",
    empty: "No data",
    reason: "Change reason (at least 8 characters)",
    save: "Save",
    cancel: "Cancel",
  },
};

function locale() {
  return document.documentElement.lang === "en-US" ? "en-US" : "zh-CN";
}

function useList<T>(path: string | null) {
  const [items, setItems] = useState<T[]>([]),
    [loading, setLoading] = useState(true),
    [error, setError] = useState("");
  const reload = useCallback(async () => {
    if (!path) {
      setItems([]);
      setLoading(false);
      return;
    }
    setLoading(true);
    setError("");
    try {
      setItems((await request<ListResponse<T>>(path)).items || []);
    } catch (cause) {
      setError((cause as Error).message);
    } finally {
      setLoading(false);
    }
  }, [path]);
  useEffect(() => {
    void reload();
  }, [reload]);
  return { items, loading, error, reload };
}

function ResourcePage<T extends object>({
  title,
  description,
  path,
  columns,
  create,
  actions,
}: {
  title: string;
  description: string;
  path: string | null;
  columns: Array<[string, keyof T | ((item: T) => string)]>;
  create?: (reload: () => Promise<void>) => React.ReactNode;
  actions?: (item: T, reload: () => Promise<void>) => React.ReactNode;
}) {
  const state = useList<T>(path),
    text = copy[locale()];
  return (
    <>
      <PageHeader
        title={title}
        description={description}
        actions={
          <>
            <Button onClick={() => void state.reload()}>
              <RefreshCw size={14} />
              {text.refresh}
            </Button>
            {create?.(state.reload)}
          </>
        }
      />
      {state.error && <Notice>{state.error}</Notice>}
      {state.loading ? (
        <Loading />
      ) : !state.items.length ? (
        <Empty>{text.empty}</Empty>
      ) : (
        <div className="table-panel">
          <table>
            <thead>
              <tr>
                {columns.map(([label]) => (
                  <th key={label}>{label}</th>
                ))}
                {actions && <th>{locale() === "zh-CN" ? "操作" : "Actions"}</th>}
              </tr>
            </thead>
            <tbody>
              {state.items.map((item, index) => (
                <tr key={(item as { id?: string; identityId?: string }).id || (item as { identityId?: string }).identityId || index}>
                  {columns.map(([label, value]) => (
                    <td key={label}>
                      {typeof value === "function"
                        ? value(item)
                        : String(item[value] ?? "—")}
                    </td>
                  ))}
                  {actions && <td>{actions(item, state.reload)}</td>}
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
    </>
  );
}

export function UsersPage() {
  const zh = locale() === "zh-CN";
  return (
    <ResourcePage<LocalUser>
      title={zh ? "用户" : "Users"}
      description={
        zh
          ? "登录用户可以管理所有用户。"
          : "Signed-in users can manage every user."
      }
      path="/users"
      columns={[
        ["Name", "displayName"],
        ["Username", "username"],
        ["Email", (item) => item.email || "—"],
        ["Status", (item) => (item.enabled ? "active" : "disabled")],
      ]}
      actions={(user, reload) => (
        <StatusToggleButton
          enabled={user.enabled}
          path={`/users/${user.identityId}/status`}
          reload={reload}
          resource={zh ? `用户 ${user.displayName}` : `user ${user.displayName}`}
        />
      )}
      create={(reload) => <UserCreateButton reload={reload} />}
    />
  );
}

function UserCreateButton({ reload }: { reload: () => Promise<void> }) {
  const zh = locale() === "zh-CN",
    [open, setOpen] = useState(false),
    [busy, setBusy] = useState(false),
    [error, setError] = useState("");
  const save = async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    setBusy(true);
    setError("");
    const data = new FormData(event.currentTarget);
    try {
      await mutation(
        "/users",
        "POST",
        {
          username: data.get("username"),
          displayName: data.get("displayName"),
          email: data.get("email"),
          password: data.get("password"),
        },
        { idempotent: true },
      );
      await reload();
      setOpen(false);
    } catch (cause) {
      setError((cause as Error).message);
    } finally {
      setBusy(false);
    }
  };
  return (
    <>
      <Button kind="primary" onClick={() => setOpen(true)}>
        <Plus size={14} />
        {copy[locale()].create}
      </Button>
      <Dialog open={open} onOpenChange={setOpen}>
        <DialogContent>
          <form onSubmit={save}>
            <DialogHeader>
              <DialogTitle>
                {zh ? "新建本地用户" : "Create local user"}
              </DialogTitle>
            </DialogHeader>
            {error && <Notice>{error}</Notice>}
            <div className="form-grid">
              <label>
                {zh ? "用户名" : "Username"}
                <Input name="username" required />
              </label>
              <label>
                {zh ? "显示名称" : "Display name"}
                <Input name="displayName" required />
              </label>
              <label>
                {zh ? "邮箱" : "Email"}
                <Input name="email" type="email" />
              </label>
              <label>
                {zh ? "初始密码" : "Initial password"}
                <Input
                  name="password"
                  type="password"
                  minLength={12}
                  required
                />
              </label>
            </div>
            <DialogFooter>
              <Button type="button" onClick={() => setOpen(false)}>
                {copy[locale()].cancel}
              </Button>
              <Button type="submit" kind="primary" busy={busy}>
                {copy[locale()].save}
              </Button>
            </DialogFooter>
          </form>
        </DialogContent>
      </Dialog>
    </>
  );
}

function StatusToggleButton({
  enabled,
  path,
  reload,
  resource,
}: {
  enabled: boolean;
  path: string;
  reload: () => Promise<void>;
  resource: string;
}) {
  const zh = locale() === "zh-CN",
    [open, setOpen] = useState(false),
    [busy, setBusy] = useState(false),
    [error, setError] = useState("");
  const update = async (reason: string) => {
    setBusy(true);
    setError("");
    try {
      await mutation(path, "PATCH", { enabled: !enabled, reason });
      await reload();
      setOpen(false);
    } catch (cause) {
      setError((cause as Error).message);
    } finally {
      setBusy(false);
    }
  };
  return (
    <>
      <Button
        kind={enabled ? "danger" : "secondary"}
        onClick={() => setOpen(true)}
      >
        {enabled ? (zh ? "禁用" : "Disable") : zh ? "启用" : "Enable"}
      </Button>
      {error && <Notice>{error}</Notice>}
      <ConfirmDialog
        open={open}
        busy={busy}
        title={
          enabled
            ? zh
              ? `禁用${resource}？`
              : `Disable ${resource}?`
            : zh
              ? `启用${resource}？`
              : `Enable ${resource}?`
        }
        detail={
          enabled
            ? zh
              ? "该用户将不能继续登录。"
              : "This user will no longer be able to sign in."
            : zh
              ? "该对象将恢复访问能力。"
              : "Access for this resource will be restored."
        }
        onClose={() => setOpen(false)}
        onConfirm={(reason) => void update(reason)}
      />
    </>
  );
}

export function OAuthClientsPage() {
  const zh = locale() === "zh-CN",
    [secret, setSecret] = useState("");
  return (
    <>
      <ResourcePage<OAuthClient>
        title="OIDC Clients"
        description={
          zh
            ? "仅支持 Authorization Code + PKCE、Refresh Token 和 Client Credentials。"
            : "Only Authorization Code + PKCE, Refresh Token, and Client Credentials are supported."
        }
        path="/oauth-clients"
        columns={[
          ["Name", "name"],
          ["Client ID", "id"],
          ["Grants", (item) => item.grantTypes.join(", ")],
          ["Type", (item) => (item.public ? "public" : "confidential")],
          ["Status", (item) => (item.enabled ? "enabled" : "disabled")],
        ]}
        create={(reload) => (
          <OAuthClientCreateButton reload={reload} onSecret={setSecret} />
        )}
      />
      {secret && (
        <Notice>
          {zh ? "Client Secret 只显示一次：" : "Client secret is shown once: "}
          <code>{secret}</code>
        </Notice>
      )}
    </>
  );
}

function OAuthClientCreateButton({
  reload,
  onSecret,
}: {
  reload: () => Promise<void>;
  onSecret: (value: string) => void;
}) {
  const zh = locale() === "zh-CN",
    [open, setOpen] = useState(false),
    [busy, setBusy] = useState(false),
    [error, setError] = useState("");
  const save = async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    setBusy(true);
    setError("");
    const data = new FormData(event.currentTarget),
      grantTypes = data.getAll("grantTypes").map(String),
      scopes = String(data.get("scopes") || "")
        .split(/\s+/)
        .filter(Boolean),
      redirectUris = String(data.get("redirectUris") || "")
        .split(/\r?\n/)
        .map((value) => value.trim())
        .filter(Boolean);
    try {
      const result = await mutation<OAuthClient & { clientSecret?: string }>(
        "/oauth-clients",
        "POST",
        {
          id: data.get("id"),
          name: data.get("name"),
          public: data.get("public") === "on",
          trusted: data.get("trusted") === "on",
          enabled: true,
          grantTypes,
          scopes,
          redirectUris,
          reason: data.get("reason"),
        },
        { idempotent: true },
      );
      onSecret(result.clientSecret || "");
      await reload();
      setOpen(false);
    } catch (cause) {
      setError((cause as Error).message);
    } finally {
      setBusy(false);
    }
  };
  return (
    <>
      <Button kind="primary" onClick={() => setOpen(true)}>
        <Plus size={14} />
        {copy[locale()].create}
      </Button>
      <Dialog open={open} onOpenChange={setOpen}>
        <DialogContent>
          <form onSubmit={save}>
            <DialogHeader>
              <DialogTitle>
                {zh ? "新建 OAuth Client" : "Create OAuth client"}
              </DialogTitle>
            </DialogHeader>
            {error && <Notice>{error}</Notice>}
            <div className="form-grid">
              <label>
                Client ID
                <Input name="id" required />
              </label>
              <label>
                {zh ? "名称" : "Name"}
                <Input name="name" required />
              </label>
              <label className="full">
                Redirect URI ({zh ? "每行一个" : "one per line"})
                <textarea name="redirectUris" rows={3} />
              </label>
              <fieldset className="full">
                <legend>Grant Types</legend>
                <label>
                  <input
                    type="checkbox"
                    name="grantTypes"
                    value="authorization_code"
                  />{" "}
                  Authorization Code + PKCE S256
                </label>
                <label>
                  <input
                    type="checkbox"
                    name="grantTypes"
                    value="refresh_token"
                  />{" "}
                  Refresh Token
                </label>
                <label>
                  <input
                    type="checkbox"
                    name="grantTypes"
                    value="client_credentials"
                  />{" "}
                  Client Credentials
                </label>
              </fieldset>
              <label className="full">
                Scopes
                <Input
                  name="scopes"
                  placeholder="openid profile email offline_access kubeloop.api"
                  required
                />
              </label>
              <label>
                <input type="checkbox" name="public" /> Public client
              </label>
              <label>
                <input type="checkbox" name="trusted" />{" "}
                {zh ? "可信（跳过 Consent）" : "Trusted (skip consent)"}
              </label>
              <label className="full">
                {copy[locale()].reason}
                <Input name="reason" minLength={8} required />
              </label>
            </div>
            <DialogFooter>
              <Button type="button" onClick={() => setOpen(false)}>
                {copy[locale()].cancel}
              </Button>
              <Button type="submit" kind="primary" busy={busy}>
                {copy[locale()].save}
              </Button>
            </DialogFooter>
          </form>
        </DialogContent>
      </Dialog>
    </>
  );
}
