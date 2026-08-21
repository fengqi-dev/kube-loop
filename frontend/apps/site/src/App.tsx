import { useEffect, useState } from "react";
import { ArrowRight, Blocks, Bot, Check, ChevronRight, Copy, Download, ExternalLink, Github, Globe2, Menu, Network, PackageOpen, Route, Server, ShieldCheck, Terminal, X, Zap } from "lucide-react";
import { Badge } from "@/components/ui/badge";
import { Button, buttonVariants } from "@/components/ui/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { cn } from "@/lib/utils";

type Locale = "en" | "zh";
type Page = "overview" | "start" | "workflows" | "architecture" | "mcp";
const repo = "https://github.com/fengqi-dev/kube-loop";
const releases = `${repo}/releases/latest`;

const copy = {
  en: {
    nav: ["Overview", "Get started", "Workflows", "Architecture", "MCP"],
    eyebrow: "Kubernetes networking for local development",
    title: "Bring your laptop into the cluster loop.",
    intro: "Reach Pod IPs, ClusterIP Services, and cluster DNS from the tools you already use—without public gateways or a forest of port forwards.",
    download: "Download KubeLoop", source: "View source", trusted: "Focused tunnel. Familiar workflow. Minimal cluster footprint.",
    capabilities: "One connection, every development path", capabilitiesLead: "KubeLoop combines transparent networking with focused traffic tools, so you can move between browsing, debugging, and local iteration without changing context.",
    quick: "Connect in four steps", quickLead: "Add an HTTP or HTTPS KubeLoop Server, sign in, and connect with TUN by default—no local kubeconfig is shared with the desktop app.",
    workflows: "Choose the traffic path you need", workflowsLead: "Keep the Service identity stable while deciding where requests should go.",
    architecture: "A narrow, auditable data path", architectureLead: "The Control Plane authenticates every user. RelayTicket-authenticated WebSockets carry traffic to an assigned, credential-free Data Plane.",
    mcp: "A local MCP boundary for agents", mcpLead: "Connect Codex, Claude Code, Cursor, or VS Code to the active authenticated Server Profile and Cluster Session without exposing Kubernetes credentials.",
  },
  zh: {
    nav: ["概览", "快速开始", "流量工作流", "架构", "MCP"],
    eyebrow: "面向本地开发的 Kubernetes 网络工具",
    title: "让本地开发机进入集群网络。",
    intro: "从日常使用的浏览器、IDE、CLI 和 SDK 直接访问 Pod IP、ClusterIP Service 与集群 DNS，无需公网集群入口，也无需维护大量端口转发。",
    download: "下载 KubeLoop", source: "查看源码", trusted: "聚焦集群流量，保持熟悉工作流，最小化集群内组件。",
    capabilities: "一次连接，覆盖完整开发路径", capabilitiesLead: "KubeLoop 把透明网络与流量工具放在同一个桌面应用中，在访问、调试和本地迭代之间切换时无需改变工作方式。",
    quick: "四步连接集群", quickLead: "添加 HTTP 或 HTTPS KubeLoop Server、登录并以默认 TUN 模式连接；桌面端不读取或共享本机 kubeconfig。",
    workflows: "为任务选择合适的流量路径", workflowsLead: "保留 Service 身份，同时精确决定请求应该去往哪里。",
    architecture: "收敛且可审计的数据路径", architectureLead: "Control Plane 验证每个登录用户；流量通过 RelayTicket 认证的 WebSocket 到达分配的无凭据 Data Plane。",
    mcp: "面向 Agent 的本地 MCP 边界", mcpLead: "让 Codex、Claude Code、Cursor 或 VS Code 使用当前已认证的 Server Profile 与 Cluster Session，而无需暴露 Kubernetes 凭据。",
  },
};

const pages: Page[] = ["overview", "start", "workflows", "architecture", "mcp"];
function pageFromHash(): Page { const value = location.hash.replace("#/", ""); return pages.includes(value as Page) ? value as Page : "overview"; }
function go(page: Page) { location.hash = `/${page}`; }

