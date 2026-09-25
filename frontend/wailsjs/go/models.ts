export namespace config {
	
	export class Channel {
	    id: string;
	    name: string;
	    type: string;
	    baseUrl: string;
	    apiKey?: string;
	    extra?: number[];
	    models: string[];
	    modelMapping?: Record<string, string>;
	    priority: number;
	    enabled: boolean;
	
	    static createFrom(source: any = {}) {
	        return new Channel(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.name = source["name"];
	        this.type = source["type"];
	        this.baseUrl = source["baseUrl"];
	        this.apiKey = source["apiKey"];
	        this.extra = source["extra"];
	        this.models = source["models"];
	        this.modelMapping = source["modelMapping"];
	        this.priority = source["priority"];
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

}

export namespace service {
	
	export class ChannelView {
	    id: string;
	    name: string;
	    type: string;
	    baseUrl: string;
	    apiKeyHint: string;
	    hasExtra: boolean;
	    models: string[];
	    modelMapping: Record<string, string>;
	    priority: number;
	    enabled: boolean;
	    status: string;
	    coolingUntil?: string;
	    failReason?: string;
	    todayTokens: number;
	
	    static createFrom(source: any = {}) {
	        return new ChannelView(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.name = source["name"];
	        this.type = source["type"];
	        this.baseUrl = source["baseUrl"];
	        this.apiKeyHint = source["apiKeyHint"];
	        this.hasExtra = source["hasExtra"];
	        this.models = source["models"];
	        this.modelMapping = source["modelMapping"];
	        this.priority = source["priority"];
	        this.enabled = source["enabled"];
	        this.status = source["status"];
	        this.coolingUntil = source["coolingUntil"];
	        this.failReason = source["failReason"];
	        this.todayTokens = source["todayTokens"];
	    }
	}
	export class PresetView {
	    key: string;
	    displayName: string;
	    type: string;
	    baseUrl: string;
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
	        this.models = source["models"];
	        this.mapping = source["mapping"];
	        this.docsUrl = source["docsUrl"];
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

