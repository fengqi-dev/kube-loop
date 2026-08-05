export namespace app {

	export class BootstrapData {
	    contexts: cluster.ContextInfo[];
	    namespaces: string[];
	    session: session.State;
	    update: update.Info;
	    preferredContext?: string;
	    preferredNamespace?: string;
	    preferredMode?: string;
	    platform: string;
	    kubeconfigFiles?: cluster.KubeconfigFileInfo[];

	    static createFrom(source: any = {}) {
	        return new BootstrapData(source);
	    }

	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.contexts = this.convertValues(source["contexts"], cluster.ContextInfo);
	        this.namespaces = source["namespaces"];
	        this.session = this.convertValues(source["session"], session.State);
	        this.update = this.convertValues(source["update"], update.Info);
	        this.preferredContext = source["preferredContext"];
	        this.preferredNamespace = source["preferredNamespace"];
	        this.preferredMode = source["preferredMode"];
	        this.platform = source["platform"];
	        this.kubeconfigFiles = this.convertValues(source["kubeconfigFiles"], cluster.KubeconfigFileInfo);
	    }

		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}

}

export namespace cluster {

	export class Capabilities {
	    gatewayInstall: boolean;
	    gatewayPortForward: boolean;
	    clusterNodes: boolean;
	    inventoryCluster: boolean;
	    serviceWrite: boolean;
	    serviceCreate: boolean;
	    podExec: boolean;
	    scopeNamespaces?: string[];
	    issues?: string[];

	    static createFrom(source: any = {}) {
	        return new Capabilities(source);
	    }

	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.gatewayInstall = source["gatewayInstall"];
	        this.gatewayPortForward = source["gatewayPortForward"];
	        this.clusterNodes = source["clusterNodes"];
	        this.inventoryCluster = source["inventoryCluster"];
	        this.serviceWrite = source["serviceWrite"];
	        this.serviceCreate = source["serviceCreate"];
	        this.podExec = source["podExec"];
	        this.scopeNamespaces = source["scopeNamespaces"];
	        this.issues = source["issues"];
	    }
	}
	export class KubeconfigFileInfo {
	    path: string;
	    default: boolean;

	    static createFrom(source: any = {}) {
	        return new KubeconfigFileInfo(source);
	    }

	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.path = source["path"];
	        this.default = source["default"];
	    }
	}
	export class ContextInfo {
	    name: string;
	    cluster: string;
	    server?: string;
	    user?: string;
	    namespace?: string;
	    source?: string;
	    current: boolean;

	    static createFrom(source: any = {}) {
	        return new ContextInfo(source);
	    }

	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.name = source["name"];
	        this.cluster = source["cluster"];
	        this.server = source["server"];
	        this.user = source["user"];
	        this.namespace = source["namespace"];
	        this.source = source["source"];
	        this.current = source["current"];
	    }
	}
	export class ClusterInventory {
	    contexts: ContextInfo[];
	    files: KubeconfigFileInfo[];

	    static createFrom(source: any = {}) {
	        return new ClusterInventory(source);
	    }

	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.contexts = this.convertValues(source["contexts"], ContextInfo);
	        this.files = this.convertValues(source["files"], KubeconfigFileInfo);
	    }

		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}

	export class Discovery {
	    podCIDRs: string[];
	    serviceCIDRs: string[];
	    serviceIPs: string[];
	    dnsServer: string;
	    clusterDomains?: string[];
	    pods: number;
	    services: number;
	    deployments: number;

	    static createFrom(source: any = {}) {
	        return new Discovery(source);
	    }

	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.podCIDRs = source["podCIDRs"];
	        this.serviceCIDRs = source["serviceCIDRs"];
	        this.serviceIPs = source["serviceIPs"];
	        this.dnsServer = source["dnsServer"];
	        this.clusterDomains = source["clusterDomains"];
	        this.pods = source["pods"];
	        this.services = source["services"];
	        this.deployments = source["deployments"];
	    }
	}
	export class InterceptPort {
	    name: string;
	    protocol: string;
	    servicePort: number;
	    listenPort: number;

	    static createFrom(source: any = {}) {
	        return new InterceptPort(source);
	    }

	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.name = source["name"];
	        this.protocol = source["protocol"];
	        this.servicePort = source["servicePort"];
	        this.listenPort = source["listenPort"];
	    }
	}

	export class ManualNetwork {
	    podCIDRs?: string[];
	    serviceCIDRs?: string[];
	    dnsServer?: string;
	    clusterDomains?: string[];
	    dnsNamespace?: string;

	    static createFrom(source: any = {}) {
	        return new ManualNetwork(source);
	    }

	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.podCIDRs = source["podCIDRs"];
	        this.serviceCIDRs = source["serviceCIDRs"];
	        this.dnsServer = source["dnsServer"];
	        this.clusterDomains = source["clusterDomains"];
	        this.dnsNamespace = source["dnsNamespace"];
	    }
	}
	export class PodPortInfo {
	    name: string;
	    port: number;
	    protocol: string;

	    static createFrom(source: any = {}) {
	        return new PodPortInfo(source);
	    }

	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.name = source["name"];
	        this.port = source["port"];
	        this.protocol = source["protocol"];
	    }
	}
	export class PodInfo {
	    name: string;
	    uid?: string;
	    namespace: string;
	    phase: string;
	    ready: boolean;
	    ip?: string;
	    node?: string;
	    containers: string[];
	    ports: PodPortInfo[];

	    static createFrom(source: any = {}) {
	        return new PodInfo(source);
	    }

	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.name = source["name"];
	        this.uid = source["uid"];
	        this.namespace = source["namespace"];
	        this.phase = source["phase"];
	        this.ready = source["ready"];
	        this.ip = source["ip"];
	        this.node = source["node"];
	        this.containers = source["containers"];
	        this.ports = this.convertValues(source["ports"], PodPortInfo);
	    }

		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}

	export class ProbeResult {
	    context: string;
	    ok: boolean;
	    version?: string;
	    latencyMs?: number;
	    error?: string;

	    static createFrom(source: any = {}) {
	        return new ProbeResult(source);
	    }

	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.context = source["context"];
	        this.ok = source["ok"];
	        this.version = source["version"];
	        this.latencyMs = source["latencyMs"];
	        this.error = source["error"];
	    }
	}
	export class ServicePortInfo {
	    name: string;
	    port: number;
	    protocol: string;

	    static createFrom(source: any = {}) {
	        return new ServicePortInfo(source);
	    }

	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.name = source["name"];
	        this.port = source["port"];
	        this.protocol = source["protocol"];
	    }
	}
	export class ServiceInfo {
	    name: string;
	    namespace: string;
	    clusterIP: string;
	    ports: ServicePortInfo[];

	    static createFrom(source: any = {}) {
	        return new ServiceInfo(source);
	    }

	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.name = source["name"];
	        this.namespace = source["namespace"];
	        this.clusterIP = source["clusterIP"];
	        this.ports = this.convertValues(source["ports"], ServicePortInfo);
	    }

		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}

}