export default function App() {
  const [locale, setLocale] = useState<Locale>(() => localStorage.getItem("kubeloop.site.locale") === "zh" ? "zh" : "en");
  const [page, setPage] = useState<Page>(pageFromHash);
  const [menu, setMenu] = useState(false);
  const t = copy[locale];
  useEffect(() => { const sync = () => { setPage(pageFromHash()); setMenu(false); scrollTo({ top: 0, behavior: "smooth" }); }; addEventListener("hashchange", sync); return () => removeEventListener("hashchange", sync); }, []);
  useEffect(() => { document.documentElement.lang = locale === "zh" ? "zh-CN" : "en"; localStorage.setItem("kubeloop.site.locale", locale); }, [locale]);
  return <div className="min-h-screen bg-background text-foreground">
    <Header page={page} locale={locale} labels={t.nav} menu={menu} setMenu={setMenu} setLocale={setLocale} />
    <main>{page === "overview" ? <Overview t={t} locale={locale} /> : page === "start" ? <GetStarted t={t} locale={locale} /> : page === "workflows" ? <Workflows t={t} locale={locale} /> : page === "architecture" ? <Architecture t={t} locale={locale} /> : <Mcp t={t} locale={locale} />}</main>
    <Footer locale={locale} />
  </div>;
}

function Header({ page, locale, labels, menu, setMenu, setLocale }: { page: Page; locale: Locale; labels: string[]; menu: boolean; setMenu(v: boolean): void; setLocale(v: Locale): void }) {
  return <header className="sticky top-0 z-50 border-b border-border/70 bg-background/85 backdrop-blur-xl"><div className="mx-auto flex h-16 max-w-7xl items-center px-4 sm:px-6">
    <button className="flex items-center gap-3" onClick={() => go("overview")} aria-label="KubeLoop home"><Logo /><span className="text-sm font-bold tracking-tight">KubeLoop</span><Badge className="hidden sm:inline-flex">Open source</Badge></button>
    <nav className="ml-auto hidden items-center gap-1 lg:flex" aria-label="Primary navigation">{pages.map((item, index) => <button key={item} onClick={() => go(item)} className={cn("rounded-md px-3 py-2 text-sm text-muted-foreground transition hover:bg-accent hover:text-foreground", page === item && "bg-accent text-foreground")}>{labels[index]}</button>)}</nav>
    <div className="ml-auto flex items-center gap-1 lg:ml-4"><Button variant="ghost" size="sm" onClick={() => setLocale(locale === "en" ? "zh" : "en")} aria-label="Change language"><Globe2 className="size-4" />{locale === "en" ? "中文" : "EN"}</Button><a className={cn(buttonVariants({ variant: "ghost", size: "sm" }), "hidden sm:inline-flex")} href={repo} target="_blank" rel="noreferrer"><Github className="size-4" />GitHub</a><Button className="lg:hidden" variant="ghost" size="sm" onClick={() => setMenu(!menu)} aria-label="Toggle menu">{menu ? <X /> : <Menu />}</Button></div>
  </div>{menu && <nav className="border-t border-border p-3 lg:hidden">{pages.map((item, index) => <button key={item} onClick={() => go(item)} className={cn("block w-full rounded-md px-3 py-3 text-left text-sm text-muted-foreground", page === item && "bg-accent text-foreground")}>{labels[index]}</button>)}</nav>}</header>;
}

function Logo() { return <span className="grid size-9 place-items-center rounded-xl bg-primary text-xs font-black text-primary-foreground shadow-sm">KL</span>; }
function PageIntro({ eyebrow, title, lead }: { eyebrow: string; title: string; lead: string }) { return <section className="page-intro"><Badge>{eyebrow}</Badge><h1>{title}</h1><p>{lead}</p></section>; }

