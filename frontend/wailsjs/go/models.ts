export namespace config {
	
	export class ChannelTarget {
	    providerId: string;
	    upstreamModel: string;
	    priority: number;
	    weight?: number;
	    enabled: boolean;
	
	    static createFrom(source: any = {}) {
	        return new ChannelTarget(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.providerId = source["providerId"];
	        this.upstreamModel = source["upstreamModel"];
	        this.priority = source["priority"];
	        this.weight = source["weight"];
	        this.enabled = source["enabled"];
	    }
	}
	export class Channel {
	    id: string;
	    name: string;
	    model: string;
	    strategy: string;
	    targets: ChannelTarget[];
	    enabled: boolean;
	    type?: string;
	    baseUrl?: string;
	    apiKey?: string;
	    extra?: number[];
	    models?: string[];
	    modelMapping?: Record<string, string>;
	    priority?: number;
	
	    static createFrom(source: any = {}) {
	        return new Channel(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.name = source["name"];
	        this.model = source["model"];
	        this.strategy = source["strategy"];
	        this.targets = this.convertValues(source["targets"], ChannelTarget);
	        this.enabled = source["enabled"];
	        this.type = source["type"];
	        this.baseUrl = source["baseUrl"];
	        this.apiKey = source["apiKey"];
	        this.extra = source["extra"];
	        this.models = source["models"];
	        this.modelMapping = source["modelMapping"];
	        this.priority = source["priority"];
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
	
	export class ProviderAccount {
	    id: string;
	    name: string;
	    vendor?: string;
	    type: string;
	    baseUrl: string;
	    apiKey?: string;
	    extra?: number[];
	    authMode?: string;
	    balanceKind?: string;
	    balanceUrl?: string;
	    balanceKey?: string;
	    enabled: boolean;
	
	    static createFrom(source: any = {}) {
	        return new ProviderAccount(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.name = source["name"];
	        this.vendor = source["vendor"];
	        this.type = source["type"];
	        this.baseUrl = source["baseUrl"];
	        this.apiKey = source["apiKey"];
	        this.extra = source["extra"];
	        this.authMode = source["authMode"];
	        this.balanceKind = source["balanceKind"];
	        this.balanceUrl = source["balanceUrl"];
	        this.balanceKey = source["balanceKey"];
	        this.enabled = source["enabled"];
	    }
	}

}

export namespace provider {
	
	export class DeviceAuthInfo {
	    deviceCode: string;
	    userCode: string;
	    verificationUri: string;
	    verificationUriComplete?: string;
	    expiresInSeconds: number;
	    intervalSeconds: number;
	
	    static createFrom(source: any = {}) {
	        return new DeviceAuthInfo(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.deviceCode = source["deviceCode"];
	        this.userCode = source["userCode"];
	        this.verificationUri = source["verificationUri"];
	        this.verificationUriComplete = source["verificationUriComplete"];
	        this.expiresInSeconds = source["expiresInSeconds"];
	        this.intervalSeconds = source["intervalSeconds"];
	    }
	}
	export class ModelInfo {
	    id: string;
	
	    static createFrom(source: any = {}) {
	        return new ModelInfo(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	    }
	}

}

export namespace service {
	
	export class BalanceMetric {
	    label: string;
	    value: string;
	    percent?: number;
	    resetAt?: string;
	
	    static createFrom(source: any = {}) {
	        return new BalanceMetric(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.label = source["label"];
	        this.value = source["value"];
	        this.percent = source["percent"];
	        this.resetAt = source["resetAt"];
	    }
	}
	export class BalanceView {
	    supported: boolean;
	    kind: string;
	    summary: string;
	    details: BalanceMetric[];
	    checkedAt: string;
	
	    static createFrom(source: any = {}) {
	        return new BalanceView(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.supported = source["supported"];
	        this.kind = source["kind"];
	        this.summary = source["summary"];
	        this.details = this.convertValues(source["details"], BalanceMetric);
	        this.checkedAt = source["checkedAt"];
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
	export class ChannelView {
	    id: string;
	    name: string;
	    model: string;
	    strategy: string;
	    targets: config.ChannelTarget[];
	    enabled: boolean;
	    healthy: number;
	    total: number;
	
	    static createFrom(source: any = {}) {
	        return new ChannelView(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.name = source["name"];
	        this.model = source["model"];
	        this.strategy = source["strategy"];
	        this.targets = this.convertValues(source["targets"], config.ChannelTarget);
	        this.enabled = source["enabled"];
	        this.healthy = source["healthy"];
	        this.total = source["total"];
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
	export class PresetView {
	    key: string;
	    displayName: string;
	    type: string;
	    baseUrl: string;
	    vendor: string;
	    balanceKind?: string;
	    balanceUrl?: string;
	    models: string[];
	    mapping?: Record<string, string>;
	    docsUrl?: string;
	
	    static createFrom(source: any = {}) {
	        return new PresetView(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.key = source["key"];
	        this.displayName = source["displayName"];
	        this.type = source["type"];
	        this.baseUrl = source["baseUrl"];
	        this.vendor = source["vendor"];
	        this.balanceKind = source["balanceKind"];
	        this.balanceUrl = source["balanceUrl"];
	        this.models = source["models"];
	        this.mapping = source["mapping"];
	        this.docsUrl = source["docsUrl"];
	    }
	}
	export class ProviderKeysView {
	    apiKey: string;
	    balanceKey: string;
	
	    static createFrom(source: any = {}) {
	        return new ProviderKeysView(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.apiKey = source["apiKey"];
	        this.balanceKey = source["balanceKey"];
	    }
	}
	export class ProviderView {
	    id: string;
	    name: string;
	    vendor: string;
	    type: string;
	    baseUrl: string;
	    apiKeyHint: string;
	    hasExtra: boolean;
	    authMode?: string;
	    balanceKind: string;
	    balanceUrl: string;
	    balanceKeyHint: string;
	    enabled: boolean;
	    status: string;
	    coolingUntil?: string;
	    failReason?: string;
	    todayTokens: number;
	    weekTokens: number;
	    monthTokens: number;
	
	    static createFrom(source: any = {}) {
	        return new ProviderView(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.name = source["name"];
	        this.vendor = source["vendor"];
	        this.type = source["type"];
	        this.baseUrl = source["baseUrl"];
	        this.apiKeyHint = source["apiKeyHint"];
	        this.hasExtra = source["hasExtra"];
	        this.authMode = source["authMode"];
	        this.balanceKind = source["balanceKind"];
	        this.balanceUrl = source["balanceUrl"];
	        this.balanceKeyHint = source["balanceKeyHint"];
	        this.enabled = source["enabled"];
	        this.status = source["status"];
	        this.coolingUntil = source["coolingUntil"];
	        this.failReason = source["failReason"];
	        this.todayTokens = source["todayTokens"];
	        this.weekTokens = source["weekTokens"];
	        this.monthTokens = source["monthTokens"];
	    }
	}
	export class SettingsView {
	    listenHost: string;
	    listenPort: number;
	    localKey: string;
	    retainDays: number;
	    running: boolean;
	
	    static createFrom(source: any = {}) {
	        return new SettingsView(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.listenHost = source["listenHost"];
	        this.listenPort = source["listenPort"];
	        this.localKey = source["localKey"];
	        this.retainDays = source["retainDays"];
	        this.running = source["running"];
	    }
	}

}

export namespace usage {
	
	export class AggRow {
	    channelId: string;
	    channelName: string;
	    modelRequested: string;
	    modelUpstream: string;
	    requests: number;
	    inputTokens: number;
	    outputTokens: number;
	    cacheReadTokens: number;
	    cacheWriteTokens: number;
	    errors: number;
	
	    static createFrom(source: any = {}) {
	        return new AggRow(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.channelId = source["channelId"];
	        this.channelName = source["channelName"];
	        this.modelRequested = source["modelRequested"];
	        this.modelUpstream = source["modelUpstream"];
	        this.requests = source["requests"];
	        this.inputTokens = source["inputTokens"];
	        this.outputTokens = source["outputTokens"];
	        this.cacheReadTokens = source["cacheReadTokens"];
	        this.cacheWriteTokens = source["cacheWriteTokens"];
	        this.errors = source["errors"];
	    }
	}

}

