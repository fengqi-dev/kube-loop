const dictionary = {
  en: {
    "nav.docs": "Docs",
    "nav.github": "GitHub",
    "nav.releases": "Releases",
    "nav.menu": "Menu",
    "nav.overview": "Overview",
    "nav.getStarted": "Get started",
    "nav.product": "Product",
    "nav.workflows": "Workflows",
    "nav.mcp": "MCP",
    "nav.architecture": "Architecture",
    "nav.design": "Design notes",
    "sidebar.group.start": "Start",
    "sidebar.group.guides": "Guides",
    "sidebar.group.reference": "Reference",
    "cta.download": "Download releases",
    "cta.github": "View on GitHub",
    "cta.design": "Design notes",
    "cta.designHref":
      "https://github.com/fengqi-dev/kube-loop/blob/main/docs/design.md",
    "cta.getStarted": "Get started",
    "footer.copy": "© KubeLoop contributors. MIT License.",

    "overview.title": "Welcome to KubeLoop",
    "overview.desc":
      "KubeLoop is a desktop client that connects your laptop to Kubernetes like a VPN — so local apps can reach Pod IPs, ClusterIP Services, and cluster DNS without port-forwards.",
    "overview.carousel.title": "Developer tools",
    "overview.carousel.desc":
      "Port Forward, Exchange, Mirror, and Preview — animated traffic paths between your app, KubeLoop, and Kubernetes.",
    "overview.carousel.more": "See paths on Architecture",
    "overview.start.title": "Get started",
    "overview.start.quick.title": "Quickstart",
    "overview.start.quick.body": "Download a build, pick a Context, and connect.",
    "overview.start.clusters.title": "Manage clusters",
    "overview.start.clusters.body": "Add kubeconfig files, probe APIs, switch contexts.",
    "overview.start.design.title": "Design notes",
    "overview.start.design.body": "Architecture, permissions, and security boundaries.",
    "overview.guides.title": "Guides",
    "overview.guides.product.title": "Product capabilities",
    "overview.guides.product.body":
      "TUN, DNS, Host Aliases, cluster management, and MCP for AI agents.",
    "overview.guides.workflows.title": "Everyday workflows",
    "overview.guides.workflows.body":
      "Open Services, map custom domains, debug Pods, exchange, mirror, or preview locally.",
    "overview.guides.mcp.title": "MCP for AI agents",
    "overview.guides.mcp.body":
      "Install a local Streamable HTTP endpoint into Cursor, Claude Code, Codex, or VS Code.",
    "overview.guides.arch.title": "Architecture",
    "overview.guides.arch.body": "How traffic flows from your apps to the Gateway.",
    "overview.more.title": "Also useful",
    "overview.more.releases.title": "Releases",
    "overview.more.releases.body": "Desktop packages and Gateway images.",
    "overview.more.github.title": "GitHub",
    "overview.more.github.body": "Source, issues, and contribution entry points.",
    "overview.callout.title": "Download KubeLoop",
    "overview.callout.body":
      "Grab a desktop build from GitHub Releases, then connect with your kubeconfig.",

    "started.title": "Get started",
    "started.desc": "Connect your machine to a cluster in a few minutes.",
    "started.steps.title": "Connect once",
    "started.step1.title": "Install KubeLoop",
    "started.step1.lead":
      "Install a platform package from GitHub Releases, or build from source with Wails.",
    "started.install.copy": "Copy",
    "started.install.copied": "Copied",
    "started.install.macos.brewTitle": "Homebrew",
    "started.install.macos.shellTitle": "Shell",
    "started.install.macos.shell":
      "Download the latest DMG for your CPU, then open it and drag KubeLoop.app into Applications.",
    "started.install.macos.manualTitle": "Manual",
    "started.install.macos.manual":
      "Open the DMG from GitHub Releases and drag KubeLoop.app into Applications. If Gatekeeper blocks it, right-click → Open, or run:",
    "started.install.windows.shellTitle": "Shell",
    "started.install.windows.shell":
      "In PowerShell, download and run the latest installer:",
    "started.install.windows.installerTitle": "Installer",
    "started.install.windows.installer":
      "Run the NSIS installer from GitHub Releases.",
    "started.install.windows.portableTitle": "Portable",
    "started.install.windows.portable":
      "Extract the portable zip and run KubeLoop. If SmartScreen appears, choose More info → Run anyway.",
    "started.install.linux.shellTitle": "Shell",
    "started.install.linux.shell":
      "Download and install the latest .deb/.rpm for your distro:",
    "started.install.linux.pkgTitle": "Package",
    "started.install.linux.pkg":
      "Install kubeloop-VERSION-linux-ARCH.deb or .rpm from GitHub Releases.",
    "started.install.linux.tarballTitle": "Tarball",
    "started.install.linux.tarball":
      "Extract the tar.gz and run the KubeLoop binary from the unpacked directory.",
    "started.step2.title": "Confirm kubeconfig access",
    "started.step2.body":
      "Make sure this machine can reach the cluster API with a normal kubeconfig.",
    "started.step3.title": "Select a Context and Connect",
    "started.step3.body":
      "Open the Clusters page, pick a Context, optionally probe the API, then connect.",
    "started.step4.title": "Approve the helper once",
    "started.step4.body":
      "On first use, approve the virtual network service. Later connects should not ask again.",
    "started.after.title": "After you are connected",
    "started.after.body":
      "Use Overview for traffic and status, Workload / Network for Port Forward, Exchange, Mirror, and Preview, and Host Aliases for custom domain → IP maps. Short names like mysql.default resolve via Kubernetes search domains.",
    "started.mcp.title": "Optional: MCP for AI agents",
    "started.mcp.body":
      "Open the MCP tab, install into Cursor / Claude Code / Codex / VS Code, then refresh MCP in that client. Off by default; localhost only.",

    "product.title": "Product",
    "product.desc":
      "Transparent cluster networking — TUN, DNS, Host Aliases, cluster management, and MCP for AI agents.",
    "product.core.title": "Core capabilities",
    "product.core.tun.title": "Transparent TUN",
    "product.core.tun.body": "Managed sing-box routes only discovered Pod and Service traffic.",
    "product.core.dns.title": "Cluster DNS",
    "product.core.dns.body":
      "Split DNS with search domains — mysql.default and *.svc.cluster.local both work.",
    "product.core.hosts.title": "Host Aliases",
    "product.core.hosts.body":
      "Per-context domain → IPv4 maps via local DNS while connected. Reconnect to apply; cleared on disconnect.",
    "product.core.clusters.title": "Cluster management",
    "product.core.clusters.body": "Add kubeconfig files, list Contexts, probe and switch.",
    "product.core.mcp.title": "MCP for AI agents",
    "product.core.mcp.body":
      "Optional local Streamable HTTP server so agents can connect, discover, and run network tools.",
    "product.tools.title": "Developer tools",
    "product.tools.desc":
      "Four ways to move traffic between your app, KubeLoop, and Kubernetes.",
    "product.tools.exchange.title": "Exchange",
    "product.tools.exchange.body": "Replace a ClusterIP Service with a process on your machine.",
    "product.tools.mirror.title": "Mirror",
    "product.tools.mirror.body":
      "Keep cluster Pods as the primary path and tee a copy of TCP/UDP requests to a local process.",
    "product.tools.preview.title": "Preview",
    "product.tools.preview.body": "Expose a local app as a temporary ClusterIP Service.",
    "product.tools.portfwd.title": "Port Forward",
    "product.tools.portfwd.body": "Forward a local port to a Pod or Service.",
    "product.cap.outbound": "Outbound",
    "product.cap.inbound": "Inbound replace",
    "product.cap.tee": "Inbound tee",
    "product.cap.inboundNew": "Inbound new Service",
    "product.cap.principle": "How it works",
    "product.portfwd.principle":
      "KubeLoop opens a local listener. Your app connects to that port; KubeLoop tunnels the session through the in-cluster Gateway, which dials the target Pod IP or Service ClusterIP. No per-app proxy settings — just localhost.",
    "product.exchange.principle":
      "KubeLoop rewrites the Service's EndpointSlice so kube-proxy sends traffic to the Gateway. Callers — users or other in-cluster Services — keep the same ClusterIP and DNS name; the Gateway reverse-streams connections to KubeLoop, which forwards them to your local process. On stop, selectors and endpoints are restored.",
    "product.mirror.principle":
      "Like Exchange, Service traffic is steered to the Gateway. KubeLoop tees each request: the primary path dials the original Pod and returns its response to the caller; a shadow copy goes to your local app, whose replies are discarded. Local failures never block primary.",
    "product.preview.principle":
      "Instead of replacing an existing Service, KubeLoop creates a temporary ClusterIP Service and EndpointSlice that point at the Gateway. Users or other in-cluster Services call the new name; KubeLoop Accepts reverse streams and delivers them to your local app. On stop, the temporary resources are deleted.",
    "product.gateway.title": "Minimal Gateway",
    "product.gateway.body":
      "Unprivileged in-cluster Deployment reached only via API Server port-forward. Works with scoped RBAC and admin-preinstalled Gateways.",

    "mcp.title": "MCP for AI agents",
    "mcp.desc":
      "Let Cursor, Claude Code, Codex, or VS Code drive KubeLoop over a local Model Context Protocol endpoint.",
    "mcp.what.title": "What it is",
    "mcp.what.body":
      "KubeLoop can expose its desktop control plane as a local MCP server (Streamable HTTP on 127.0.0.1). Seven compact tools cover cluster discovery, connections, networking, traffic sessions, helper management, Pod commands, and bidirectional file transfers.",
    "mcp.setup.title": "Set up in three steps",
    "mcp.setup.s1.title": "Open the MCP tab",
    "mcp.setup.s1.body": "Start KubeLoop Desktop and open the MCP page in the sidebar.",
    "mcp.setup.s2.title": "Install into your client",
    "mcp.setup.s2.body":
      "Choose Claude Code, Codex, Cursor, or VS Code, then click Install MCP server. This writes the user-scoped config and enables the local endpoint if needed.",
    "mcp.setup.s3.title": "Refresh the client",
    "mcp.setup.s3.body":
      "Restart or refresh MCP in that client so it picks up the new server.",
    "mcp.tips.title": "Tips",
    "mcp.tips.body":
      "You can also Copy config for the selected client. MCP is off by default until enabled or installed; it binds only to localhost. Bearer token auth is optional and off by default.",
    "mcp.tools.title": "What agents can do",
    "mcp.tools.cluster.title": "manage_cluster",
    "mcp.tools.cluster.body":
      "Reload or probe contexts; list Namespaces, Services, and Pods.",
    "mcp.tools.connection.title": "manage_connection",
    "mcp.tools.connection.body":
      "Get status, connect, disconnect, or read the sing-box config.",
    "mcp.tools.network.title": "manage_network",
    "mcp.tools.network.body":
      "Get or set manual network overrides and Host Aliases.",
    "mcp.tools.traffic.title": "manage_traffic",
    "mcp.tools.traffic.body":
      "Start, stop, or list Exchange, Mirror, Preview, and Port Forward.",
    "mcp.tools.helper.title": "manage_helper",
    "mcp.tools.helper.body":
      "Get status, install, or uninstall the privileged Helper.",
    "mcp.tools.exec.title": "exec_pod_command",
    "mcp.tools.exec.body":
      "Run a shell command in a Pod and return stdout, stderr, and exit code.",
    "mcp.tools.files.title": "manage_file_transfer",
    "mcp.tools.files.body":
      "Start, list, or cancel local-to-Pod and Pod-to-local transfers.",
    "mcp.behavior.title": "Important behavior",
    "mcp.behavior.portForward":
      "Port Forward requires targetKind to be pod or service. For a Service, name stays the requested Service and podName identifies the resolved backend Pod.",
    "mcp.behavior.files":
      "File and directory transfers run asynchronously in upload or download direction. Use manage_file_transfer with action=list for progress or action=cancel with the returned task id.",

    "workflows.title": "Workflows",
    "workflows.desc":
      "Browse, debug, map domains, exchange, mirror, preview, and drive KubeLoop from AI agents — without opening a terminal for every Service.",
    "workflows.list.title": "Everyday paths",
    "workflows.w1.label": "Internal API",
    "workflows.w1.title": "Open a Service in the browser",
    "workflows.w1.body": "Connect, then use a ClusterIP or Service DNS name.",
    "workflows.w1.hint": "mysql.default.svc",
    "workflows.w2.label": "Pod debug",
    "workflows.w2.title": "Hit a real Pod IP",
    "workflows.w2.body": "Pod CIDR is routed locally after Connect.",
    "workflows.w2.hint": "10.244.x.x",
    "workflows.w3.label": "Port forward",
    "workflows.w3.title": "Skip kubectl port-forward",
    "workflows.w3.body": "Start from Workload or Network with a Namespace picker.",
    "workflows.w3.hint": "localhost:8080",
    "workflows.w4.label": "Exchange",
    "workflows.w4.title": "Run a local process as a Service",
    "workflows.w4.body": "Exchange keeps ClusterIP / DNS while traffic lands locally.",
    "workflows.w4.hint": "Exchange",
    "workflows.w5.label": "Mirror",
    "workflows.w5.title": "Debug without replacing the Service",
    "workflows.w5.body":
      "Cluster Pods keep answering clients; a copy of each TCP/UDP request is sent to your local process.",
    "workflows.w5.hint": "Mirror",
    "workflows.w6.label": "Host alias",
    "workflows.w6.title": "Map a custom domain to a cluster IP",
    "workflows.w6.body":
      "On Host Aliases, bind app.dev to a Service or Pod IP. Reconnect so split DNS picks it up.",
    "workflows.w6.hint": "app.dev → 10.96.x.x",
    "workflows.w7.label": "MCP",
    "workflows.w7.title": "Let an AI agent operate KubeLoop",
    "workflows.w7.body":
      "On the MCP tab, install into Cursor or another client, then ask the agent to connect and start Port Forward or Exchange.",
    "workflows.w7.hint": "127.0.0.1 · MCP",

    "arch.title": "Architecture",
    "arch.desc":
      "Local apps enter through sing-box. Only cluster destinations cross the SOCKS bridge into the Gateway.",
    "arch.flow.title": "Traffic path",
    "arch.flow.caption":
      "Apps enter through sing-box and the local SOCKS bridge, then reach the in-cluster Gateway via the Kubernetes API Server port-forward.",
    "arch.flow.targets": "Gateway dials cluster targets",
    "arch.n1.tag": "Desktop",
    "arch.n1.title": "Your apps",
    "arch.n1.graph": "Your apps",
    "arch.n1.body": "Browsers, IDEs, CLIs, and SDKs — no SOCKS settings per app.",
    "arch.n2.tag": "sing-box",
    "arch.n2.title": "TUN / DNS / rules",
    "arch.n2.graph": "TUN / DNS",
    "arch.n2.body":
      "Split DNS and focused routes for Pod CIDR, Service CIDR, cluster.local, and optional Host Aliases.",
    "arch.n3.tag": "Bridge",
    "arch.n3.title": "Local SOCKS5 bridge",
    "arch.n3.graph": "SOCKS5 bridge",
    "arch.n3.body": "TCP and UDP sessions multiplexed toward the cluster path.",
    "arch.n4.tag": "API",
    "arch.n4.title": "Kubernetes API Server",
    "arch.n4.graph": "API Server",
    "arch.n4.body": "port-forward only — no NodePort, LoadBalancer, or public ingress.",
    "arch.n5.tag": "Gateway",
    "arch.n5.title": "In-cluster Gateway",
    "arch.n5.graph": "Gateway",
    "arch.n5.body": "Unprivileged dialer into Pods, Services, and CoreDNS.",
    "arch.t1": "Pod IP",
    "arch.t2": "ClusterIP Service",
    "arch.t3": "CoreDNS",
    "arch.features.title": "Feature traffic paths",
    "arch.features.desc":
      "Every path has three actors — User, KubeLoop, and Kubernetes — with packets animated between them.",
    "arch.features.productLink": "See everyday workflows",
    "arch.features.actor.user": "User",
    "arch.features.actor.k8s": "Kubernetes",
    "arch.features.dir.in": "Request",
    "arch.features.dir.out": "Response",
    "arch.features.dir.inShort": "req →",
    "arch.features.dir.outShort": "← res",
    "arch.features.mirror.shadowOnly": "Shadow request only",
    "arch.features.node.app": "App",
    "arch.features.node.listen": "listen + tunnel",
    "arch.features.node.localProcess": "local process",
    "arch.features.node.caller": "Caller",
    "arch.features.node.callerSub": "user / service",
    "arch.features.node.callerUser": "User",
    "arch.features.node.callerUserSub": "client",
    "arch.features.node.callerSvc": "Other Service",
    "arch.features.node.callerSvcSub": "in-cluster",
    "arch.features.node.podService": "Pod / SVC",
    "arch.features.node.previewSvc": "Preview SVC",
    "arch.features.node.originalPod": "Original Pod",
    "arch.features.node.tee": "Tee",
    "arch.features.portfwd.tab": "Port Forward",
    "arch.features.portfwd.caption":
      "Request: User → KubeLoop → Gateway → Pod/SVC. Response returns on the same path.",
    "arch.features.portfwd.s1": "User App connects to localhost:<port>",
    "arch.features.portfwd.s2": "KubeLoop tunnels the session into Kubernetes",
    "arch.features.portfwd.s3": "Gateway dials the target Pod or Service",
    "arch.features.exchange.tab": "Exchange",
    "arch.features.exchange.caption":
      "Request: in-cluster user or other Service → Service → Gateway → KubeLoop → local App. Response returns to the caller. ClusterIP / DNS stay the same.",
    "arch.features.exchange.s1": "A user or other in-cluster Service dials the original Service",
    "arch.features.exchange.s2": "Gateway delivers the stream to KubeLoop",
    "arch.features.exchange.s3": "KubeLoop hands traffic to the local App",
    "arch.features.mirror.tab": "Mirror",
    "arch.features.mirror.caption":
      "Request from a user or other in-cluster Service forks at KubeLoop: primary to the original Pod (response returns); shadow to the local App (response discarded).",
    "arch.features.mirror.primary": "Primary + response",
    "arch.features.mirror.shadow": "shadow / discard",
    "arch.features.mirror.shadowShort": "Shadow",
    "arch.features.mirror.s1": "Caller traffic reaches KubeLoop via Gateway",
    "arch.features.mirror.s2": "Primary path returns to the original Pod in Kubernetes",
    "arch.features.mirror.s3": "Shadow copy goes to the local App; replies are discarded",
    "arch.features.preview.tab": "Preview",
    "arch.features.preview.caption":
      "Request: in-cluster user or other Service → Preview SVC → Gateway → KubeLoop → local App. Response returns to the caller.",
    "arch.features.preview.s1": "KubeLoop creates a temporary Service in Kubernetes",
    "arch.features.preview.s2": "A user or other in-cluster Service dials the Preview Service",
    "arch.features.preview.s3": "KubeLoop delivers the stream to the local App",
    "arch.more.title": "Read more",
    "arch.more.body": "Protocol, RBAC, and recovery details live in the design notes.",
  },
  "zh-CN": {
    "nav.docs": "文档",
    "nav.github": "GitHub",
    "nav.releases": "下载",
    "nav.menu": "菜单",
    "nav.overview": "概览",
    "nav.getStarted": "快速开始",
    "nav.product": "产品能力",
    "nav.workflows": "使用场景",
    "nav.mcp": "MCP",
    "nav.architecture": "架构",
    "nav.design": "设计文档",
    "sidebar.group.start": "开始",
    "sidebar.group.guides": "指南",
    "sidebar.group.reference": "参考",
    "cta.download": "下载 Release",
    "cta.github": "查看 GitHub",
    "cta.design": "设计文档",
    "cta.designHref":
      "https://github.com/fengqi-dev/kube-loop/blob/main/docs/design.zh-CN.md",
    "cta.getStarted": "快速开始",
    "footer.copy": "© KubeLoop 贡献者。MIT License。",

    "overview.title": "欢迎使用 KubeLoop",
    "overview.desc":
      "KubeLoop 是一款桌面客户端：像连 VPN 一样连上 Kubernetes，让本机应用直接访问 Pod IP、ClusterIP Service 与集群 DNS，无需 port-forward。",
    "overview.carousel.title": "开发者工具",
    "overview.carousel.desc":
      "Port Forward、Exchange、Mirror、Preview——应用、KubeLoop 与 Kubernetes 之间的动画流量路径。",
    "overview.carousel.more": "在架构页查看路径",
    "overview.start.title": "开始使用",
    "overview.start.quick.title": "快速开始",
    "overview.start.quick.body": "下载构建、选择 Context，然后连接。",
    "overview.start.clusters.title": "管理集群",
    "overview.start.clusters.body": "添加 kubeconfig、探测 API、切换 Context。",
    "overview.start.design.title": "设计文档",
    "overview.start.design.body": "架构、权限与安全边界说明。",
    "overview.guides.title": "指南",
    "overview.guides.product.title": "产品能力",
    "overview.guides.product.body": "TUN、DNS、主机别名、集群管理，以及面向 AI Agent 的 MCP。",
    "overview.guides.workflows.title": "使用场景",
    "overview.guides.workflows.body":
      "打开 Service、映射自定义域名、调试 Pod，以及 Exchange / Mirror / Preview。",
    "overview.guides.mcp.title": "面向 AI Agent 的 MCP",
    "overview.guides.mcp.body":
      "将本机 Streamable HTTP 端点安装到 Cursor、Claude Code、Codex 或 VS Code。",
    "overview.guides.arch.title": "架构",
    "overview.guides.arch.body": "流量如何从本机应用到达 Gateway。",
    "overview.more.title": "更多",
    "overview.more.releases.title": "Release",
    "overview.more.releases.body": "桌面安装包与 Gateway 镜像。",
    "overview.more.github.title": "GitHub",
    "overview.more.github.body": "源码、Issue 与贡献入口。",
    "overview.callout.title": "下载 KubeLoop",
    "overview.callout.body": "从 GitHub Releases 获取桌面构建，再用本机 kubeconfig 连接集群。",

    "started.title": "快速开始",
    "started.desc": "几分钟内把本机连上集群。",
    "started.steps.title": "连接一次",
    "started.step1.title": "安装 KubeLoop",
    "started.step1.lead": "从 GitHub Releases 安装对应平台包，或用 Wails 从源码构建。",
    "started.install.copy": "复制",
    "started.install.copied": "已复制",
    "started.install.macos.brewTitle": "Homebrew",
    "started.install.macos.shellTitle": "Shell",
    "started.install.macos.shell":
      "下载对应 CPU 架构的最新 DMG，打开后将 KubeLoop.app 拖入 Applications。",
    "started.install.macos.manualTitle": "手动安装",
    "started.install.macos.manual":
      "从 GitHub Releases 打开 DMG，将 KubeLoop.app 拖入 Applications。若被 Gatekeeper 拦截，可右键 → 打开，或执行：",
    "started.install.windows.shellTitle": "Shell",
    "started.install.windows.shell": "在 PowerShell 中下载并运行最新安装包：",
    "started.install.windows.installerTitle": "安装包",
    "started.install.windows.installer": "运行 GitHub Releases 中的 NSIS 安装包。",
    "started.install.windows.portableTitle": "便携版",
    "started.install.windows.portable":
      "解压便携 zip 后运行 KubeLoop。若出现 SmartScreen，选择「更多信息 → 仍要运行」。",
    "started.install.linux.shellTitle": "Shell",
    "started.install.linux.shell": "按发行版下载并安装最新 .deb/.rpm：",
    "started.install.linux.pkgTitle": "软件包",
    "started.install.linux.pkg":
      "从 GitHub Releases 安装 kubeloop-VERSION-linux-ARCH.deb 或 .rpm。",
    "started.install.linux.tarballTitle": "压缩包",
    "started.install.linux.tarball":
      "解压 tar.gz，在解压目录中运行 KubeLoop。",
    "started.step2.title": "确认 kubeconfig 可用",
    "started.step2.body": "确保本机可用普通 kubeconfig 访问集群 API。",
    "started.step3.title": "选择 Context 并连接",
    "started.step3.body": "打开「集群」页，选择 Context，可先探测 API，然后连接。",
    "started.step4.title": "首次批准 Helper",
    "started.step4.body": "首次使用时批准虚拟网卡服务；之后连接通常不再要求授权。",
    "started.after.title": "连接之后",
    "started.after.body":
      "在概览查看流量与状态；在工作负载 / 网络使用端口转发、Exchange、Mirror 与 Preview；在主机别名配置域名 → IP。mysql.default 等短名通过 Kubernetes 搜索域解析。",
    "started.mcp.title": "可选：面向 AI Agent 的 MCP",
    "started.mcp.body":
      "打开 MCP 页，安装到 Cursor / Claude Code / Codex / VS Code，再在对应客户端刷新 MCP。默认关闭，仅监听本机。",

    "product.title": "产品能力",
    "product.desc": "透明集群网络——TUN、DNS、主机别名、集群管理，以及面向 AI Agent 的 MCP。",
    "product.core.title": "核心能力",
    "product.core.tun.title": "透明 TUN",
    "product.core.tun.body": "托管 sing-box，仅路由已发现的 Pod 与 Service 流量。",
    "product.core.dns.title": "集群 DNS",
    "product.core.dns.body":
      "分流 DNS 与搜索域——mysql.default 与 *.svc.cluster.local 都可用。",
    "product.core.hosts.title": "主机别名",
    "product.core.hosts.body":
      "按 Context 配置域名 → IPv4；连接期间经本地 DNS 生效。改后需重连；断开时清理。",
    "product.core.clusters.title": "集群管理",
    "product.core.clusters.body": "添加 kubeconfig、列出 Context、探测并切换。",
    "product.core.mcp.title": "面向 AI Agent 的 MCP",
    "product.core.mcp.body":
      "可选的本机 Streamable HTTP 服务，供 Agent 连接、发现并操作网络工具。",
    "product.tools.title": "开发者工具",
    "product.tools.desc": "在 User App、KubeLoop 与 Kubernetes 之间搬运流量的四种方式。",
    "product.tools.exchange.title": "Exchange",
    "product.tools.exchange.body": "用本机进程替换现有 ClusterIP Service。",
    "product.tools.mirror.title": "Mirror",
    "product.tools.mirror.body": "集群原 Pod 继续响应客户端，同时将 TCP/UDP 请求拷贝一份到本机进程。",
    "product.tools.preview.title": "Preview",
    "product.tools.preview.body": "把本机应用临时暴露为 ClusterIP Service。",
    "product.tools.portfwd.title": "Port Forward",
    "product.tools.portfwd.body": "将本地端口转发到 Pod 或 Service。",
    "product.cap.outbound": "出站",
    "product.cap.inbound": "入站替换",
    "product.cap.tee": "入站分流",
    "product.cap.inboundNew": "入站新建 Service",
    "product.cap.principle": "原理",
    "product.portfwd.principle":
      "KubeLoop 在本机打开监听端口。你的应用连接该端口后，KubeLoop 经集群内 Gateway 隧道转发，由 Gateway 拨号到目标 Pod IP 或 Service ClusterIP。无需为每个应用配置代理——只用 localhost。",
    "product.exchange.principle":
      "KubeLoop 改写 Service 的 EndpointSlice，使 kube-proxy 把流量打到 Gateway。调用方（用户或集群内其他 Service）仍使用原 ClusterIP 与 DNS；Gateway 反向把连接交给 KubeLoop，再转发到本机进程。停止时恢复 selector 与 endpoints。",
    "product.mirror.principle":
      "与 Exchange 类似，Service 流量被导向 Gateway。KubeLoop 对每个请求分流：主路径拨号原 Pod 并把响应返回给调用方；影子副本到本机应用，本机回复被丢弃。本机故障不会阻塞主路径。",
    "product.preview.principle":
      "不替换已有 Service，而是由 KubeLoop 创建临时 ClusterIP Service 与 EndpointSlice，指向 Gateway。用户或集群内其他 Service 访问新名称；KubeLoop Accept 反向流并投递到本机应用。停止时删除临时资源。",
    "product.gateway.title": "最小化 Gateway",
    "product.gateway.body":
      "无特权集群内 Deployment，仅经 API Server port-forward 访问；支持受限 RBAC 与管理员预装。",

    "mcp.title": "面向 AI Agent 的 MCP",
    "mcp.desc":
      "让 Cursor、Claude Code、Codex 或 VS Code 通过本机 Model Context Protocol 端点操控 KubeLoop。",
    "mcp.what.title": "是什么",
    "mcp.what.body":
      "KubeLoop 可将桌面控制面以本机 MCP 服务暴露（127.0.0.1 上的 Streamable HTTP）。7 个精简 Tool 覆盖集群发现、连接、网络、流量 Session、Helper 管理、Pod 命令执行和双向文件传输。",
    "mcp.setup.title": "三步完成",
    "mcp.setup.s1.title": "打开 MCP 页",
    "mcp.setup.s1.body": "启动 KubeLoop Desktop，在侧栏打开 MCP 页。",
    "mcp.setup.s2.title": "安装到客户端",
    "mcp.setup.s2.body":
      "选择 Claude Code、Codex、Cursor 或 VS Code，点击「安装 MCP Server」。会写入对应用户级配置；如未启用会自动启用本机端点。",
    "mcp.setup.s3.title": "刷新客户端",
    "mcp.setup.s3.body": "在对应客户端中重启或刷新 MCP，以加载新服务。",
    "mcp.tips.title": "提示",
    "mcp.tips.body":
      "也可对所选客户端使用「复制配置」。MCP 默认关闭（安装或手动启用后开启），仅监听本机；Bearer Token 认证可选且默认关闭。",
    "mcp.tools.title": "Agent 能做什么",
    "mcp.tools.cluster.title": "manage_cluster",
    "mcp.tools.cluster.body": "重新加载或探测 Context；列出 Namespace、Service 与 Pod。",
    "mcp.tools.connection.title": "manage_connection",
    "mcp.tools.connection.body": "查询状态、连接、断开或读取 sing-box 配置。",
    "mcp.tools.network.title": "manage_network",
    "mcp.tools.network.body": "查询或设置手工网络参数与 Host Alias。",
    "mcp.tools.traffic.title": "manage_traffic",
    "mcp.tools.traffic.body": "启动、停止或列出 Exchange、Mirror、Preview 与 Port Forward。",
    "mcp.tools.helper.title": "manage_helper",
    "mcp.tools.helper.body": "查询状态、安装或卸载特权 Helper。",
    "mcp.tools.exec.title": "exec_pod_command",
    "mcp.tools.exec.body": "在 Pod 中执行 Shell 命令，返回 stdout、stderr 和退出码。",
    "mcp.tools.files.title": "manage_file_transfer",
    "mcp.tools.files.body": "启动、列出或取消本机到 Pod、Pod 到本机的传输。",
    "mcp.behavior.title": "重要行为",
    "mcp.behavior.portForward":
      "Port Forward 的 targetKind 必须为 pod 或 service。Service 转发的 name 保留请求的 Service，podName 表示解析到的后端 Pod。",
    "mcp.behavior.files":
      "文件和目录以 upload 或 download 方向异步传输。使用 manage_file_transfer 的 action=list 查询进度，或通过 action=cancel 和返回的任务 ID 取消。",

    "workflows.title": "使用场景",
    "workflows.desc":
      "浏览、排查、映射域名、Exchange / Mirror / Preview，以及用 AI Agent 操控 KubeLoop——不必为每个 Service 再开终端。",
    "workflows.list.title": "日常路径",
    "workflows.w1.label": "内部 API",
    "workflows.w1.title": "在浏览器打开 Service",
    "workflows.w1.body": "连接后使用 ClusterIP 或 Service DNS 名称。",
    "workflows.w1.hint": "mysql.default.svc",
    "workflows.w2.label": "Pod 排查",
    "workflows.w2.title": "直连真实 Pod IP",
    "workflows.w2.body": "连接后本机已路由 Pod 网段。",
    "workflows.w2.hint": "10.244.x.x",
    "workflows.w3.label": "端口转发",
    "workflows.w3.title": "告别 kubectl port-forward",
    "workflows.w3.body": "在工作负载或网络页选择 Namespace 启动。",
    "workflows.w3.hint": "localhost:8080",
    "workflows.w4.label": "Exchange",
    "workflows.w4.title": "本机进程充当 Service",
    "workflows.w4.body": "Exchange 保留 ClusterIP / DNS，流量落到本机。",
    "workflows.w4.hint": "Exchange",
    "workflows.w5.label": "Mirror",
    "workflows.w5.title": "不替换 Service 也能调试",
    "workflows.w5.body": "集群原 Pod 继续响应客户端，同时将 TCP/UDP 请求拷贝到本机进程。",
    "workflows.w5.hint": "Mirror",
    "workflows.w6.label": "主机别名",
    "workflows.w6.title": "把自定义域名指到集群 IP",
    "workflows.w6.body":
      "在「主机别名」将 app.dev 映射到 Service 或 Pod IP，重新连接后分流 DNS 生效。",
    "workflows.w6.hint": "app.dev → 10.96.x.x",
    "workflows.w7.label": "MCP",
    "workflows.w7.title": "让 AI Agent 操作 KubeLoop",
    "workflows.w7.body":
      "在 MCP 页安装到 Cursor 等客户端，然后让 Agent 连接集群并启动 Port Forward 或 Exchange。",
    "workflows.w7.hint": "127.0.0.1 · MCP",

    "arch.title": "架构",
    "arch.desc": "本地应用经 sing-box 进入。只有集群目标会穿过 SOCKS 桥到达 Gateway。",
    "arch.flow.title": "流量路径",
    "arch.flow.caption":
      "应用经 sing-box 与本机 SOCKS 桥进入，再通过 Kubernetes API Server 的 port-forward 到达集群内 Gateway。",
    "arch.flow.targets": "Gateway 拨号到集群目标",
    "arch.n1.tag": "桌面",
    "arch.n1.title": "你的应用",
    "arch.n1.graph": "你的应用",
    "arch.n1.body": "浏览器、IDE、CLI、SDK——无需逐个配置 SOCKS。",
    "arch.n2.tag": "sing-box",
    "arch.n2.title": "TUN / DNS / 规则",
    "arch.n2.graph": "TUN / DNS",
    "arch.n2.body":
      "为 Pod CIDR、Service CIDR、cluster.local 以及可选的主机别名做分流 DNS 与定向路由。",
    "arch.n3.tag": "桥",
    "arch.n3.title": "本机 SOCKS5 桥",
    "arch.n3.graph": "SOCKS5 桥",
    "arch.n3.body": "TCP / UDP 会话复用后送向集群路径。",
    "arch.n4.tag": "API",
    "arch.n4.title": "Kubernetes API Server",
    "arch.n4.graph": "API Server",
    "arch.n4.body": "仅 port-forward——无 NodePort、LoadBalancer 或公网 Ingress。",
    "arch.n5.tag": "Gateway",
    "arch.n5.title": "集群内 Gateway",
    "arch.n5.graph": "Gateway",
    "arch.n5.body": "无特权拨号到 Pod、Service 与 CoreDNS。",
    "arch.t1": "Pod IP",
    "arch.t2": "ClusterIP Service",
    "arch.t3": "CoreDNS",
    "arch.features.title": "功能流量路径",
    "arch.features.desc":
      "每条路径都有三个角色——User、KubeLoop、Kubernetes——数据包在它们之间流动。",
    "arch.features.productLink": "查看日常使用场景",
    "arch.features.actor.user": "User",
    "arch.features.actor.k8s": "Kubernetes",
    "arch.features.dir.in": "入口 / 请求",
    "arch.features.dir.out": "出口 / 响应",
    "arch.features.dir.inShort": "入口 →",
    "arch.features.dir.outShort": "← 出口",
    "arch.features.mirror.shadowOnly": "影子仅请求",
    "arch.features.node.app": "App",
    "arch.features.node.listen": "监听 + 隧道",
    "arch.features.node.localProcess": "本机进程",
    "arch.features.node.caller": "调用方",
    "arch.features.node.callerSub": "用户 / 其他服务",
    "arch.features.node.callerUser": "用户",
    "arch.features.node.callerUserSub": "客户端",
    "arch.features.node.callerSvc": "其他服务",
    "arch.features.node.callerSvcSub": "集群内",
    "arch.features.node.podService": "Pod / SVC",
    "arch.features.node.previewSvc": "Preview SVC",
    "arch.features.node.originalPod": "原 Pod",
    "arch.features.node.tee": "分流",
    "arch.features.portfwd.tab": "Port Forward",
    "arch.features.portfwd.caption":
      "入口：User → KubeLoop → Gateway → Pod/SVC；出口沿原路返回。",
    "arch.features.portfwd.s1": "User App 连接 localhost:<port>",
    "arch.features.portfwd.s2": "KubeLoop 将会话隧道到 Kubernetes",
    "arch.features.portfwd.s3": "Gateway 拨号到目标 Pod 或 Service",
    "arch.features.exchange.tab": "Exchange",
    "arch.features.exchange.caption":
      "入口：集群内用户或其他 Service → Service → Gateway → KubeLoop → 本机 App；出口响应回调用方。ClusterIP / DNS 不变。",
    "arch.features.exchange.s1": "用户或集群内其他 Service 拨号原 Service",
    "arch.features.exchange.s2": "Gateway 将流交给 KubeLoop",
    "arch.features.exchange.s3": "KubeLoop 把流量交给本机 App",
    "arch.features.mirror.tab": "Mirror",
    "arch.features.mirror.caption":
      "用户或集群内其他 Service 的请求在 KubeLoop 分流：主路径到原 Pod（响应回调用方）；影子请求到本机 App（响应丢弃）。",
    "arch.features.mirror.primary": "主路径 + 响应",
    "arch.features.mirror.shadow": "影子 / 丢弃响应",
    "arch.features.mirror.shadowShort": "影子路径",
    "arch.features.mirror.s1": "调用方流量经 Gateway 到达 KubeLoop",
    "arch.features.mirror.s2": "主路径回到 Kubernetes 中的原 Pod",
    "arch.features.mirror.s3": "影子副本到本机 App；回复被丢弃",
    "arch.features.preview.tab": "Preview",
    "arch.features.preview.caption":
      "入口：集群内用户或其他 Service → Preview SVC → Gateway → KubeLoop → 本机 App；出口响应回调用方。",
    "arch.features.preview.s1": "KubeLoop 在 Kubernetes 中创建临时 Service",
    "arch.features.preview.s2": "用户或集群内其他 Service 拨号 Preview Service",
    "arch.features.preview.s3": "KubeLoop 将流交给本机 App",
    "arch.more.title": "延伸阅读",
    "arch.more.body": "协议、RBAC 与故障恢复细节见设计文档。",
  },
};

