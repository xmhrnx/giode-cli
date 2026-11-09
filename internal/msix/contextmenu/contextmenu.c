// The Explorer context menu handler of giode's MSIX packages.
//
// A packaged app's registry writes are its own, so the menu entries
// giode.App.InstallContextActions writes don't reach Explorer: the
// package declares them instead (windows.fileExplorerContextMenus), each
// a COM class implementing IExplorerCommand in this DLL, which Windows
// loads in a surrogate process. Explorer asks it for the entry's title
// and runs Invoke with every selected item; it starts the app once with
// them, as giode's context actions do:
//
//	App.exe --giode-action=<id> <path> <path>...
//
// The actions are in GiodeContextMenu.txt beside the DLL, UTF-8, written
// by the giode CLI: the app's executable on the first line, then one
// action per line, tab-separated: {CLSID}, action id, title.
//
// It's built freestanding, without a C runtime: build.sh.

#define WIN32_LEAN_AND_MEAN
#define COBJMACROS
#include <windows.h>
#include <shobjidl.h>

// The interfaces it implements, here as it doesn't link libuuid.
static const GUID iidUnknown = {0x00000000, 0x0000, 0x0000, {0xC0, 0, 0, 0, 0, 0, 0, 0x46}};
static const GUID iidClassFactory = {0x00000001, 0x0000, 0x0000, {0xC0, 0, 0, 0, 0, 0, 0, 0x46}};
static const GUID iidExplorerCommand = {0xa08ce4d0, 0xfa25, 0x44ab, {0xb5, 0x7c, 0xc7, 0xb1, 0xc3, 0x23, 0xe0, 0xb9}};

// The compiler may call these for struct copies and zeroing.
void *memset(void *d, int c, size_t n) {
	unsigned char *p = d;
	while (n--) *p++ = (unsigned char)c;
	return d;
}

void *memcpy(void *d, const void *s, size_t n) {
	unsigned char *p = d;
	const unsigned char *q = s;
	while (n--) *p++ = *q++;
	return d;
}

static int sameGUID(const GUID *a, const GUID *b) {
	const unsigned char *p = (const unsigned char *)a, *q = (const unsigned char *)b;
	for (int i = 0; i < (int)sizeof(GUID); i++) {
		if (p[i] != q[i]) return 0;
	}
	return 1;
}

static int wlen(const WCHAR *s) {
	int n = 0;
	while (s[n]) n++;
	return n;
}

static void *alloc(SIZE_T n) { return HeapAlloc(GetProcessHeap(), HEAP_ZERO_MEMORY, n); }
static void release(void *p) { if (p) HeapFree(GetProcessHeap(), 0, p); }

// The actions, read once.
typedef struct {
	GUID clsid;
	WCHAR *id, *title;
} action;

static HMODULE module;
static volatile LONG locks;
static WCHAR dir[MAX_PATH];
static WCHAR exe[MAX_PATH];
static action actions[32];
static int nactions;
static volatile LONG loaded;

// hexval parses one hex digit, -1 if it isn't one.
static int hexval(WCHAR c) {
	if (c >= '0' && c <= '9') return c - '0';
	if (c >= 'a' && c <= 'f') return c - 'a' + 10;
	if (c >= 'A' && c <= 'F') return c - 'A' + 10;
	return -1;
}

// parseGUID parses {xxxxxxxx-xxxx-xxxx-xxxx-xxxxxxxxxxxx}.
static int parseGUID(const WCHAR *s, GUID *g) {
	static const int groups[] = {8, 4, 4, 4, 12};
	unsigned char b[16];
	int bi = 0;
	if (*s++ != '{') return 0;
	for (int gi = 0; gi < 5; gi++) {
		for (int i = 0; i < groups[gi]; i += 2) {
			int hi = hexval(s[0]), lo = hexval(s[1]);
			if (hi < 0 || lo < 0) return 0;
			b[bi++] = (unsigned char)(hi << 4 | lo);
			s += 2;
		}
		if (gi < 4 && *s++ != '-') return 0;
	}
	if (*s != '}') return 0;
	g->Data1 = (DWORD)b[0] << 24 | (DWORD)b[1] << 16 | (DWORD)b[2] << 8 | b[3];
	g->Data2 = (WORD)(b[4] << 8 | b[5]);
	g->Data3 = (WORD)(b[6] << 8 | b[7]);
	for (int i = 0; i < 8; i++) g->Data4[i] = b[8 + i];
	return 1;
}

