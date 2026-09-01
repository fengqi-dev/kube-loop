export namespace app {
	
	export class AuthSession {
	    authenticated: boolean;
	    userName?: string;
	    accessExpiresAt: string;
	    refreshExpiresAt: string;
	
	    static createFrom(source: any = {}) {
	        return new AuthSession(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.authenticated = source["authenticated"];
	        this.userName = source["userName"];
	        this.accessExpiresAt = source["accessExpiresAt"];
	        this.refreshExpiresAt = source["refreshExpiresAt"];
	    }
	}
	export class BootstrapData {
	    update: update.Info;
	    platform: string;
	    coreVersion: string;
	    serverProfiles: profile.State;
	
	    static createFrom(source: any = {}) {
	        return new BootstrapData(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.update = this.convertValues(source["update"], update.Info);
	        this.platform = source["platform"];
	        this.coreVersion = source["coreVersion"];
	        this.serverProfiles = this.convertValues(source["serverProfiles"], profile.State);
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
	export class RemoteInventory {
	    kubernetesVersion: string;
	    gatewayVersion: string;
	    namespaces: remote.Namespace[];
	    namespace?: string;
	    capabilities: string[];
	    pods: remote.Pod[];
	    services: remote.Service[];
	    session?: remote.Session;
	    network?: networkdiag.Result;
	    dataPlane?: dataplane.Status;
	
	    static createFrom(source: any = {}) {
	        return new RemoteInventory(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.kubernetesVersion = source["kubernetesVersion"];
	        this.gatewayVersion = source["gatewayVersion"];
	        this.namespaces = this.convertValues(source["namespaces"], remote.Namespace);
	        this.namespace = source["namespace"];
	        this.capabilities = source["capabilities"];
	        this.pods = this.convertValues(source["pods"], remote.Pod);
	        this.services = this.convertValues(source["services"], remote.Service);
	        this.session = this.convertValues(source["session"], remote.Session);
	        this.network = this.convertValues(source["network"], networkdiag.Result);
	        this.dataPlane = this.convertValues(source["dataPlane"], dataplane.Status);
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
	export class SaveServerProfileRequest {
	    id?: string;
	    baseUrl: string;
	    displayName?: string;
	    activate: boolean;
	
	    static createFrom(source: any = {}) {
	        return new SaveServerProfileRequest(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.baseUrl = source["baseUrl"];
	        this.displayName = source["displayName"];
	        this.activate = source["activate"];
	    }
	}
	export class ServerExecRequest {
	    profileId: string;
	    pod: string;
	    container?: string;
	    command: string[];
	    tty: boolean;
	    width?: number;
	    height?: number;
	
	    static createFrom(source: any = {}) {
	        return new ServerExecRequest(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.profileId = source["profileId"];
	        this.pod = source["pod"];
	        this.container = source["container"];
	        this.command = source["command"];
	        this.tty = source["tty"];
	        this.width = source["width"];
	        this.height = source["height"];
	    }
	}
	export class ServerLocalFileEntry {
	    name: string;
	    path: string;
	    kind: string;
	    size: number;
	    mode: number;
	    modifiedAt: string;
	
	    static createFrom(source: any = {}) {
	        return new ServerLocalFileEntry(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.name = source["name"];
	        this.path = source["path"];
	        this.kind = source["kind"];
	        this.size = source["size"];
	        this.mode = source["mode"];
	        this.modifiedAt = source["modifiedAt"];
	    }
	}
	export class ServerNetworkSettings {
	    dnsNamespace?: string;
	    socksPort: number;
	    hostAliases?: profile.HostAlias[];
	
	    static createFrom(source: any = {}) {
	        return new ServerNetworkSettings(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.dnsNamespace = source["dnsNamespace"];
	        this.socksPort = source["socksPort"];
	        this.hostAliases = this.convertValues(source["hostAliases"], profile.HostAlias);
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
	export class ServerPodFileCreateRequest {
	    profileId: string;
	    pod: string;
	    container?: string;
	    path: string;
	    kind: string;
	
	    static createFrom(source: any = {}) {
	        return new ServerPodFileCreateRequest(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.profileId = source["profileId"];
	        this.pod = source["pod"];
	        this.container = source["container"];
	        this.path = source["path"];
	        this.kind = source["kind"];
	    }
	}
	export class ServerPodFileDeleteRequest {
	    profileId: string;
	    pod: string;
	    container?: string;
	    path: string;
	    recursive?: boolean;
	
	    static createFrom(source: any = {}) {
	        return new ServerPodFileDeleteRequest(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.profileId = source["profileId"];
	        this.pod = source["pod"];
	        this.container = source["container"];
	        this.path = source["path"];
	        this.recursive = source["recursive"];
	    }
	}
	export class ServerPodFileRenameRequest {
	    profileId: string;
	    pod: string;
	    container?: string;
	    path: string;
	    destination: string;
	
	    static createFrom(source: any = {}) {
	        return new ServerPodFileRenameRequest(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.profileId = source["profileId"];
	        this.pod = source["pod"];
	        this.container = source["container"];
	        this.path = source["path"];
	        this.destination = source["destination"];
	    }
	}
	export class ServerPodFileTarget {
	    profileId: string;
	    pod: string;
	    container?: string;
	    path: string;
	
	    static createFrom(source: any = {}) {
	        return new ServerPodFileTarget(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.profileId = source["profileId"];
	        this.pod = source["pod"];
	        this.container = source["container"];
	        this.path = source["path"];
	    }
	}
	export class ServerPodSSHRequest {
	    profileId: string;
	    pod: string;
	    container?: string;
	
	    static createFrom(source: any = {}) {
	        return new ServerPodSSHRequest(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.profileId = source["profileId"];
	        this.pod = source["pod"];
	        this.container = source["container"];
	    }
	}
	export class ServerProfileResult {
	    profile: profile.Profile;
	    discovery: discovery.Document;
	
	    static createFrom(source: any = {}) {
	        return new ServerProfileResult(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.profile = this.convertValues(source["profile"], profile.Profile);
	        this.discovery = this.convertValues(source["discovery"], discovery.Document);
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
	export class TrafficInspectionQuery {
	    host: string;
	    path: string;
	    limit: number;
	
	    static createFrom(source: any = {}) {
	        return new TrafficInspectionQuery(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.host = source["host"];
	        this.path = source["path"];
	        this.limit = source["limit"];
	    }
	}
	export class TrafficInspectionResult {
	    enabled: boolean;
	    events: trafficinspect.Event[];
	
	    static createFrom(source: any = {}) {
	        return new TrafficInspectionResult(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.enabled = source["enabled"];
	        this.events = this.convertValues(source["events"], trafficinspect.Event);
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
	export class TrafficInspectionSettings {
	    enabled: boolean;
	    protobufFiles: string[];
	
	    static createFrom(source: any = {}) {
	        return new TrafficInspectionSettings(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.enabled = source["enabled"];
	        this.protobufFiles = source["protobufFiles"];
	    }
	}

}

export namespace capability {
	
	export class Snapshot {
	    schemaVersion: number;
	    identityId: string;
	    namespace: string;
	    gatewayVersion: string;
	    capabilities: string[];
	
	    static createFrom(source: any = {}) {
	        return new Snapshot(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.schemaVersion = source["schemaVersion"];
	        this.identityId = source["identityId"];
	        this.namespace = source["namespace"];
	        this.gatewayVersion = source["gatewayVersion"];
	        this.capabilities = source["capabilities"];
	    }
	}

}

export namespace dataplane {
	
	export class Status {
	    state: string;
	    mode: string;
	    sessionId: string;
	    sessionGeneration: number;
	    socksAddress: string;
	    networkSpecHash: string;
	
	    static createFrom(source: any = {}) {
	        return new Status(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.state = source["state"];
	        this.mode = source["mode"];
	        this.sessionId = source["sessionId"];
	        this.sessionGeneration = source["sessionGeneration"];
	        this.socksAddress = source["socksAddress"];
	        this.networkSpecHash = source["networkSpecHash"];
	    }
	}

}

export namespace discovery {
	
	export class AuthMethod {
	    id: string;
	    type: string;
	    displayName?: string;
	    interaction: string;
	
	    static createFrom(source: any = {}) {
	        return new AuthMethod(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.type = source["type"];
	        this.displayName = source["displayName"];
	        this.interaction = source["interaction"];
	    }
	}
	export class Document {
	    serviceId: string;
	    publicUrl: string;
	    tunnelPath: string;
	    apiVersions: string[];
	    authMethods: AuthMethod[];
	    features: string[];
	    serverVersion: string;
	    serverCommit?: string;
	    protocolMin: string;
	    protocolMax: string;
	    minClientVersion?: string;
	
	    static createFrom(source: any = {}) {
	        return new Document(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.serviceId = source["serviceId"];
	        this.publicUrl = source["publicUrl"];
	        this.tunnelPath = source["tunnelPath"];
	        this.apiVersions = source["apiVersions"];
	        this.authMethods = this.convertValues(source["authMethods"], AuthMethod);
	        this.features = source["features"];
	        this.serverVersion = source["serverVersion"];
	        this.serverCommit = source["serverCommit"];
	        this.protocolMin = source["protocolMin"];
	        this.protocolMax = source["protocolMax"];
	        this.minClientVersion = source["minClientVersion"];
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

export namespace exchange {
	
	export class Info {
	    id: string;
	    profileId: string;
	    sessionId: string;
	    namespace: string;
	    service: string;
	    clusterIp: string;
	    state: string;
	    targets: reverserelay.Target[];
	
	    static createFrom(source: any = {}) {
	        return new Info(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.profileId = source["profileId"];
	        this.sessionId = source["sessionId"];
	        this.namespace = source["namespace"];
	        this.service = source["service"];
	        this.clusterIp = source["clusterIp"];
	        this.state = source["state"];
	        this.targets = this.convertValues(source["targets"], reverserelay.Target);
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
	export class Request {
	    profileId: string;
	    service: string;
	    targets: reverserelay.Target[];
	
	    static createFrom(source: any = {}) {
	        return new Request(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.profileId = source["profileId"];
	        this.service = source["service"];
	        this.targets = this.convertValues(source["targets"], reverserelay.Target);
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

export namespace filetransfer {
	
	export class Request {
	    profileId: string;
	    direction: string;
	    kind: string;
	    pod: string;
	    container?: string;
	    localPath: string;
	    remotePath: string;
	    overwrite?: boolean;
	
	    static createFrom(source: any = {}) {
	        return new Request(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.profileId = source["profileId"];
	        this.direction = source["direction"];
	        this.kind = source["kind"];
	        this.pod = source["pod"];
	        this.container = source["container"];
	        this.localPath = source["localPath"];
	        this.remotePath = source["remotePath"];
	        this.overwrite = source["overwrite"];
	    }
	}
	export class Task {
	    id: string;
	    profileId: string;
	    sessionId: string;
	    namespace: string;
	    direction: string;
	    kind: string;
	    pod: string;
	    container?: string;
	    localPath: string;
	    remotePath: string;
	    overwrite?: boolean;
	    status: string;
	    totalBytes?: number;
	    doneBytes?: number;
	    checksum?: string;
	    resumeId?: string;
	    temporaryPath?: string;
	    error?: string;
	    createdAt: string;
	    updatedAt: string;
	    completedAt?: string;
	
	    static createFrom(source: any = {}) {
	        return new Task(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.profileId = source["profileId"];
	        this.sessionId = source["sessionId"];
	        this.namespace = source["namespace"];
	        this.direction = source["direction"];
	        this.kind = source["kind"];
	        this.pod = source["pod"];
	        this.container = source["container"];
	        this.localPath = source["localPath"];
	        this.remotePath = source["remotePath"];
	        this.overwrite = source["overwrite"];
	        this.status = source["status"];
	        this.totalBytes = source["totalBytes"];
	        this.doneBytes = source["doneBytes"];
	        this.checksum = source["checksum"];
	        this.resumeId = source["resumeId"];
	        this.temporaryPath = source["temporaryPath"];
	        this.error = source["error"];
	        this.createdAt = source["createdAt"];
	        this.updatedAt = source["updatedAt"];
	        this.completedAt = source["completedAt"];
	    }
	}

}

export namespace helper {
	
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

export namespace mirror {
	
	export class LocalTarget {
	    servicePort: number;
	    protocol: string;
	    localHost: string;
	    localPort: number;
	
	    static createFrom(source: any = {}) {
	        return new LocalTarget(source);
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
	    profileId: string;
	    sessionId: string;
	    namespace: string;
	    service: string;
	    clusterIp: string;
	    state: string;
	    targets: LocalTarget[];
	
	    static createFrom(source: any = {}) {
	        return new Info(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.profileId = source["profileId"];
	        this.sessionId = source["sessionId"];
	        this.namespace = source["namespace"];
	        this.service = source["service"];
	        this.clusterIp = source["clusterIp"];
	        this.state = source["state"];
	        this.targets = this.convertValues(source["targets"], LocalTarget);
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
	
	export class Request {
	    profileId: string;
	    service: string;
	    targets: LocalTarget[];
	
	    static createFrom(source: any = {}) {
	        return new Request(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.profileId = source["profileId"];
	        this.service = source["service"];
	        this.targets = this.convertValues(source["targets"], LocalTarget);
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

export namespace networkdiag {
	
	export class Issue {
	    code: string;
	    severity: string;
	    message: string;
	    target?: string;
	    conflict?: string;
	    interface?: string;
	
	    static createFrom(source: any = {}) {
	        return new Issue(source);
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
	export class Result {
	    routingMode: string;
	    strictRoute: boolean;
	    issues: Issue[];
	
	    static createFrom(source: any = {}) {
	        return new Result(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.routingMode = source["routingMode"];
	        this.strictRoute = source["strictRoute"];
	        this.issues = this.convertValues(source["issues"], Issue);
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

export namespace networkspec {
	
	export class Spec {
	    version: number;
	    podCIDRs: string[];
	    podIPs: string[];
	    serviceCIDRs: string[];
	    serviceIPs: string[];
	    dnsServer?: string;
	    clusterDomains: string[];
	
	    static createFrom(source: any = {}) {
	        return new Spec(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.version = source["version"];
	        this.podCIDRs = source["podCIDRs"];
	        this.podIPs = source["podIPs"];
	        this.serviceCIDRs = source["serviceCIDRs"];
	        this.serviceIPs = source["serviceIPs"];
	        this.dnsServer = source["dnsServer"];
	        this.clusterDomains = source["clusterDomains"];
	    }
	}

}

export namespace podssh {
	
	export class Info {
	    id: string;
	    profileId: string;
	    sessionId: string;
	    namespace: string;
	    pod: string;
	    container: string;
	    containers: string[];
	    podIp: string;
	    address: string;
	    port: number;
	    command: string;
	    state: string;
	
	    static createFrom(source: any = {}) {
	        return new Info(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.profileId = source["profileId"];
	        this.sessionId = source["sessionId"];
	        this.namespace = source["namespace"];
	        this.pod = source["pod"];
	        this.container = source["container"];
	        this.containers = source["containers"];
	        this.podIp = source["podIp"];
	        this.address = source["address"];
	        this.port = source["port"];
	        this.command = source["command"];
	        this.state = source["state"];
	    }
	}

}

export namespace portforward {
	
	export class Info {
	    id: string;
	    profileId: string;
	    sessionId: string;
	    namespace: string;
	    kind: string;
	    name: string;
	    protocol: string;
	    remotePort: number;
	    localPort: number;
	    address: string;
	    dialAddress: string;
	    state: string;
	
	    static createFrom(source: any = {}) {
	        return new Info(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.profileId = source["profileId"];
	        this.sessionId = source["sessionId"];
	        this.namespace = source["namespace"];
	        this.kind = source["kind"];
	        this.name = source["name"];
	        this.protocol = source["protocol"];
	        this.remotePort = source["remotePort"];
	        this.localPort = source["localPort"];
	        this.address = source["address"];
	        this.dialAddress = source["dialAddress"];
	        this.state = source["state"];
	    }
	}
	export class Request {
	    profileId: string;
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
	        this.profileId = source["profileId"];
	        this.kind = source["kind"];
	        this.name = source["name"];
	        this.protocol = source["protocol"];
	        this.remotePort = source["remotePort"];
	        this.localPort = source["localPort"];
	    }
	}

}

export namespace preview {
	
	export class Info {
	    id: string;
	    profileId: string;
	    sessionId: string;
	    namespace: string;
	    name: string;
	    clusterIp: string;
	    state: string;
	    targets: reverserelay.Target[];
	
	    static createFrom(source: any = {}) {
	        return new Info(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.profileId = source["profileId"];
	        this.sessionId = source["sessionId"];
	        this.namespace = source["namespace"];
	        this.name = source["name"];
	        this.clusterIp = source["clusterIp"];
	        this.state = source["state"];
	        this.targets = this.convertValues(source["targets"], reverserelay.Target);
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
	export class Request {
	    profileId: string;
	    namespace: string;
	    name: string;
	    targets: reverserelay.Target[];
	
	    static createFrom(source: any = {}) {
	        return new Request(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.profileId = source["profileId"];
	        this.namespace = source["namespace"];
	        this.name = source["name"];
	        this.targets = this.convertValues(source["targets"], reverserelay.Target);
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

export namespace profile {
	
	export class HostAlias {
	    domain: string;
	    ip: string;
	
	    static createFrom(source: any = {}) {
	        return new HostAlias(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.domain = source["domain"];
	        this.ip = source["ip"];
	    }
	}
	export class Profile {
	    id: string;
	    baseUrl: string;
	    tunnelPath: string;
	    displayName?: string;
	    lastIdentityId?: string;
	    lastUserName?: string;
	    lastNamespace?: string;
	    dnsNamespace?: string;
	    socksPort?: number;
	    hostAliases?: HostAlias[];
	
	    static createFrom(source: any = {}) {
	        return new Profile(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.baseUrl = source["baseUrl"];
	        this.tunnelPath = source["tunnelPath"];
	        this.displayName = source["displayName"];
	        this.lastIdentityId = source["lastIdentityId"];
	        this.lastUserName = source["lastUserName"];
	        this.lastNamespace = source["lastNamespace"];
	        this.dnsNamespace = source["dnsNamespace"];
	        this.socksPort = source["socksPort"];
	        this.hostAliases = this.convertValues(source["hostAliases"], HostAlias);
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
	    version: number;
	    activeProfileId?: string;
	    profiles: Profile[];
	
	    static createFrom(source: any = {}) {
	        return new State(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.version = source["version"];
	        this.activeProfileId = source["activeProfileId"];
	        this.profiles = this.convertValues(source["profiles"], Profile);
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

export namespace remote {
	
	export class ExecTask {
	    id: string;
	    sessionId: string;
	    namespace: string;
	    state: string;
	    pod: string;
	    container?: string;
	    tty: boolean;
	    createdAt: string;
	    updatedAt: string;
	    expiresAt: string;
	
	    static createFrom(source: any = {}) {
	        return new ExecTask(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.sessionId = source["sessionId"];
	        this.namespace = source["namespace"];
	        this.state = source["state"];
	        this.pod = source["pod"];
	        this.container = source["container"];
	        this.tty = source["tty"];
	        this.createdAt = source["createdAt"];
	        this.updatedAt = source["updatedAt"];
	        this.expiresAt = source["expiresAt"];
	    }
	}
	export class Namespace {
	    name: string;
	    status?: string;
	
	    static createFrom(source: any = {}) {
	        return new Namespace(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.name = source["name"];
	        this.status = source["status"];
	    }
	}
	export class PodPort {
	    name?: string;
	    port: number;
	    protocol: string;
	
	    static createFrom(source: any = {}) {
	        return new PodPort(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.name = source["name"];
	        this.port = source["port"];
	        this.protocol = source["protocol"];
	    }
	}
	export class Pod {
	    name: string;
	    namespace: string;
	    phase?: string;
	    podIp?: string;
	    nodeName?: string;
	    ready: boolean;
	    readyContainers: number;
	    totalContainers: number;
	    restarts: number;
	    ageSeconds: number;
	    containers: string[];
	    ports: PodPort[];
	
	    static createFrom(source: any = {}) {
	        return new Pod(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.name = source["name"];
	        this.namespace = source["namespace"];
	        this.phase = source["phase"];
	        this.podIp = source["podIp"];
	        this.nodeName = source["nodeName"];
	        this.ready = source["ready"];
	        this.readyContainers = source["readyContainers"];
	        this.totalContainers = source["totalContainers"];
	        this.restarts = source["restarts"];
	        this.ageSeconds = source["ageSeconds"];
	        this.containers = source["containers"];
	        this.ports = this.convertValues(source["ports"], PodPort);
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
	export class PodFileEntry {
	    name: string;
	    path: string;
	    kind: string;
	    size: number;
	    mode: string;
	    modifiedAt: string;
	
	    static createFrom(source: any = {}) {
	        return new PodFileEntry(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.name = source["name"];
	        this.path = source["path"];
	        this.kind = source["kind"];
	        this.size = source["size"];
	        this.mode = source["mode"];
	        this.modifiedAt = source["modifiedAt"];
	    }
	}
	export class PodFileList {
	    sessionId: string;
	    namespace: string;
	    pod: string;
	    container: string;
	    path: string;
	    items: PodFileEntry[];
	
	    static createFrom(source: any = {}) {
	        return new PodFileList(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.sessionId = source["sessionId"];
	        this.namespace = source["namespace"];
	        this.pod = source["pod"];
	        this.container = source["container"];
	        this.path = source["path"];
	        this.items = this.convertValues(source["items"], PodFileEntry);
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
	export class PodFileResult {
	    completed: boolean;
	    error?: string;
	
	    static createFrom(source: any = {}) {
	        return new PodFileResult(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.completed = source["completed"];
	        this.error = source["error"];
	    }
	}
	export class PodFileTask {
	    id: string;
	    sessionId: string;
	    namespace: string;
	    state: string;
	    action: string;
	    pod: string;
	    container: string;
	    path: string;
	    destination?: string;
	    kind?: string;
	    recursive?: boolean;
	    result: PodFileResult;
	    createdAt: string;
	    updatedAt: string;
	    expiresAt: string;
	
	    static createFrom(source: any = {}) {
	        return new PodFileTask(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.sessionId = source["sessionId"];
	        this.namespace = source["namespace"];
	        this.state = source["state"];
	        this.action = source["action"];
	        this.pod = source["pod"];
	        this.container = source["container"];
	        this.path = source["path"];
	        this.destination = source["destination"];
	        this.kind = source["kind"];
	        this.recursive = source["recursive"];
	        this.result = this.convertValues(source["result"], PodFileResult);
	        this.createdAt = source["createdAt"];
	        this.updatedAt = source["updatedAt"];
	        this.expiresAt = source["expiresAt"];
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
	
	export class ServicePort {
	    name?: string;
	    port: number;
	    protocol: string;
	    targetPort?: string;
	
	    static createFrom(source: any = {}) {
	        return new ServicePort(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.name = source["name"];
	        this.port = source["port"];
	        this.protocol = source["protocol"];
	        this.targetPort = source["targetPort"];
	    }
	}
	export class Service {
	    name: string;
	    namespace: string;
	    type: string;
	    clusterIp?: string;
	    externalName?: string;
	    externalIps: string[];
	    ageSeconds: number;
	    ports: ServicePort[];
	
	    static createFrom(source: any = {}) {
	        return new Service(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.name = source["name"];
	        this.namespace = source["namespace"];
	        this.type = source["type"];
	        this.clusterIp = source["clusterIp"];
	        this.externalName = source["externalName"];
	        this.externalIps = source["externalIps"];
	        this.ageSeconds = source["ageSeconds"];
	        this.ports = this.convertValues(source["ports"], ServicePort);
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
	
	export class Session {
	    id: string;
	    namespace: string;
	    state: string;
	    generation: number;
	    createdAt: string;
	    updatedAt: string;
	    lastHeartbeatAt: string;
	    expiresAt: string;
	    networkSpec: networkspec.Spec;
	    networkSpecHash: string;
	    capabilities?: capability.Snapshot;
	
	    static createFrom(source: any = {}) {
	        return new Session(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.namespace = source["namespace"];
	        this.state = source["state"];
	        this.generation = source["generation"];
	        this.createdAt = source["createdAt"];
	        this.updatedAt = source["updatedAt"];
	        this.lastHeartbeatAt = source["lastHeartbeatAt"];
	        this.expiresAt = source["expiresAt"];
	        this.networkSpec = this.convertValues(source["networkSpec"], networkspec.Spec);
	        this.networkSpecHash = source["networkSpecHash"];
	        this.capabilities = this.convertValues(source["capabilities"], capability.Snapshot);
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
	export class TrafficBindingPort {
	    name?: string;
	    targetPort: number;
	    relayPort?: number;
	    localHost?: string;
	    localPort?: number;
	    protocol: string;
	
	    static createFrom(source: any = {}) {
	        return new TrafficBindingPort(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.name = source["name"];
	        this.targetPort = source["targetPort"];
	        this.relayPort = source["relayPort"];
	        this.localHost = source["localHost"];
	        this.localPort = source["localPort"];
	        this.protocol = source["protocol"];
	    }
	}
	export class TrafficBindingPreview {
	    serviceName: string;

	    static createFrom(source: any = {}) {
	        return new TrafficBindingPreview(source);
	    }

	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.serviceName = source["serviceName"];
	    }
	}
	export class TrafficBindingRelay {
	    address: string;
	
	    static createFrom(source: any = {}) {
	        return new TrafficBindingRelay(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.address = source["address"];
	    }
	}
	export class TrafficBindingTarget {
	    kind: string;
	    name: string;

	    static createFrom(source: any = {}) {
	        return new TrafficBindingTarget(source);
	    }

	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.kind = source["kind"];
	        this.name = source["name"];
	    }
	}
	export class TrafficBindingSession {
	    id: string;
	    name: string;
	    namespace: string;
	    sessionId: string;
	    mode: string;
	    desiredState: string;
	    phase: string;
	    target?: TrafficBindingTarget;
	    preview?: TrafficBindingPreview;
	    relay?: TrafficBindingRelay;
	    ports: TrafficBindingPort[];
	    serviceName?: string;
	    serviceClusterIp?: string;
	    dialAddress?: string;
	    createdAt: string;

	    static createFrom(source: any = {}) {
	        return new TrafficBindingSession(source);
	    }

	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.name = source["name"];
	        this.namespace = source["namespace"];
	        this.sessionId = source["sessionId"];
	        this.mode = source["mode"];
	        this.desiredState = source["desiredState"];
	        this.phase = source["phase"];
	        this.target = this.convertValues(source["target"], TrafficBindingTarget);
	        this.preview = this.convertValues(source["preview"], TrafficBindingPreview);
	        this.relay = this.convertValues(source["relay"], TrafficBindingRelay);
	        this.ports = this.convertValues(source["ports"], TrafficBindingPort);
	        this.serviceName = source["serviceName"];
	        this.serviceClusterIp = source["serviceClusterIp"];
	        this.dialAddress = source["dialAddress"];
	        this.createdAt = source["createdAt"];
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

export namespace reverserelay {

	export class Target {
	    servicePort: number;
	    protocol: string;
	    localHost: string;
	    localPort: number;

	    static createFrom(source: any = {}) {
	        return new Target(source);
	    }

	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.servicePort = source["servicePort"];
	        this.protocol = source["protocol"];
	        this.localHost = source["localHost"];
	        this.localPort = source["localPort"];
	    }
	}

}
export namespace trafficinspect {
	
	export class RawEvent {
	    format: string;
	    direction: string;
	    encoding: string;
	    data: string;
	    size: number;
	    truncated: boolean;
	
	    static createFrom(source: any = {}) {
	        return new RawEvent(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.format = source["format"];
	        this.direction = source["direction"];
	        this.encoding = source["encoding"];
	        this.data = source["data"];
	        this.size = source["size"];
	        this.truncated = source["truncated"];
	    }
	}
	export class ProtobufEvent {
	    format: string;
	    schema: string;
	    message_type?: string;
	    data?: string;
	    error?: string;
	
	    static createFrom(source: any = {}) {
	        return new ProtobufEvent(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.format = source["format"];
	        this.schema = source["schema"];
	        this.message_type = source["message_type"];
	        this.data = source["data"];
	        this.error = source["error"];
	    }
	}
	export class GRPCEvent {
	    service?: string;
	    method?: string;
	    path?: string;
	    status?: string;
	
	    static createFrom(source: any = {}) {
	        return new GRPCEvent(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.service = source["service"];
	        this.method = source["method"];
	        this.path = source["path"];
	        this.status = source["status"];
	    }
	}
	export class HTTPEvent {
	    version: string;
	    method?: string;
	    host?: string;
	    path?: string;
	    status?: number;
	    request_headers?: Record<string, Array<string>>;
	    response_headers?: Record<string, Array<string>>;
	
	    static createFrom(source: any = {}) {
	        return new HTTPEvent(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.version = source["version"];
	        this.method = source["method"];
	        this.host = source["host"];
	        this.path = source["path"];
	        this.status = source["status"];
	        this.request_headers = source["request_headers"];
	        this.response_headers = source["response_headers"];
	    }
	}
	export class Event {
	    schema_version: number;
	    event_id: string;
	    flow_id: string;
	    // Go type: time
	    timestamp: any;
	    type: string;
	    protocol: string;
	    tls: boolean;
	    destination: string;
	    duration_ms?: number;
	    http?: HTTPEvent;
	    grpc?: GRPCEvent;
	    protobuf?: ProtobufEvent;
	    raw?: RawEvent;
	
	    static createFrom(source: any = {}) {
	        return new Event(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.schema_version = source["schema_version"];
	        this.event_id = source["event_id"];
	        this.flow_id = source["flow_id"];
	        this.timestamp = this.convertValues(source["timestamp"], null);
	        this.type = source["type"];
	        this.protocol = source["protocol"];
	        this.tls = source["tls"];
	        this.destination = source["destination"];
	        this.duration_ms = source["duration_ms"];
	        this.http = this.convertValues(source["http"], HTTPEvent);
	        this.grpc = this.convertValues(source["grpc"], GRPCEvent);
	        this.protobuf = this.convertValues(source["protobuf"], ProtobufEvent);
	        this.raw = this.convertValues(source["raw"], RawEvent);
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

export namespace update {
	
	export class Info {
	    currentVersion: string;
	    latestVersion?: string;
	    available: boolean;
	    url: string;
	    publishedAt: string;
	    checkedAt: string;
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
	        this.publishedAt = source["publishedAt"];
	        this.checkedAt = source["checkedAt"];
	        this.error = source["error"];
	    }
	}

}