const titles = {
  en: {
    overview: "Overview — KubeLoop Docs",
    "get-started": "Get started — KubeLoop Docs",
    product: "Product — KubeLoop Docs",
    workflows: "Workflows — KubeLoop Docs",
    architecture: "Architecture — KubeLoop Docs",
  },
  "zh-CN": {
    overview: "概览 — KubeLoop 文档",
    "get-started": "快速开始 — KubeLoop 文档",
    product: "产品能力 — KubeLoop 文档",
    workflows: "使用场景 — KubeLoop 文档",
    architecture: "架构 — KubeLoop 文档",
  },
};

const storageKey = "kubeloop.site.language";

function pageId() {
  return document.body.getAttribute("data-page") || "overview";
}

function mountShell() {
  const page = pageId();
  const header = document.getElementById("site-header");
  const sidebar = document.getElementById("site-sidebar");
  if (header) {
    header.innerHTML = `
      <div class="topbar-inner">
        <a class="brand" href="index.html">
          <img src="assets/appicon.svg" width="26" height="26" alt="" />
          <span>KubeLoop</span>
          <span class="badge" data-i18n="nav.docs">Docs</span>
        </a>
        <div class="top-actions">
          <button type="button" class="menu-btn" id="menu-toggle" data-i18n="nav.menu">Menu</button>
          <a href="https://github.com/fengqi-dev/kube-loop/releases" data-i18n="nav.releases">Releases</a>
          <a href="https://github.com/fengqi-dev/kube-loop" data-i18n="nav.github">GitHub</a>
          <div class="lang-switch" role="group" aria-label="Language">
            <button type="button" class="lang-btn" data-lang="en" aria-pressed="true">EN</button>
            <button type="button" class="lang-btn" data-lang="zh-CN" aria-pressed="false">中文</button>
          </div>
        </div>
      </div>`;
  }
  if (sidebar) {
    sidebar.innerHTML = `
      <div class="sidebar-group">
        <h2 data-i18n="sidebar.group.start">Start</h2>
        <a href="index.html" data-nav="overview" data-i18n="nav.overview">Overview</a>
        <a href="get-started.html" data-nav="get-started" data-i18n="nav.getStarted">Get started</a>
      </div>
      <div class="sidebar-group">
        <h2 data-i18n="sidebar.group.guides">Guides</h2>
        <a href="product.html" data-nav="product" data-i18n="nav.product">Product</a>
        <a href="workflows.html" data-nav="workflows" data-i18n="nav.workflows">Workflows</a>
        <a href="mcp.html" data-nav="mcp" data-i18n="nav.mcp">MCP</a>
        <a href="architecture.html" data-nav="architecture" data-i18n="nav.architecture">Architecture</a>
      </div>
      <div class="sidebar-group">
        <h2 data-i18n="sidebar.group.reference">Reference</h2>
        <a href="https://github.com/fengqi-dev/kube-loop/blob/main/docs/design.md" data-design-link data-i18n="nav.design">Design notes</a>
        <a href="https://github.com/fengqi-dev/kube-loop/releases" data-i18n="nav.releases">Releases</a>
      </div>`;
    sidebar.querySelectorAll("[data-nav]").forEach((link) => {
      if (link.getAttribute("data-nav") === page) {
        link.setAttribute("aria-current", "page");
      }
    });
  }

  const toggle = document.getElementById("menu-toggle");
  const backdrop = document.getElementById("sidebar-backdrop");
  const close = () => document.body.classList.remove("sidebar-open");
  toggle?.addEventListener("click", () => {
    document.body.classList.toggle("sidebar-open");
  });
  backdrop?.addEventListener("click", close);
  sidebar?.querySelectorAll("a").forEach((link) => {
    link.addEventListener("click", close);
  });
}