static void load(void) {
	if (InterlockedCompareExchange(&loaded, 1, 0) != 0) return;
	DWORD n = GetModuleFileNameW(module, dir, MAX_PATH);
	if (n == 0 || n >= MAX_PATH) return;
	while (n > 0 && dir[n - 1] != '\\') n--;
	dir[n] = 0;
	WCHAR path[MAX_PATH + 32];
	int i = 0;
	for (; dir[i]; i++) path[i] = dir[i];
	const WCHAR *name = L"GiodeContextMenu.txt";
	for (int j = 0; name[j]; j++) path[i++] = name[j];
	path[i] = 0;
	HANDLE f = CreateFileW(path, GENERIC_READ, FILE_SHARE_READ, NULL, OPEN_EXISTING, 0, NULL);
	if (f == INVALID_HANDLE_VALUE) return;
	DWORD size = GetFileSize(f, NULL), got = 0;
	char *text = alloc(size + 1);
	if (!text || size > 1 << 20 || !ReadFile(f, text, size, &got, NULL)) {
		CloseHandle(f);
		release(text);
		return;
	}
	CloseHandle(f);
	int wn = MultiByteToWideChar(CP_UTF8, 0, text, (int)got, NULL, 0);
	WCHAR *w = alloc((SIZE_T)(wn + 1) * sizeof(WCHAR));
	MultiByteToWideChar(CP_UTF8, 0, text, (int)got, w, wn);
	release(text);
	// Lines, the fields of each split on tabs, in place.
	WCHAR *line = w;
	int first = 1;
	while (*line) {
		WCHAR *end = line;
		while (*end && *end != '\n') end++;
		WCHAR *next = *end ? end + 1 : end;
		if (end > line && end[-1] == '\r') end--;
		*end = 0;
		if (first) {
			int k = 0;
			for (; line[k] && k < MAX_PATH - 1; k++) exe[k] = line[k];
			exe[k] = 0;
			first = 0;
		} else if (nactions < 32) {
			WCHAR *f2 = line;
			while (*f2 && *f2 != '\t') f2++;
			if (*f2) {
				*f2++ = 0;
				WCHAR *f3 = f2;
				while (*f3 && *f3 != '\t') f3++;
				if (*f3) {
					*f3++ = 0;
					action *a = &actions[nactions];
					if (parseGUID(line, &a->clsid)) {
						a->id = f2;
						a->title = f3;
						nactions++;
					}
				}
			}
		}
		line = next;
	}
}

static HRESULT dupString(const WCHAR *s, LPWSTR *out) {
	int n = wlen(s);
	WCHAR *p = CoTaskMemAlloc((SIZE_T)(n + 1) * sizeof(WCHAR));
	if (!p) return E_OUTOFMEMORY;
	for (int i = 0; i <= n; i++) p[i] = s[i];
	*out = p;
	return S_OK;
}

// A command line under construction.
typedef struct {
	WCHAR *s;
	int n, cap;
} buf;

static int put(buf *b, const WCHAR *s, int n) {
	if (b->n + n + 1 > b->cap) {
		int cap = (b->n + n + 1) * 2;
		WCHAR *p = alloc((SIZE_T)cap * sizeof(WCHAR));
		if (!p) return 0;
		for (int i = 0; i < b->n; i++) p[i] = b->s[i];
		release(b->s);
		b->s = p;
		b->cap = cap;
	}
	for (int i = 0; i < n; i++) b->s[b->n++] = s[i];
	b->s[b->n] = 0;
	return 1;
}

