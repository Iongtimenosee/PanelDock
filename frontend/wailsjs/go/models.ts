export namespace main {
	
	export class AppSettings {
	    showTrayIcon: boolean;
	    theme?: string;
	    language?: string;
	    panelCloseAction: string;
	    lightweightQuitOnLastPanel: boolean;
	    closeAction?: string;
	    managerCloseAction?: string;
	
	    static createFrom(source: any = {}) {
	        return new AppSettings(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.showTrayIcon = source["showTrayIcon"];
	        this.theme = source["theme"];
	        this.language = source["language"];
	        this.panelCloseAction = source["panelCloseAction"];
	        this.lightweightQuitOnLastPanel = source["lightweightQuitOnLastPanel"];
	        this.closeAction = source["closeAction"];
	        this.managerCloseAction = source["managerCloseAction"];
	    }
	}
	export class PanelWindowState {
	    x: number;
	    y: number;
	    width: number;
	    height: number;
	    maximized?: boolean;
	
	    static createFrom(source: any = {}) {
	        return new PanelWindowState(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.x = source["x"];
	        this.y = source["y"];
	        this.width = source["width"];
	        this.height = source["height"];
	        this.maximized = source["maximized"];
	    }
	}
	export class PanelTab {
	    id: string;
	    name: string;
	    url: string;
	
	    static createFrom(source: any = {}) {
	        return new PanelTab(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.name = source["name"];
	        this.url = source["url"];
	    }
	}
	export class PanelConfig {
	    id: string;
	    name: string;
	    tabs?: PanelTab[];
	    enabled: boolean;
	    alwaysOnTop: boolean;
	    window: PanelWindowState;
	    defaultTabIndex?: number;
	    minimizeToTray?: boolean;
	    sessionMode?: string;
	    passwordAutosave?: string;
	    shortcut?: string;
	    shortcuts?: string[];
	    url?: string;
	
	    static createFrom(source: any = {}) {
	        return new PanelConfig(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.name = source["name"];
	        this.tabs = this.convertValues(source["tabs"], PanelTab);
	        this.enabled = source["enabled"];
	        this.alwaysOnTop = source["alwaysOnTop"];
	        this.window = this.convertValues(source["window"], PanelWindowState);
	        this.defaultTabIndex = source["defaultTabIndex"];
	        this.minimizeToTray = source["minimizeToTray"];
	        this.sessionMode = source["sessionMode"];
	        this.passwordAutosave = source["passwordAutosave"];
	        this.shortcut = source["shortcut"];
	        this.shortcuts = source["shortcuts"];
	        this.url = source["url"];
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
	export class PanelIconApplyResult {
	    iconPath: string;
	    updated: string[];
	    failed: string[];
	    createdShortcut: boolean;
	    removed: string[];
	
	    static createFrom(source: any = {}) {
	        return new PanelIconApplyResult(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.iconPath = source["iconPath"];
	        this.updated = source["updated"];
	        this.failed = source["failed"];
	        this.createdShortcut = source["createdShortcut"];
	        this.removed = source["removed"];
	    }
	}
	export class PanelIconPreview {
	    available: boolean;
	    reason: string;
	    panelName: string;
	    tabName: string;
	    pageURL: string;
	    host: string;
	    sizes: number[];
	    preview: string;
	    shortcuts: string[];
	    iconPath: string;
	    alreadyCached: boolean;
	    shortcutPath: string;
	    willCreateShortcut: boolean;
	    duplicates: string[];
	
	    static createFrom(source: any = {}) {
	        return new PanelIconPreview(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.available = source["available"];
	        this.reason = source["reason"];
	        this.panelName = source["panelName"];
	        this.tabName = source["tabName"];
	        this.pageURL = source["pageURL"];
	        this.host = source["host"];
	        this.sizes = source["sizes"];
	        this.preview = source["preview"];
	        this.shortcuts = source["shortcuts"];
	        this.iconPath = source["iconPath"];
	        this.alreadyCached = source["alreadyCached"];
	        this.shortcutPath = source["shortcutPath"];
	        this.willCreateShortcut = source["willCreateShortcut"];
	        this.duplicates = source["duplicates"];
	    }
	}
	export class PanelShortcutPreview {
	    exists: boolean;
	    shortcuts: string[];
	    extra: string[];
	    iconPath: string;
	    targetPath: string;
	
	    static createFrom(source: any = {}) {
	        return new PanelShortcutPreview(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.exists = source["exists"];
	        this.shortcuts = source["shortcuts"];
	        this.extra = source["extra"];
	        this.iconPath = source["iconPath"];
	        this.targetPath = source["targetPath"];
	    }
	}
	export class PanelShortcutResult {
	    path: string;
	    created: boolean;
	    renamed: boolean;
	    removed: string[];
	
	    static createFrom(source: any = {}) {
	        return new PanelShortcutResult(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.path = source["path"];
	        this.created = source["created"];
	        this.renamed = source["renamed"];
	        this.removed = source["removed"];
	    }
	}
	
	
	export class PinTaskbarResult {
	    shortcut: string;
	    alreadyPinned: boolean;
	
	    static createFrom(source: any = {}) {
	        return new PinTaskbarResult(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.shortcut = source["shortcut"];
	        this.alreadyPinned = source["alreadyPinned"];
	    }
	}

}