function t(key) {
  const lang = localStorage.getItem(storageKey) || "en";
  const table = dictionary[lang] || dictionary.en;
  return table[key] || dictionary.en[key] || key;
}

function applyLanguage(lang) {
  const table = dictionary[lang] || dictionary.en;
  const page = pageId();
  document.documentElement.lang = lang;
  document.title = (titles[lang] || titles.en)[page] || titles.en.overview;
  document.querySelectorAll("[data-i18n]").forEach((node) => {
    const key = node.getAttribute("data-i18n");
    const value = table[key];
    if (typeof value === "string") node.textContent = value;
  });
  document.querySelectorAll("[data-i18n-aria]").forEach((node) => {
    if (node.classList.contains("is-copied")) return;
    const key = node.getAttribute("data-i18n-aria");
    const value = table[key];
    if (typeof value === "string") node.setAttribute("aria-label", value);
  });
  const designHref =
    table["cta.designHref"] ||
    "https://github.com/fengqi-dev/kube-loop/blob/main/docs/design.md";
  document.querySelectorAll("[data-design-link]").forEach((node) => {
    node.setAttribute("href", designHref);
  });
  document.querySelectorAll(".lang-btn").forEach((button) => {
    button.setAttribute(
      "aria-pressed",
      button.getAttribute("data-lang") === lang ? "true" : "false",
    );
  });
  localStorage.setItem(storageKey, lang);
}