export namespace filemanager {

	export class FileEntry {
	    name: string;
	    path: string;
	    dir: boolean;
	    size: number;
	    mode: number;
	    // Go type: time
	    modTime: any;

	    static createFrom(source: any = {}) {
	        return new FileEntry(source);
	    }

	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.name = source["name"];
	        this.path = source["path"];
	        this.dir = source["dir"];
	        this.size = source["size"];
	        this.mode = source["mode"];
	        this.modTime = this.convertValues(source["modTime"], null);
	    }

		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class Target {
	    context: string;
	    namespace: string;
	    pod: string;
	    podUID?: string;
	    container: string;

	    static createFrom(source: any = {}) {
	        return new Target(source);
	    }

	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.context = source["context"];
	        this.namespace = source["namespace"];
	        this.pod = source["pod"];
	        this.podUID = source["podUID"];
	        this.container = source["container"];
	    }
	}
	export class TransferRequest {
	    direction: string;
	    target: Target;
	    sourcePath: string;
	    destinationDir: string;
	    overwrite: boolean;

	    static createFrom(source: any = {}) {
	        return new TransferRequest(source);
	    }

	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.direction = source["direction"];
	        this.target = this.convertValues(source["target"], Target);
	        this.sourcePath = source["sourcePath"];
	        this.destinationDir = source["destinationDir"];
	        this.overwrite = source["overwrite"];
	    }

		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class TransferTask {
	    id: string;
	    direction: string;
	    target: Target;
	    sourcePath: string;
	    destinationPath: string;
	    tempPath?: string;
	    directory?: boolean;
	    status: string;
	    totalBytes: number;
	    doneBytes: number;
	    // Go type: time
	    sourceModTime: any;
	    overwrite: boolean;
	    error?: string;
	    // Go type: time
	    createdAt: any;
	    // Go type: time
	    updatedAt: any;
	    // Go type: time
	    completedAt?: any;

	    static createFrom(source: any = {}) {
	        return new TransferTask(source);
	    }

	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.direction = source["direction"];
	        this.target = this.convertValues(source["target"], Target);
	        this.sourcePath = source["sourcePath"];
	        this.destinationPath = source["destinationPath"];
	        this.tempPath = source["tempPath"];
	        this.directory = source["directory"];
	        this.status = source["status"];
	        this.totalBytes = source["totalBytes"];
	        this.doneBytes = source["doneBytes"];
	        this.sourceModTime = this.convertValues(source["sourceModTime"], null);
	        this.overwrite = source["overwrite"];
	        this.error = source["error"];
	        this.createdAt = this.convertValues(source["createdAt"], null);
	        this.updatedAt = this.convertValues(source["updatedAt"], null);
	        this.completedAt = this.convertValues(source["completedAt"], null);
	    }

		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}

}