// putArg appends s quoted the way CommandLineToArgvW reads it.
static int putArg(buf *b, const WCHAR *s) {
	if (!put(b, L" \"", 2)) return 0;
	int slashes = 0;
	for (const WCHAR *p = s; *p; p++) {
		if (*p == '\\') {
			slashes++;
			continue;
		}
		for (int i = 0; i < slashes * (*p == '"' ? 2 : 1); i++) put(b, L"\\", 1);
		slashes = 0;
		if (*p == '"' && !put(b, L"\\", 1)) return 0;
		if (!put(b, p, 1)) return 0;
	}
	for (int i = 0; i < slashes * 2; i++) put(b, L"\\", 1);
	return put(b, L"\"", 1);
}

// start runs the app with the command line's arguments.
static void start(buf *b) {
	WCHAR app[MAX_PATH * 2];
	int i = 0;
	for (int k = 0; dir[k]; k++) app[i++] = dir[k];
	for (int k = 0; exe[k]; k++) app[i++] = exe[k];
	app[i] = 0;
	STARTUPINFOW si = {sizeof si};
	PROCESS_INFORMATION pi;
	if (CreateProcessW(app, b->s, NULL, NULL, FALSE, 0, NULL, dir, &si, &pi)) {
		CloseHandle(pi.hThread);
		CloseHandle(pi.hProcess);
	}
}

// The command object: IExplorerCommand for one action.
typedef struct {
	IExplorerCommand iface;
	volatile LONG refs;
	action *a;
} command;

static HRESULT STDMETHODCALLTYPE cmdQueryInterface(IExplorerCommand *this, REFIID riid, void **out) {
	if (sameGUID(riid, &iidUnknown) || sameGUID(riid, &iidExplorerCommand)) {
		*out = this;
		IExplorerCommand_AddRef(this);
		return S_OK;
	}
	*out = NULL;
	return E_NOINTERFACE;
}

static ULONG STDMETHODCALLTYPE cmdAddRef(IExplorerCommand *this) {
	return InterlockedIncrement(&((command *)this)->refs);
}

static ULONG STDMETHODCALLTYPE cmdRelease(IExplorerCommand *this) {
	LONG n = InterlockedDecrement(&((command *)this)->refs);
	if (n == 0) {
		release(this);
		InterlockedDecrement(&locks);
	}
	return n;
}

static HRESULT STDMETHODCALLTYPE cmdGetTitle(IExplorerCommand *this, IShellItemArray *items, LPWSTR *name) {
	return dupString(((command *)this)->a->title, name);
}

static HRESULT STDMETHODCALLTYPE cmdGetIcon(IExplorerCommand *this, IShellItemArray *items, LPWSTR *icon) {
	WCHAR path[MAX_PATH * 2 + 4];
	int i = 0;
	for (int k = 0; dir[k]; k++) path[i++] = dir[k];
	for (int k = 0; exe[k]; k++) path[i++] = exe[k];
	path[i++] = ',';
	path[i++] = '0';
	path[i] = 0;
	return dupString(path, icon);
}

static HRESULT STDMETHODCALLTYPE cmdGetToolTip(IExplorerCommand *this, IShellItemArray *items, LPWSTR *tip) {
	*tip = NULL;
	return E_NOTIMPL;
}

static HRESULT STDMETHODCALLTYPE cmdGetCanonicalName(IExplorerCommand *this, GUID *guid) {
	*guid = ((command *)this)->a->clsid;
	return S_OK;
}

static HRESULT STDMETHODCALLTYPE cmdGetState(IExplorerCommand *this, IShellItemArray *items, BOOL slow, EXPCMDSTATE *state) {
	*state = ECS_ENABLED;
	return S_OK;
}

// Command lines are at most 32767 characters: a selection too long for
// one starts the app more than once, each with some of it.
#define maxCommandLine 32000