function Overview({ t, locale }: { t: typeof copy.en; locale: Locale }) {
  const features = locale === "zh" ? [
    [Network, "透明 TUN", "自动发现 Pod 与 Service 网段，仅将 Kubernetes 流量导入托管的 sing-box。"],
    [Route, "集群 DNS", "支持 split DNS 和 search domain，短名称与 *.svc.cluster.local 均可解析。"],
    [Zap, "本地流量工作流", "Port Forward、Exchange、Mirror 与 Preview 覆盖双向开发场景。"],
    [ShieldCheck, "简单登录", "IAM 仅保留本地用户与 OIDC；用户登录后即可访问，无需邀请或权限分配。"],
    [Terminal, "Pod SSH 与文件传输", "无需容器运行 sshd，通过 pods/exec 提供 Shell、SFTP 与 SCP。"],
    [Bot, "Agent 工具", "通过本地 MCP 端点安全操作当前活动的 Server Profile 与 Cluster Session。"],
  ] : [
    [Network, "Transparent TUN", "Discover Pod and Service networks automatically; route only Kubernetes traffic through managed sing-box."],
    [Route, "Cluster DNS", "Split DNS and search domains make short names and *.svc.cluster.local work from local apps."],
    [Zap, "Local traffic workflows", "Port Forward, Exchange, Mirror, and Preview cover outbound and inbound development paths."],
    [ShieldCheck, "Simple sign-in", "IAM keeps local users and OIDC only; every signed-in user can access without invitations or permission assignments."],
    [Terminal, "Pod SSH and files", "Use Shell, SFTP, and SCP over pods/exec without running sshd in the container."],
    [Bot, "Agent tools", "Operate the active Server Profile and Cluster Session through a local MCP endpoint."],
  ];
  return <><section className="hero"><div className="hero-grid"><div><Badge className="mb-6 border-primary/20 bg-primary/10 text-primary">{t.eyebrow}</Badge><h1>{t.title}</h1><p>{t.intro}</p><div className="mt-8 flex flex-wrap gap-3"><a className={buttonVariants({ size: "lg" })} href={releases}>{t.download}<Download className="size-4" /></a><a className={buttonVariants({ variant: "outline", size: "lg" })} href={repo}>{t.source}<Github className="size-4" /></a></div><div className="mt-8 flex items-center gap-3 text-sm text-muted-foreground"><span className="flex -space-x-1">{["mac", "win", "linux"].map(x => <i key={x} className="grid size-7 place-items-center rounded-full border-2 border-background bg-secondary not-italic text-[9px] font-bold uppercase">{x[0]}</i>)}</span>{t.trusted}</div></div><TrafficPanel locale={locale} /></div></section>
    <section className="section"><SectionHead title={t.capabilities} lead={t.capabilitiesLead} /><div className="grid gap-4 md:grid-cols-2 lg:grid-cols-3">{features.map(([Icon, title, body]) => <Card key={String(title)} className="feature-card"><CardHeader><span className="icon-box"><Icon className="size-5" /></span><CardTitle>{String(title)}</CardTitle><CardDescription>{String(body)}</CardDescription></CardHeader></Card>)}</div></section>
    <Cta locale={locale} />
  </>;
}

function TrafficPanel({ locale }: { locale: Locale }) { return <div className="traffic-panel"><div className="flex items-center justify-between border-b border-white/10 px-5 py-4"><span className="text-xs font-semibold text-white/70">{locale === "zh" ? "活动连接" : "ACTIVE CONNECTION"}</span><span className="flex items-center gap-2 text-xs text-emerald-300"><i className="size-2 rounded-full bg-emerald-400" />Connected</span></div><div className="space-y-5 p-5"><Path from="Local app" to="KubeLoop" detail="TUN (default) / SOCKS + split DNS" /><Path from="KubeLoop" to="Relay" detail="RelayTicket · WS / WSS" /><Path from="Data Plane" to="Service" detail="10.96.0.42:443" /><div className="grid grid-cols-3 gap-2 pt-2">{[["12ms", "RTT"], ["24", "Routes"], ["0", "Public ports"]].map(([v,l]) => <div className="rounded-lg border border-white/10 bg-white/5 p-3 text-center" key={l}><strong className="block text-lg text-white">{v}</strong><span className="text-[10px] text-white/50">{l}</span></div>)}</div></div></div>; }
function Path({ from, to, detail }: { from: string; to: string; detail: string }) { return <div><div className="mb-2 flex items-center gap-2 text-sm text-white"><span>{from}</span><span className="h-px flex-1 bg-gradient-to-r from-emerald-400/80 to-emerald-400/10" /><ChevronRight className="size-4 text-emerald-300" /><span>{to}</span></div><p className="font-mono text-[10px] text-white/45">{detail}</p></div>; }
function SectionHead({ title, lead }: { title: string; lead: string }) { return <div className="mb-10 max-w-3xl"><h2 className="text-3xl font-bold tracking-tight md:text-4xl">{title}</h2><p className="mt-4 text-base leading-7 text-muted-foreground">{lead}</p></div>; }