export namespace helperapi {

	export class Status {
	    installed: boolean;
	    running: boolean;
	    coreReady: boolean;
	    version?: string;
	    protocol?: number;
	    expected: string;
	    socket: string;
	    error?: string;

	    static createFrom(source: any = {}) {
	        return new Status(source);
	    }

	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.installed = source["installed"];
	        this.running = source["running"];
	        this.coreReady = source["coreReady"];
	        this.version = source["version"];
	        this.protocol = source["protocol"];
	        this.expected = source["expected"];
	        this.socket = source["socket"];
	        this.error = source["error"];
	    }
	}

}

export namespace intercept {

	export class PortMapping {
	    servicePort: number;
	    protocol: string;
	    localHost: string;
	    localPort: number;

	    static createFrom(source: any = {}) {
	        return new PortMapping(source);
	    }

	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.servicePort = source["servicePort"];
	        this.protocol = source["protocol"];
	        this.localHost = source["localHost"];
	        this.localPort = source["localPort"];
	    }
	}
	export class Info {
	    id: string;
	    namespace: string;
	    service: string;
	    clusterIP?: string;
	    preview?: boolean;
	    mode?: string;
	    ports: cluster.InterceptPort[];
	    locals: PortMapping[];

	    static createFrom(source: any = {}) {
	        return new Info(source);
	    }

	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.namespace = source["namespace"];
	        this.service = source["service"];
	        this.clusterIP = source["clusterIP"];
	        this.preview = source["preview"];
	        this.mode = source["mode"];
	        this.ports = this.convertValues(source["ports"], cluster.InterceptPort);
	        this.locals = this.convertValues(source["locals"], PortMapping);
	    }

		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class Mapping {
	    namespace: string;
	    service: string;
	    ports: PortMapping[];
	    mode?: string;

	    static createFrom(source: any = {}) {
	        return new Mapping(source);
	    }

	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.namespace = source["namespace"];
	        this.service = source["service"];
	        this.ports = this.convertValues(source["ports"], PortMapping);
	        this.mode = source["mode"];
	    }

		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}

	export class PreviewRequest {
	    namespace: string;
	    name: string;
	    ports: PortMapping[];

	    static createFrom(source: any = {}) {
	        return new PreviewRequest(source);
	    }

	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.namespace = source["namespace"];
	        this.name = source["name"];
	        this.ports = this.convertValues(source["ports"], PortMapping);
	    }

		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}

}

export namespace mcp {

	export class InstallResult {
	    client: string;
	    path: string;

	    static createFrom(source: any = {}) {
	        return new InstallResult(source);
	    }

	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.client = source["client"];
	        this.path = source["path"];
	    }
	}
	export class Status {
	    enabled: boolean;
	    listening: boolean;
	    url?: string;
	    port: number;
	    tokenEnabled: boolean;
	    token?: string;
	    error?: string;

	    static createFrom(source: any = {}) {
	        return new Status(source);
	    }

	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.enabled = source["enabled"];
	        this.listening = source["listening"];
	        this.url = source["url"];
	        this.port = source["port"];
	        this.tokenEnabled = source["tokenEnabled"];
	        this.token = source["token"];
	        this.error = source["error"];
	    }
	}

}

export namespace podssh {