static HRESULT STDMETHODCALLTYPE cmdInvoke(IExplorerCommand *this, IShellItemArray *items, IBindCtx *bind) {
	action *a = ((command *)this)->a;
	DWORD count = 0;
	if (!items || FAILED(IShellItemArray_GetCount(items, &count))) return E_INVALIDARG;
	buf b = {0};
	buf head = {0};
	put(&head, L"\"", 1);
	put(&head, exe, wlen(exe));
	put(&head, L"\" --giode-action=", 17);
	put(&head, a->id, wlen(a->id));
	int args = 0;
	put(&b, head.s, head.n);
	for (DWORD i = 0; i < count; i++) {
		IShellItem *item;
		if (FAILED(IShellItemArray_GetItemAt(items, i, &item))) continue;
		LPWSTR path = NULL;
		if (SUCCEEDED(IShellItem_GetDisplayName(item, SIGDN_FILESYSPATH, &path))) {
			if (args > 0 && b.n + wlen(path) + 4 > maxCommandLine) {
				start(&b);
				b.n = 0;
				put(&b, head.s, head.n);
				args = 0;
			}
			putArg(&b, path);
			args++;
			CoTaskMemFree(path);
		}
		IShellItem_Release(item);
	}
	if (args > 0) start(&b);
	release(b.s);
	release(head.s);
	return S_OK;
}

static HRESULT STDMETHODCALLTYPE cmdGetFlags(IExplorerCommand *this, EXPCMDFLAGS *flags) {
	*flags = ECF_DEFAULT;
	return S_OK;
}

static HRESULT STDMETHODCALLTYPE cmdEnumSubCommands(IExplorerCommand *this, IEnumExplorerCommand **out) {
	*out = NULL;
	return E_NOTIMPL;
}

static IExplorerCommandVtbl commandVtbl = {
	cmdQueryInterface, cmdAddRef, cmdRelease, cmdGetTitle, cmdGetIcon, cmdGetToolTip,
	cmdGetCanonicalName, cmdGetState, cmdInvoke, cmdGetFlags, cmdEnumSubCommands,
};

// The class factory, one per action, never freed.
typedef struct {
	IClassFactory iface;
	action *a;
} factory;

static HRESULT STDMETHODCALLTYPE factoryQueryInterface(IClassFactory *this, REFIID riid, void **out) {
	if (sameGUID(riid, &iidUnknown) || sameGUID(riid, &iidClassFactory)) {
		*out = this;
		return S_OK;
	}
	*out = NULL;
	return E_NOINTERFACE;
}

static ULONG STDMETHODCALLTYPE factoryAddRef(IClassFactory *this) { return 2; }
static ULONG STDMETHODCALLTYPE factoryRelease(IClassFactory *this) { return 1; }

static HRESULT STDMETHODCALLTYPE factoryCreateInstance(IClassFactory *this, IUnknown *outer, REFIID riid, void **out) {
	*out = NULL;
	if (outer) return CLASS_E_NOAGGREGATION;
	command *c = alloc(sizeof *c);
	if (!c) return E_OUTOFMEMORY;
	c->iface.lpVtbl = &commandVtbl;
	c->refs = 1;
	c->a = ((factory *)this)->a;
	InterlockedIncrement(&locks);
	HRESULT hr = cmdQueryInterface(&c->iface, riid, out);
	cmdRelease(&c->iface);
	return hr;
}

static HRESULT STDMETHODCALLTYPE factoryLockServer(IClassFactory *this, BOOL lock) {
	if (lock) InterlockedIncrement(&locks);
	else InterlockedDecrement(&locks);
	return S_OK;
}

static IClassFactoryVtbl factoryVtbl = {
	factoryQueryInterface, factoryAddRef, factoryRelease, factoryCreateInstance, factoryLockServer,
};

static factory factories[32];

HRESULT WINAPI DllGetClassObject(REFCLSID clsid, REFIID riid, void **out) {
	*out = NULL;
	load();
	for (int i = 0; i < nactions; i++) {
		if (sameGUID(clsid, &actions[i].clsid)) {
			factories[i].iface.lpVtbl = &factoryVtbl;
			factories[i].a = &actions[i];
			return factoryQueryInterface(&factories[i].iface, riid, out);
		}
	}
	return CLASS_E_CLASSNOTAVAILABLE;
}

HRESULT WINAPI DllCanUnloadNow(void) {
	return locks == 0 ? S_OK : S_FALSE;
}

BOOL WINAPI DllMainCRTStartup(HINSTANCE inst, DWORD reason, LPVOID reserved) {
	if (reason == DLL_PROCESS_ATTACH) module = inst;
	return TRUE;
}