function GetStarted({ t, locale }: { t: typeof copy.en; locale: Locale }) { const steps = locale === "zh" ? [["01", "安装桌面端", "下载适用于 macOS、Windows 或 Linux 的发行包。"], ["02", "添加 Server", "填写管理员提供的 KubeLoop Server HTTP 或 HTTPS 地址并完成能力发现。"], ["03", "登录", "在系统浏览器完成登录。无需邀请或权限分配；如有需要，再选择 Namespace 上下文。"], ["04", "以 TUN 模式连接", "TUN 是默认模式，首次使用时需批准本地 Helper；不希望使用特权网络时可切换到 SOCKS。"]] : [["01", "Install the desktop app", "Download the package for macOS, Windows, or Linux."], ["02", "Add a Server", "Enter the HTTP or HTTPS KubeLoop Server URL supplied by your administrator and run discovery."], ["03", "Sign in", "Complete sign-in in the system browser. No invitation or permission assignment is required; choose a namespace context only when needed."], ["04", "Connect with TUN", "TUN is the default; approve the local Helper on first use. Switch to SOCKS when privileged networking is not desired."]]; return <div className="section"><PageIntro eyebrow={locale === "zh" ? "开始" : "START"} title={t.quick} lead={t.quickLead} /><ServerInstallBlock locale={locale} /><div className="mt-6"><InstallBlock locale={locale} /></div><div className="mt-10 grid gap-4 md:grid-cols-2">{steps.map(([n,title,body]) => <Card key={n}><CardHeader><Badge className="w-fit">{n}</Badge><CardTitle className="mt-3">{title}</CardTitle><CardDescription>{body}</CardDescription></CardHeader></Card>)}</div><Notice locale={locale} /></div>; }
function ServerInstallBlock({ locale }: { locale: Locale }) {
  const [copied, setCopied] = useState(false);
  const command = `helm upgrade --install kubeloop \\
  oci://ghcr.io/fengqi-dev/kube-loop/charts/kubeloop \\
  --version 2.1.1 \\
  --namespace kubeloop-system \\
  --create-namespace \\
  --set publicURL=http://kubeloop.example.com \\
  --set ingress.enabled=true \\
  --set ingress.host=kubeloop.example.com \\
  --set ingress.className=nginx \\
  --wait`;
  return <Card className="mb-6 overflow-hidden"><div className="flex items-center justify-between border-b border-border bg-muted/40 px-4 py-3"><span className="flex items-center gap-2 text-xs font-semibold"><Server className="size-4" />KubeLoop Server · Helm</span><Button variant="ghost" size="sm" onClick={async () => { await navigator.clipboard.writeText(command); setCopied(true); setTimeout(() => setCopied(false), 1500); }}>{copied ? <Check className="size-4" /> : <Copy className="size-4" />}{copied ? (locale === "zh" ? "已复制" : "Copied") : (locale === "zh" ? "复制" : "Copy")}</Button></div><pre className="overflow-x-auto p-5 text-sm"><code>{command}</code></pre><div className="border-t border-border px-4 py-3 text-xs leading-5 text-muted-foreground">{locale === "zh" ? "Helm 会自动生成并保留内部 Relay 签名密钥和 TLS Secret。Ingress 默认使用 HTTP，无需公网证书；启用 HTTPS 时将 publicURL 改为 https://，并设置 ingress.tls.enabled=true 与 ingress.tls.secretName。" : "Helm automatically generates and retains the internal Relay signing and TLS Secret. Ingress uses HTTP by default and needs no public certificate; for HTTPS, change publicURL to https:// and set ingress.tls.enabled=true plus ingress.tls.secretName."}</div></Card>;
}
function InstallBlock({ locale }: { locale: Locale }) { const [copied, setCopied] = useState(false); const command = "curl -fsSL https://raw.githubusercontent.com/fengqi-dev/kube-loop/main/scripts/install.sh | bash"; return <Card className="overflow-hidden"><div className="flex items-center justify-between border-b border-border bg-muted/40 px-4 py-3"><span className="flex items-center gap-2 text-xs font-semibold"><Terminal className="size-4" />KubeLoop Desktop · macOS / Linux</span><Button variant="ghost" size="sm" onClick={async () => { await navigator.clipboard.writeText(command); setCopied(true); setTimeout(() => setCopied(false), 1500); }}>{copied ? <Check className="size-4" /> : <Copy className="size-4" />}{copied ? (locale === "zh" ? "已复制" : "Copied") : (locale === "zh" ? "复制" : "Copy")}</Button></div><pre className="overflow-x-auto p-5 text-sm"><code>{command}</code></pre><div className="flex flex-wrap gap-2 border-t border-border px-4 py-3"><a className={buttonVariants({ variant: "outline", size: "sm" })} href={releases}><PackageOpen className="size-4" />Windows / manual download</a></div></Card>; }
function Notice({ locale }: { locale: Locale }) { return <div className="mt-6 rounded-xl border border-primary/20 bg-primary/5 p-5 text-sm leading-6"><strong>{locale === "zh" ? "连接后：" : "After connecting: "}</strong>{locale === "zh" ? "普通本地应用可直接使用 ClusterIP、Pod IP 与集群域名，无需配置应用代理。" : "ordinary local applications can use ClusterIP, Pod IP, and cluster DNS directly—no per-application proxy settings."}</div>; }