	export class EnableRequest {
	    context: string;
	    namespace: string;
	    pod: string;
	    container?: string;

	    static createFrom(source: any = {}) {
	        return new EnableRequest(source);
	    }

	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.context = source["context"];
	        this.namespace = source["namespace"];
	        this.pod = source["pod"];
	        this.container = source["container"];
	    }
	}
	export class Info {
	    id: string;
	    context: string;
	    namespace: string;
	    pod: string;
	    container: string;
	    ip: string;
	    port: number;
	    command: string;

	    static createFrom(source: any = {}) {
	        return new Info(source);
	    }

	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.context = source["context"];
	        this.namespace = source["namespace"];
	        this.pod = source["pod"];
	        this.container = source["container"];
	        this.ip = source["ip"];
	        this.port = source["port"];
	        this.command = source["command"];
	    }
	}

}

export namespace portfwd {

	export class Info {
	    id: string;
	    context: string;
	    namespace: string;
	    kind: string;
	    name: string;
	    podName: string;
	    protocol: string;
	    remotePort: number;
	    localPort: number;
	    address: string;

	    static createFrom(source: any = {}) {
	        return new Info(source);
	    }

	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.context = source["context"];
	        this.namespace = source["namespace"];
	        this.kind = source["kind"];
	        this.name = source["name"];
	        this.podName = source["podName"];
	        this.protocol = source["protocol"];
	        this.remotePort = source["remotePort"];
	        this.localPort = source["localPort"];
	        this.address = source["address"];
	    }
	}
	export class Request {
	    context: string;
	    namespace: string;
	    kind: string;
	    name: string;
	    protocol?: string;
	    remotePort: number;
	    localPort: number;

	    static createFrom(source: any = {}) {
	        return new Request(source);
	    }

	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.context = source["context"];
	        this.namespace = source["namespace"];
	        this.kind = source["kind"];
	        this.name = source["name"];
	        this.protocol = source["protocol"];
	        this.remotePort = source["remotePort"];
	        this.localPort = source["localPort"];
	    }
	}

}

export namespace session {

	export class ConnectivityTestResult {
	    passed: boolean;
	    failedLayer?: string;
	    error?: string;

	    static createFrom(source: any = {}) {
	        return new ConnectivityTestResult(source);
	    }

	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.passed = source["passed"];
	        this.failedLayer = source["failedLayer"];
	        this.error = source["error"];
	    }
	}
	export class LogEvent {
	    // Go type: time
	    time: any;
	    level: string;
	    message: string;

	    static createFrom(source: any = {}) {
	        return new LogEvent(source);
	    }

	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.time = this.convertValues(source["time"], null);
	        this.level = source["level"];
	        this.message = source["message"];
	    }

		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class NetworkDiagnostic {
	    code: string;
	    severity: string;
	    message: string;
	    target?: string;
	    conflict?: string;
	    interface?: string;

	    static createFrom(source: any = {}) {
	        return new NetworkDiagnostic(source);
	    }

	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.code = source["code"];
	        this.severity = source["severity"];
	        this.message = source["message"];
	        this.target = source["target"];
	        this.conflict = source["conflict"];
	        this.interface = source["interface"];
	    }
	}
	export class NetworkDiagnostics {
	    routingMode: string;
	    strictRoute: boolean;
	    issues?: NetworkDiagnostic[];

	    static createFrom(source: any = {}) {
	        return new NetworkDiagnostics(source);
	    }

	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.routingMode = source["routingMode"];
	        this.strictRoute = source["strictRoute"];
	        this.issues = this.convertValues(source["issues"], NetworkDiagnostic);
	    }

		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class State {
	    phase: string;
	    mode?: string;
	    context: string;
	    namespace: string;
	    dnsNamespace?: string;
	    message: string;
	    error?: string;
	    dnsWarning?: string;
	    network?: NetworkDiagnostics;
	    discovery?: cluster.Discovery;
	    capabilities?: cluster.Capabilities;
	    scopeNamespaces?: string[];
	    gatewayManifest?: string;
	    pods?: cluster.PodInfo[];
	    services?: cluster.ServiceInfo[];
	    events?: LogEvent[];
	    coreVersion?: string;
	    socksPort?: number;
	    // Go type: time
	    connectedAt?: any;
	    metrics?: singbox.Metrics;
	    inventoryRevision: number;
	    kubernetesVersion?: string;
	    // Go type: time
	    updatedAt: any;

	    static createFrom(source: any = {}) {
	        return new State(source);
	    }

	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.phase = source["phase"];
	        this.mode = source["mode"];
	        this.context = source["context"];
	        this.namespace = source["namespace"];
	        this.dnsNamespace = source["dnsNamespace"];
	        this.message = source["message"];
	        this.error = source["error"];
	        this.dnsWarning = source["dnsWarning"];
	        this.network = this.convertValues(source["network"], NetworkDiagnostics);
	        this.discovery = this.convertValues(source["discovery"], cluster.Discovery);
	        this.capabilities = this.convertValues(source["capabilities"], cluster.Capabilities);
	        this.scopeNamespaces = source["scopeNamespaces"];
	        this.gatewayManifest = source["gatewayManifest"];
	        this.pods = this.convertValues(source["pods"], cluster.PodInfo);
	        this.services = this.convertValues(source["services"], cluster.ServiceInfo);
	        this.events = this.convertValues(source["events"], LogEvent);
	        this.coreVersion = source["coreVersion"];
	        this.socksPort = source["socksPort"];
	        this.connectedAt = this.convertValues(source["connectedAt"], null);
	        this.metrics = this.convertValues(source["metrics"], singbox.Metrics);
	        this.inventoryRevision = source["inventoryRevision"];
	        this.kubernetesVersion = source["kubernetesVersion"];
	        this.updatedAt = this.convertValues(source["updatedAt"], null);
	    }

		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}

}

