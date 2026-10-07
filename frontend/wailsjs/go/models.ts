export namespace main {
	
	export class ImportPreview {
	    issuer: string;
	    account: string;
	    uri: string;
	
	    static createFrom(source: any = {}) {
	        return new ImportPreview(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.issuer = source["issuer"];
	        this.account = source["account"];
	        this.uri = source["uri"];
	    }
	}
	export class SessionState {
	    sidebarCollapsed: boolean;
	    filter: string;
	    search: string;
	
	    static createFrom(source: any = {}) {
	        return new SessionState(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.sidebarCollapsed = source["sidebarCollapsed"];
	        this.filter = source["filter"];
	        this.search = source["search"];
	    }
	}
	export class Settings {
	    theme: string;
	    accentColor: string;
	    closeToTray: boolean;
	    backgroundType: string;
	    pattern: string;
	    backgroundUrl: string;
	    backgroundName: string;
	    opacity: number;
	    motion: boolean;
	    checkUpdatesAutomatically: boolean;
	    updateAutomatically: boolean;
	
	    static createFrom(source: any = {}) {
	        return new Settings(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.theme = source["theme"];
	        this.accentColor = source["accentColor"];
	        this.closeToTray = source["closeToTray"];
	        this.backgroundType = source["backgroundType"];
	        this.pattern = source["pattern"];
	        this.backgroundUrl = source["backgroundUrl"];
	        this.backgroundName = source["backgroundName"];
	        this.opacity = source["opacity"];
	        this.motion = source["motion"];
	        this.checkUpdatesAutomatically = source["checkUpdatesAutomatically"];
	        this.updateAutomatically = source["updateAutomatically"];
	    }
	}
	export class State {
	    tokens: vault.Token[];
	    settings: Settings;
	    dataPath: string;
	    warning?: string;
	    session: SessionState;
	
	    static createFrom(source: any = {}) {
	        return new State(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.tokens = this.convertValues(source["tokens"], vault.Token);
	        this.settings = this.convertValues(source["settings"], Settings);
	        this.dataPath = source["dataPath"];
	        this.warning = source["warning"];
	        this.session = this.convertValues(source["session"], SessionState);
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
	export class UpdateStatus {
	    currentVersion: string;
	    latestVersion: string;
	    phase: string;
	    progress: number;
	    releaseUrl: string;
	    message: string;
	    lastChecked: string;
	    downloadedBytes: number;
	    totalBytes: number;
	
	    static createFrom(source: any = {}) {
	        return new UpdateStatus(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.currentVersion = source["currentVersion"];
	        this.latestVersion = source["latestVersion"];
	        this.phase = source["phase"];
	        this.progress = source["progress"];
	        this.releaseUrl = source["releaseUrl"];
	        this.message = source["message"];
	        this.lastChecked = source["lastChecked"];
	        this.downloadedBytes = source["downloadedBytes"];
	        this.totalBytes = source["totalBytes"];
	    }
	}

}

export namespace vault {
	
	export class Token {
	    id: string;
	    issuer: string;
	    account: string;
	    group: string;
	    favorite: boolean;
	    color: string;
	    algorithm: string;
	    digits: number;
	    period: number;
	    code: string;
	    remaining: number;
	
	    static createFrom(source: any = {}) {
	        return new Token(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.issuer = source["issuer"];
	        this.account = source["account"];
	        this.group = source["group"];
	        this.favorite = source["favorite"];
	        this.color = source["color"];
	        this.algorithm = source["algorithm"];
	        this.digits = source["digits"];
	        this.period = source["period"];
	        this.code = source["code"];
	        this.remaining = source["remaining"];
	    }
	}
	export class TokenInput {
	    issuer: string;
	    account: string;
	    secret: string;
	    group: string;
	    color: string;
	    algorithm: string;
	    digits: number;
	    period: number;
	
	    static createFrom(source: any = {}) {
	        return new TokenInput(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.issuer = source["issuer"];
	        this.account = source["account"];
	        this.secret = source["secret"];
	        this.group = source["group"];
	        this.color = source["color"];
	        this.algorithm = source["algorithm"];
	        this.digits = source["digits"];
	        this.period = source["period"];
	    }
	}

}