function Workflows({ t, locale }: { t: typeof copy.en; locale: Locale }) { const items = locale === "zh" ? [["Port Forward", "出站", "将本地端口连接到 Pod 或 Service。", "localhost → cluster"], ["Exchange", "入站替换", "保留 Service 的 ClusterIP 与 DNS，将请求交给本地进程。", "Service → local app"], ["Mirror", "入站复制", "原始 Pod 继续响应；请求副本发送到本地观察程序。", "Service → Pods + shadow"], ["Preview", "新建入口", "创建临时 ClusterIP Service，将集群请求送到本地应用。", "new Service → local app"]] : [["Port Forward", "Outbound", "Forward a local port to a Pod or Service.", "localhost → cluster"], ["Exchange", "Inbound replace", "Keep Service ClusterIP and DNS while a local process handles requests.", "Service → local app"], ["Mirror", "Inbound tee", "Original Pods stay primary; a request copy goes to a local observer.", "Service → Pods + shadow"], ["Preview", "New inbound", "Create a temporary ClusterIP Service backed by a local application.", "new Service → local app"]]; return <div className="section"><PageIntro eyebrow={locale === "zh" ? "开发工具" : "DEVELOPER TOOLS"} title={t.workflows} lead={t.workflowsLead} /><div className="grid gap-4 md:grid-cols-2">{items.map(([title,kind,body,path],i) => <Card key={title} className="overflow-hidden"><CardHeader><div className="mb-4 flex items-center justify-between"><span className="icon-box">{i === 0 ? <ArrowRight /> : i === 1 ? <Zap /> : i === 2 ? <Blocks /> : <PackageOpen />}</span><Badge>{kind}</Badge></div><CardTitle className="text-xl">{title}</CardTitle><CardDescription>{body}</CardDescription></CardHeader><CardContent><div className="rounded-lg border border-border bg-muted/50 px-4 py-3 font-mono text-xs text-muted-foreground">{path}</div></CardContent></Card>)}</div></div>; }

function Architecture({ t, locale }: { t: typeof copy.en; locale: Locale }) { const nodes = [["Local applications", "Browser · IDE · CLI · SDK"], ["TUN by default", "SOCKS optional · managed sing-box · split DNS"], ["Relay", "RelayTicket-authenticated WS / WSS"], ["Assigned Data Plane", "session-scoped · no Kubernetes credentials"], ["Pods · Services · CoreDNS", "cluster destinations"]]; return <div className="section"><PageIntro eyebrow={locale === "zh" ? "数据路径" : "DATA PATH"} title={t.architecture} lead={t.architectureLead} /><div className="mx-auto max-w-3xl">{nodes.map(([title,detail],i) => <div key={title} className="relative flex gap-4 pb-8 last:pb-0"><div className="flex flex-col items-center"><span className="grid size-10 place-items-center rounded-full border border-primary/25 bg-primary/10 text-sm font-bold text-primary">{i+1}</span>{i < nodes.length - 1 && <span className="w-px flex-1 bg-border" />}</div><Card className="mb-1 flex-1"><CardHeader className="p-4"><CardTitle className="text-sm">{title}</CardTitle><CardDescription className="font-mono text-xs">{detail}</CardDescription></CardHeader></Card></div>)}</div><div className="mt-10 grid gap-4 md:grid-cols-3">{[[ShieldCheck, locale === "zh" ? "Control Plane 统一身份验证" : "Control Plane authentication"], [Server, locale === "zh" ? "Data Plane 无集群凭据" : "Credential-free Data Plane"], [Route, locale === "zh" ? "仅路由集群流量" : "Cluster routes only"]].map(([Icon,label]) => <div className="flex items-center gap-3 rounded-xl border border-border p-4 text-sm font-semibold" key={String(label)}><Icon className="size-5 text-primary" />{String(label)}</div>)}</div></div>; }