export namespace singbox {

	export class Connection {
	    id: string;
	    network: string;
	    source: string;
	    destination: string;
	    process: string;
	    upload: number;
	    download: number;
	    uploadSpeed?: number;
	    downloadSpeed?: number;
	    startedAt: string;
	    inbound: string;
	    feature?: string;
	    outbound: string;
	    rule: string;

	    static createFrom(source: any = {}) {
	        return new Connection(source);
	    }

	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.network = source["network"];
	        this.source = source["source"];
	        this.destination = source["destination"];
	        this.process = source["process"];
	        this.upload = source["upload"];
	        this.download = source["download"];
	        this.uploadSpeed = source["uploadSpeed"];
	        this.downloadSpeed = source["downloadSpeed"];
	        this.startedAt = source["startedAt"];
	        this.inbound = source["inbound"];
	        this.feature = source["feature"];
	        this.outbound = source["outbound"];
	        this.rule = source["rule"];
	    }
	}
	export class Metrics {
	    downloadTotal: number;
	    uploadTotal: number;
	    memory?: number;
	    activeConnections: number;
	    connections: Connection[];

	    static createFrom(source: any = {}) {
	        return new Metrics(source);
	    }

	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.downloadTotal = source["downloadTotal"];
	        this.uploadTotal = source["uploadTotal"];
	        this.memory = source["memory"];
	        this.activeConnections = source["activeConnections"];
	        this.connections = this.convertValues(source["connections"], Connection);
	    }

		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}

}

export namespace store {

	export class HostAliasSpec {
	    domain: string;
	    ip: string;

	    static createFrom(source: any = {}) {
	        return new HostAliasSpec(source);
	    }

	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.domain = source["domain"];
	        this.ip = source["ip"];
	    }
	}
	export class SessionIntentCounts {
	    podPortForwards: number;
	    networkPortForwards: number;
	    exchanges: number;
	    mirrors: number;

	    static createFrom(source: any = {}) {
	        return new SessionIntentCounts(source);
	    }

	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.podPortForwards = source["podPortForwards"];
	        this.networkPortForwards = source["networkPortForwards"];
	        this.exchanges = source["exchanges"];
	        this.mirrors = source["mirrors"];
	    }
	}

}

export namespace update {

	export class Info {
	    currentVersion: string;
	    latestVersion?: string;
	    available: boolean;
	    url: string;
	    // Go type: time
	    publishedAt: any;
	    // Go type: time
	    checkedAt: any;
	    error?: string;

	    static createFrom(source: any = {}) {
	        return new Info(source);
	    }

	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.currentVersion = source["currentVersion"];
	        this.latestVersion = source["latestVersion"];
	        this.available = source["available"];
	        this.url = source["url"];
	        this.publishedAt = this.convertValues(source["publishedAt"], null);
	        this.checkedAt = this.convertValues(source["checkedAt"], null);
	        this.error = source["error"];
	    }

		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}

}