function initLanguage() {
  const saved = localStorage.getItem(storageKey);
  const preferred =
    saved === "zh-CN" || saved === "en"
      ? saved
      : navigator.language?.toLowerCase().startsWith("zh")
        ? "zh-CN"
        : "en";
  applyLanguage(preferred);
  document.querySelectorAll(".lang-btn").forEach((button) => {
    button.addEventListener("click", () => {
      applyLanguage(button.getAttribute("data-lang") || "en");
      window.KubeLoopFlows?.remount(t);
    });
  });
}

function initFlowTabs() {
  const tabs = Array.from(document.querySelectorAll(".flow-tab"));
  const panels = Array.from(document.querySelectorAll(".flow-panel"));
  const section = document.getElementById("feature-flows");
  if (tabs.length === 0) return;

  const known = new Set(tabs.map((tab) => tab.getAttribute("data-flow")));

  const activate = (flow, syncHash = false) => {
    const next = known.has(flow) ? flow : "portfwd";
    tabs.forEach((tab) => {
      const selected = tab.getAttribute("data-flow") === next;
      tab.setAttribute("aria-selected", selected ? "true" : "false");
    });
    panels.forEach((panel) => {
      const selected = panel.getAttribute("data-flow") === next;
      panel.classList.toggle("is-active", selected);
      if (selected) panel.removeAttribute("hidden");
      else panel.setAttribute("hidden", "");
    });
    if (syncHash && known.has(next)) {
      history.replaceState(null, "", `#${next}`);
    }
    requestAnimationFrame(() => window.KubeLoopFlows?.resizeVisible());
  };

  tabs.forEach((tab) => {
    tab.addEventListener("click", () => {
      activate(tab.getAttribute("data-flow") || "portfwd", true);
    });
  });

  const fromHash = () => {
    const flow = location.hash.replace(/^#/, "");
    if (known.has(flow)) {
      activate(flow);
      section?.scrollIntoView({ block: "start" });
    }
  };

  fromHash();
  window.addEventListener("hashchange", fromHash);
}

function initFeatureCarousel() {
  const root = document.querySelector("[data-carousel]");
  if (!root) return;

  const slides = Array.from(root.querySelectorAll(".flow-carousel-slide"));
  const tabs = Array.from(root.querySelectorAll(".flow-carousel-tab"));
  const dots = Array.from(root.querySelectorAll(".flow-carousel-dot"));
  const order = slides.map((slide) => slide.getAttribute("data-slide")).filter(Boolean);
  if (order.length === 0) return;

  let index = Math.max(0, order.indexOf(slides.find((s) => s.classList.contains("is-active"))?.getAttribute("data-slide")));
  let timer = 0;
  const reduceMotion =
    typeof matchMedia === "function" &&
    matchMedia("(prefers-reduced-motion: reduce)").matches;
  const intervalMs = 5600;

  const activate = (slideId) => {
    const next = order.includes(slideId) ? slideId : order[0];
    index = order.indexOf(next);
    slides.forEach((slide) => {
      const selected = slide.getAttribute("data-slide") === next;
      slide.classList.toggle("is-active", selected);
      if (selected) slide.removeAttribute("hidden");
      else slide.setAttribute("hidden", "");
    });
    tabs.forEach((tab) => {
      tab.setAttribute(
        "aria-selected",
        tab.getAttribute("data-slide") === next ? "true" : "false",
      );
    });
    dots.forEach((dot) => {
      dot.classList.toggle("is-active", dot.getAttribute("data-slide") === next);
    });
    requestAnimationFrame(() => window.KubeLoopFlows?.resizeVisible());
  };

  const step = (delta) => {
    const next = (index + delta + order.length) % order.length;
    activate(order[next]);
  };

  const stop = () => {
    if (timer) {
      clearInterval(timer);
      timer = 0;
    }
  };

  const start = () => {
    if (reduceMotion) return;
    stop();
    timer = window.setInterval(() => step(1), intervalMs);
  };

  tabs.forEach((tab) => {
    tab.addEventListener("click", () => {
      activate(tab.getAttribute("data-slide") || order[0]);
      start();
    });
  });
  dots.forEach((dot) => {
    dot.addEventListener("click", () => {
      activate(dot.getAttribute("data-slide") || order[0]);
      start();
    });
  });
  root.querySelector("[data-carousel-prev]")?.addEventListener("click", () => {
    step(-1);
    start();
  });
  root.querySelector("[data-carousel-next]")?.addEventListener("click", () => {
    step(1);
    start();
  });

  root.addEventListener("mouseenter", stop);
  root.addEventListener("mouseleave", start);
  root.addEventListener("focusin", stop);
  root.addEventListener("focusout", (event) => {
    if (!root.contains(event.relatedTarget)) start();
  });

  activate(order[index] || order[0]);
  start();
}

function preferredInstallOs() {
  const ua = navigator.userAgent || "";
  const platform = navigator.platform || "";
  if (/Mac|iPhone|iPad|iPod/i.test(ua) || /Mac/i.test(platform)) return "macos";
  if (/Win/i.test(ua) || /Win/i.test(platform)) return "windows";
  if (/Linux|X11|CrOS/i.test(ua) || /Linux/i.test(platform)) return "linux";
  return "macos";
}

async function copyText(text) {
  if (navigator.clipboard?.writeText) {
    await navigator.clipboard.writeText(text);
    return;
  }
  const ta = document.createElement("textarea");
  ta.value = text;
  ta.setAttribute("readonly", "");
  ta.style.position = "fixed";
  ta.style.left = "-9999px";
  document.body.appendChild(ta);
  ta.select();
  document.execCommand("copy");
  ta.remove();
}

function initInstallCopyButtons() {
  document.querySelectorAll("pre.install-code").forEach((pre) => {
    if (pre.querySelector(".install-copy")) return;
    const code = pre.querySelector("code");
    if (!code) return;

    const btn = document.createElement("button");
    btn.type = "button";
    btn.className = "install-copy";
    btn.setAttribute("data-i18n-aria", "started.install.copy");
    btn.setAttribute("aria-label", t("started.install.copy"));
    btn.innerHTML =
      '<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><rect x="9" y="9" width="13" height="13" rx="2"/><path d="M5 15H4a2 2 0 0 1-2-2V4a2 2 0 0 1 2-2h9a2 2 0 0 1 2 2v1"/></svg>';

    let timer = 0;
    btn.addEventListener("click", async () => {
      try {
        await copyText(code.textContent || "");
        btn.classList.add("is-copied");
        btn.setAttribute("aria-label", t("started.install.copied"));
        btn.innerHTML =
          '<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><path d="M20 6 9 17l-5-5"/></svg>';
        window.clearTimeout(timer);
        timer = window.setTimeout(() => {
          btn.classList.remove("is-copied");
          btn.setAttribute("aria-label", t("started.install.copy"));
          btn.innerHTML =
            '<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><rect x="9" y="9" width="13" height="13" rx="2"/><path d="M5 15H4a2 2 0 0 1-2-2V4a2 2 0 0 1 2-2h9a2 2 0 0 1 2 2v1"/></svg>';
        }, 1600);
      } catch {
        /* ignore clipboard failures */
      }
    });

    pre.appendChild(btn);
  });
}

function initInstallOsTabs() {
  const root = document.querySelector("[data-install-os]");
  if (!root) return;

  const tabs = Array.from(root.querySelectorAll(".install-os-tab"));
  const panels = Array.from(root.querySelectorAll(".install-os-panel"));
  const known = new Set(tabs.map((tab) => tab.getAttribute("data-os")));

  const activate = (os) => {
    const next = known.has(os) ? os : "macos";
    tabs.forEach((tab) => {
      const selected = tab.getAttribute("data-os") === next;
      tab.setAttribute("aria-selected", selected ? "true" : "false");
    });
    panels.forEach((panel) => {
      const selected = panel.getAttribute("data-os") === next;
      panel.classList.toggle("is-active", selected);
      if (selected) panel.removeAttribute("hidden");
      else panel.setAttribute("hidden", "");
    });
  };

  tabs.forEach((tab) => {
    tab.addEventListener("click", () => {
      activate(tab.getAttribute("data-os") || "macos");
    });
  });

  activate(preferredInstallOs());
  initInstallCopyButtons();
}

function mountFlowDiagrams() {
  return window.KubeLoopFlows?.mount(t);
}

mountShell();
initLanguage();
initInstallOsTabs();
Promise.resolve(mountFlowDiagrams())
  .catch(() => {})
  .finally(() => {
    initFlowTabs();
    initFeatureCarousel();
    window.KubeLoopFlows?.resizeVisible();
  });
window.addEventListener("resize", () => {
  window.KubeLoopFlows?.resizeVisible();
});