function Mcp({ t, locale }: { t: typeof copy.en; locale: Locale }) { const tools = [["manage_cluster", "version · capabilities · namespaces · workloads"], ["manage_connection", "status · connect · disconnect"], ["manage_traffic", "forward · exchange · mirror · preview"], ["exec_pod_command", "exact argv · base64 output"], ["manage_file_transfer", "upload · download · cancel"], ["manage_pod_files", "list · create · rename · delete"]]; return <div className="section"><PageIntro eyebrow="MODEL CONTEXT PROTOCOL" title={t.mcp} lead={t.mcpLead} /><div className="grid gap-6 lg:grid-cols-[1fr_.8fr]"><Card><CardHeader><CardTitle>{locale === "zh" ? "可用工具" : "Available tools"}</CardTitle><CardDescription>{locale === "zh" ? "所有集群操作都经过已认证的 Control Plane；当前 IAM 不再增加权限层。" : "Every cluster operation passes through the authenticated Control Plane; current IAM adds no permission layer."}</CardDescription></CardHeader><CardContent className="space-y-2">{tools.map(([name,desc]) => <div key={name} className="flex flex-col justify-between gap-1 rounded-lg border border-border bg-muted/30 p-3 sm:flex-row sm:items-center"><code className="text-xs font-semibold text-primary">{name}</code><span className="text-xs text-muted-foreground">{desc}</span></div>)}</CardContent></Card><Card className="border-primary/20 bg-primary/[.04]"><CardHeader><span className="icon-box"><Bot /></span><CardTitle className="mt-4">{locale === "zh" ? "安全默认值" : "Secure defaults"}</CardTitle></CardHeader><CardContent className="space-y-4 text-sm leading-6 text-muted-foreground">{(locale === "zh" ? ["仅监听 127.0.0.1", "默认生成 Bearer token", "token 存入系统凭证保险库", "仅可使用当前活动 Server Profile", "变更操作必须提供精确 Session 上下文"] : ["Binds only to 127.0.0.1", "Generated Bearer token by default", "Token stays in the OS credential vault", "Uses only the active Server Profile", "Mutations require exact Session context"]).map(x => <p className="flex gap-3" key={x}><Check className="mt-1 size-4 shrink-0 text-primary" />{x}</p>)}</CardContent></Card></div></div>; }

function Cta({ locale }: { locale: Locale }) { return <section className="section pt-0"><div className="cta"><div><Badge className="mb-4 border-white/15 bg-white/10 text-white">MIT · Open source</Badge><h2>{locale === "zh" ? "准备好连接集群了吗？" : "Ready to close the loop?"}</h2><p>{locale === "zh" ? "下载桌面应用，添加 KubeLoop Server，登录后即可使用真实的集群地址。" : "Download the desktop app, add your KubeLoop Server, and use real cluster addresses after signing in."}</p></div><a className={cn(buttonVariants({ size: "lg" }), "bg-white text-emerald-950 hover:bg-white/90")} href={releases}>{locale === "zh" ? "下载最新版本" : "Download latest"}<ArrowRight /></a></div></section>; }
function Footer({ locale }: { locale: Locale }) { return <footer className="border-t border-border"><div className="mx-auto flex max-w-7xl flex-col gap-4 px-6 py-8 text-xs text-muted-foreground sm:flex-row sm:items-center"><div className="flex items-center gap-3"><Logo /><span>© KubeLoop contributors · MIT License</span></div><div className="flex gap-4 sm:ml-auto"><a href={`${repo}/blob/main/docs/design${locale === "zh" ? ".zh-CN" : ""}.md`}>Design notes</a><a href={repo}>GitHub <ExternalLink className="inline size-3" /></a></div></div></footer>; }
