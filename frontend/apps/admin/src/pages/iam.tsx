import { FormEvent, useCallback, useEffect, useState } from "react";
import { Plus, RefreshCw } from "lucide-react";
import { Button, Empty, Loading, Notice, PageHeader } from "../components";
import { mutation, request } from "../api";
import type {
  Group,
  Identity,
  Invitation,
  ListResponse,
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
}: {
  title: string;
  description: string;
  path: string | null;
  columns: Array<[string, keyof T | ((item: T) => string)]>;
  create?: (reload: () => Promise<void>) => React.ReactNode;
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
              </tr>
            </thead>
            <tbody>
              {state.items.map((item, index) => (
                <tr key={(item as { id?: string }).id || index}>
                  {columns.map(([label, value]) => (
                    <td key={label}>
                      {typeof value === "function"
                        ? value(item)
                        : String(item[value] ?? "—")}
                    </td>
                  ))}
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
    </>
  );
}

function CreateButton({
  title,
  fields,
  submit,
  reload,
}: {
  title: string;
  fields: Array<{
    name: string;
    label: string;
    type?: string;
    required?: boolean;
    placeholder?: string;
  }>;
  submit: (values: Record<string, string>) => Promise<void>;
  reload: () => Promise<void>;
}) {
  const [open, setOpen] = useState(false),
    [values, setValues] = useState<Record<string, string>>({}),
    [busy, setBusy] = useState(false),
    [error, setError] = useState(""),
    text = copy[locale()];
  const save = async (event: FormEvent) => {
    event.preventDefault();
    setBusy(true);
    setError("");
    try {
      await submit(values);
      await reload();
      setOpen(false);
      setValues({});
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
        {text.create}
      </Button>
      <Dialog open={open} onOpenChange={setOpen}>
        <DialogContent>
          <form onSubmit={save}>
            <DialogHeader>
              <DialogTitle>{title}</DialogTitle>
            </DialogHeader>
            {error && <Notice>{error}</Notice>}
            <div className="form-grid">
              {fields.map((field) => (
                <label className="full" key={field.name}>
                  {field.label}
                  <Input
                    type={field.type}
                    required={field.required}
                    placeholder={field.placeholder}
                    value={values[field.name] || ""}
                    onChange={(event) =>
                      setValues({ ...values, [field.name]: event.target.value })
                    }
                  />
                </label>
              ))}
            </div>
            <DialogFooter>
              <Button type="button" onClick={() => setOpen(false)}>
                {text.cancel}
              </Button>
              <Button type="submit" kind="primary" busy={busy}>
                {text.save}
              </Button>
            </DialogFooter>
          </form>
        </DialogContent>
      </Dialog>
    </>
  );
}

export function GroupsPage({ organizationId }: { organizationId: string }) {
  const zh = locale() === "zh-CN",
    path = organizationId ? `/organizations/${organizationId}/groups` : null,
    [accessVersion, setAccessVersion] = useState(0);
  return (
    <>
      <ResourcePage<Group>
        title={zh ? "用户组" : "Groups"}
        description={
          zh
            ? "用户从组继承 Namespace 访问权；系统管理员组可访问全部 Namespace。"
            : "Users inherit namespace access from groups; the system administrator group can access all namespaces."
        }
        path={path}
        columns={[
          ["Name", "name"],
          ["Description", "description"],
          ["Updated", "updatedAt"],
        ]}
        create={
          organizationId
            ? (reload) => (
                <CreateButton
                  title={zh ? "新建用户组" : "Create group"}
                  reload={async () => {
                    await reload();
                    setAccessVersion((version) => version + 1);
                  }}
                  fields={[
                    {
                      name: "name",
                      label: zh ? "名称" : "Name",
                      required: true,
                    },
                    { name: "description", label: zh ? "说明" : "Description" },
                    {
                      name: "reason",
                      label: copy[locale()].reason,
                      required: true,
                    },
                  ]}
                  submit={(values) =>
                    mutation(path!, "POST", values, { idempotent: true })
                  }
                />
              )
            : undefined
        }
      />
      {organizationId && (
        <GroupAccessPanel
          key={accessVersion}
          organizationId={organizationId}
        />
      )}
    </>
  );
}

function GroupAccessPanel({ organizationId }: { organizationId: string }) {
  const zh = locale() === "zh-CN",
    groups = useList<Group>(`/organizations/${organizationId}/groups`),
    identities = useList<Identity>("/identities?limit=100"),
    [groupId, setGroupId] = useState(""),
    [error, setError] = useState("");
  const members = useList<{ groupId: string; identityId: string }>(
    groupId
      ? `/organizations/${organizationId}/groups/${groupId}/memberships`
      : null,
  );
  const namespaces = useList<{ groupId: string; namespace: string }>(
    groupId
      ? `/organizations/${organizationId}/groups/${groupId}/namespaces`
      : null,
  );
  const selected = groups.items.find((group) => group.id === groupId);
  const addMember = async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    const form = event.currentTarget,
      data = new FormData(form);
    setError("");
    try {
      await mutation(
        `/organizations/${organizationId}/groups/${groupId}/memberships`,
        "POST",
        { identityId: data.get("identityId"), reason: data.get("reason") },
        { idempotent: true },
      );
      await members.reload();
      form.reset();
    } catch (cause) {
      setError((cause as Error).message);
    }
  };
  const addNamespace = async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    const form = event.currentTarget,
      data = new FormData(form);
    setError("");
    try {
      await mutation(
        `/organizations/${organizationId}/groups/${groupId}/namespaces`,
        "POST",
        { namespace: data.get("namespace"), reason: data.get("reason") },
        { idempotent: true },
      );
      await namespaces.reload();
      form.reset();
    } catch (cause) {
      setError((cause as Error).message);
    }
  };
  return (
    <section className="table-panel compact-panel">
      <h2>
        {zh ? "组成员与 Namespace 权限" : "Group members and namespace access"}
      </h2>
      {error && <Notice>{error}</Notice>}
      <label>
        {zh ? "选择用户组" : "Select group"}
        <select
          value={groupId}
          onChange={(event) => setGroupId(event.target.value)}
        >
          <option value="" />
          {groups.items.map((group) => (
            <option key={group.id} value={group.id}>
              {group.name}
              {group.system
                ? ` · ${zh ? "系统管理员" : "system administrators"}`
                : ""}
            </option>
          ))}
        </select>
      </label>
      {groupId && (
        <div className="form-grid">
          <div>
            <h3>{zh ? "成员" : "Members"}</h3>
            <ul>
              {members.items.map((member) => (
                <li key={member.identityId}>
                  {identities.items.find(
                    (identity) => identity.id === member.identityId,
                  )?.displayName || member.identityId}
                </li>
              ))}
            </ul>
            <form className="inline-form" onSubmit={addMember}>
              <label>
                {zh ? "用户" : "User"}
                <select name="identityId" required>
                  <option value="" />
                  {identities.items
                    .filter(
                      (identity) =>
                        identity.type === "human" &&
                        !members.items.some(
                          (member) => member.identityId === identity.id,
                        ),
                    )
                    .map((identity) => (
                      <option key={identity.id} value={identity.id}>
                        {identity.displayName}
                      </option>
                    ))}
                </select>
              </label>
              <label>
                {copy[locale()].reason}
                <Input name="reason" minLength={8} required />
              </label>
              <Button type="submit" kind="primary">
                {zh ? "添加成员" : "Add member"}
              </Button>
            </form>
          </div>
          <div>
            <h3>Namespace</h3>
            {selected?.system ? (
              <Notice>
                {zh
                  ? "系统管理员组自动拥有全部 Namespace 权限。"
                  : "The system administrator group automatically has access to every namespace."}
              </Notice>
            ) : (
              <>
                <ul>
                  {namespaces.items.map((item) => (
                    <li key={item.namespace}>
                      <code>{item.namespace}</code>
                    </li>
                  ))}
                </ul>
                <form className="inline-form" onSubmit={addNamespace}>
                  <label>
                    Namespace
                    <Input name="namespace" required />
                  </label>
                  <label>
                    {copy[locale()].reason}
                    <Input name="reason" minLength={8} required />
                  </label>
                  <Button type="submit" kind="primary">
                    {zh ? "添加 Namespace" : "Add namespace"}
                  </Button>
                </form>
              </>
            )}
          </div>
        </div>
      )}
    </section>
  );
}

export function UsersPage({ organizationId }: { organizationId: string }) {
  const zh = locale() === "zh-CN";
  return (
    <ResourcePage<Identity>
      title={zh ? "用户" : "Users"}
      description={
        zh
          ? "创建用户时必须选择所属用户组。"
          : "Every user must be assigned to a group when created."
      }
      path="/identities?limit=100"
      columns={[
        ["Name", "displayName"],
        ["Type", "type"],
        ["Email", (item) => item.primaryEmail || "—"],
        ["Status", "status"],
      ]}
      create={
        organizationId
          ? (reload) => (
              <UserCreateButton
                organizationId={organizationId}
                reload={reload}
              />
            )
          : undefined
      }
    />
  );
}

function UserCreateButton({
  organizationId,
  reload,
}: {
  organizationId: string;
  reload: () => Promise<void>;
}) {
  const zh = locale() === "zh-CN",
    [open, setOpen] = useState(false),
    [busy, setBusy] = useState(false),
    [error, setError] = useState(""),
    groups = useList<Group>(
      open ? `/organizations/${organizationId}/groups` : null,
    );
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
          groupId: data.get("groupId"),
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
              <label className="full">
                {zh ? "所属用户组" : "Group"}
                <select name="groupId" required>
                  <option value="" />
                  {groups.items.map((group) => (
                    <option key={group.id} value={group.id}>
                      {group.name}
                    </option>
                  ))}
                </select>
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

export function InvitationsPage({
  organizationId,
}: {
  organizationId: string;
}) {
  const zh = locale() === "zh-CN",
    path = organizationId
      ? `/organizations/${organizationId}/invitations`
      : null,
    [token, setToken] = useState("");
  return (
    <>
      <ResourcePage<Invitation>
        title={zh ? "邀请" : "Invitations"}
        description={
          zh
            ? "邀请 24 小时有效；Token 只显示一次。"
            : "Invitations expire after 24 hours; the token is shown once."
        }
        path={path}
        columns={[
          ["Email", "email"],
          ["Group", "groupId"],
          ["Status", "status"],
          ["Expires", "expiresAt"],
        ]}
        create={
          organizationId
            ? (reload) => (
                <InvitationCreateButton
                  organizationId={organizationId}
                  path={path!}
                  reload={reload}
                  onToken={setToken}
                />
              )
            : undefined
        }
      />
      {token && (
        <Notice>
          {zh
            ? "请立即安全发送此一次性 Token："
            : "Copy and deliver this one-time token now: "}
          <code>{token}</code>
        </Notice>
      )}
    </>
  );
}

function InvitationCreateButton({
  organizationId,
  path,
  reload,
  onToken,
}: {
  organizationId: string;
  path: string;
  reload: () => Promise<void>;
  onToken: (value: string) => void;
}) {
  const zh = locale() === "zh-CN",
    [open, setOpen] = useState(false),
    [busy, setBusy] = useState(false),
    [error, setError] = useState(""),
    groups = useList<Group>(
      open ? `/organizations/${organizationId}/groups` : null,
    );
  const save = async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    setBusy(true);
    setError("");
    const data = new FormData(event.currentTarget);
    try {
      const result = await mutation<{ invitationToken: string }>(
        path,
        "POST",
        {
          email: data.get("email"),
          groupId: data.get("groupId"),
          reason: data.get("reason"),
        },
        { idempotent: true },
      );
      onToken(result.invitationToken);
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
              <DialogTitle>{zh ? "创建邀请" : "Create invitation"}</DialogTitle>
            </DialogHeader>
            {error && <Notice>{error}</Notice>}
            <div className="form-grid">
              <label>
                {zh ? "邮箱" : "Email"}
                <Input name="email" type="email" required />
              </label>
              <label>
                {zh ? "所属用户组" : "Group"}
                <select name="groupId" required>
                  <option value="" />
                  {groups.items
                    .filter((group) => !group.system)
                    .map((group) => (
                      <option key={group.id} value={group.id}>
                        {group.name}
                      </option>
                    ))}
                </select>
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

export function OAuthClientsPage({
  organizationId,
}: {
  organizationId: string;
}) {
  const zh = locale() === "zh-CN",
    [secret, setSecret] = useState("");
  return (
    <>
      <ResourcePage<OAuthClient>
        title="OAuth Clients"
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
          <OAuthClientCreateButton
            organizationId={organizationId}
            reload={reload}
            onSecret={setSecret}
          />
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
  organizationId,
  reload,
  onSecret,
}: {
  organizationId: string;
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
          organizationId: organizationId || undefined,
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
