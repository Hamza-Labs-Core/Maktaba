// The window's web caches across app updates.
//
// The window keeps its WebView profile from one release to the next (its
// cookies, and the local storage that holds the reader's choices), and with it
// the service workers the pages registered and the HTTP cache. After every app
// update so far (0.3.17 over 0.3.13, 0.3.19 over 0.3.17) the window stayed
// blank: an older release's worker or cache answered its first load with the
// older release's page, which names a bundle the updated server no longer has.
// Clearing the profile's Service Worker, Cache and Code Cache folders by hand
// mended it each time.
//
// So on the first start of a release the window's service workers, Cache
// Storage and HTTP cache are cleared before it loads anything, in every start
// (opened by hand, the tray's start at sign-in with the window hidden, the
// start an installer's update makes): the window is made on about:blank, and
// its page loads once the clear is over (the loads asked for meanwhile wait:
// Gate). Cookies and local storage stay. The release is recorded beside
// mode.json once its clear succeeded: a later start of it clears nothing, and a
// clear that failed (or never ended: the page loads after CLEAR_WAIT anyway) is
// tried again at the next start.
//
// WebView2 (Windows): the profile's ClearBrowsingData with the service
// workers, Cache Storage and disk cache kinds. WKWebView (macOS): the data
// store's service worker registrations, fetch (Cache Storage), disk and memory
// caches. WebKitGTK (Linux): the website data manager's same four kinds.
use std::path::PathBuf;
use std::sync::{Arc, Mutex};
use std::time::Duration;

use tauri::{AppHandle, Manager, Url, WebviewUrl, WebviewWindow, WebviewWindowBuilder};

use crate::attempts;

/// The window (tauri.conf.json's only one, `create: false`: it is made here).
pub const MAIN: &str = "main";
/// What the window shows while its caches are cleared.
const BLANK: &str = "about:blank";
/// How long the window's page waits for a clear that does not end.
const CLEAR_WAIT: Duration = Duration::from_secs(10);
/// The file beside mode.json that records the release whose pages the window's caches hold.
pub const STAMP_FILE: &str = "webview-version";

/// The release whose pages the window's caches hold, as recorded.
pub struct Stamp(PathBuf);

impl Stamp {
    pub fn new(path: PathBuf) -> Self {
        Self(path)
    }

    pub fn read(&self) -> Option<String> {
        std::fs::read_to_string(&self.0)
            .ok()
            .map(|s| s.trim().to_string())
            .filter(|s| !s.is_empty())
    }

    pub fn write(&self, version: &str) -> Result<(), String> {
        if let Some(dir) = self.0.parent() {
            std::fs::create_dir_all(dir).map_err(|e| format!("{}: {e}", dir.display()))?;
        }
        std::fs::write(&self.0, format!("{version}\n"))
            .map_err(|e| format!("{}: {e}", self.0.display()))
    }
}

/// Whether the window's caches are cleared before it loads: they hold another
/// release's pages, or none is recorded (a first start, or an app from before
/// the record, whose caches are as old as any).
pub fn needs_clear(recorded: Option<&str>, own: &str) -> bool {
    recorded != Some(own)
}

/// What ends a clear: its result.
pub type Done = Box<dyn FnOnce(Result<(), String>) + Send>;

/// A start of the window: whether its caches are cleared first, and the record
/// once they were.
pub struct Start {
    stamp: Stamp,
    own: String,
    /// The window's caches hold another release's pages: cleared before it loads.
    pub clear: bool,
}

impl Start {
    pub fn new(stamp: Stamp, own: &str) -> Self {
        let clear = needs_clear(stamp.read().as_deref(), own);
        Self {
            stamp,
            own: own.to_string(),
            clear,
        }
    }

    /// Clear with `clearer` when a clear is due; `then` follows its end (at once
    /// when none is due). The release is recorded when the clear succeeded.
    pub fn run(self, clearer: impl FnOnce(Done), then: impl FnOnce() + Send + 'static) {
        if !self.clear {
            return then();
        }
        let Self { stamp, own, .. } = self;
        clearer(Box::new(move |res| {
            match res.and_then(|()| stamp.write(&own)) {
                Ok(()) => attempts::log(&format!("the window's caches cleared for {own}")),
                Err(e) => attempts::log(&format!(
                    "the window's caches were not cleared for {own} (its next start tries again): {e}"
                )),
            }
            then();
        }));
    }
}

/// The window's loads while its caches are cleared: held, and the last one
/// asked for loads once the clear is over. The load held while a clear runs;
/// None once it is over (every load then goes at once).
pub struct Gate(Mutex<Option<Url>>);

impl Gate {
    /// Nothing to clear: every load goes at once.
    pub fn open() -> Self {
        Self(Mutex::new(None))
    }

    /// A clear runs: `first` loads when it is over, unless another load is asked first.
    pub fn held(first: Url) -> Self {
        Self(Mutex::new(Some(first)))
    }

    /// A load of `url`: Some(url) to load now; None while the clear runs (it
    /// loads when the clear is over, unless another is asked after it).
    pub fn admit(&self, url: Url) -> Option<Url> {
        let mut held = self.0.lock().unwrap();
        match held.as_mut() {
            Some(next) => {
                *next = url;
                None
            }
            None => Some(url),
        }
    }

    /// The clear is over (or was waited for long enough): the load to make now, once.
    pub fn finish(&self) -> Option<Url> {
        self.0.lock().unwrap().take()
    }
}

/// A load of the window's `url`, through its gate: Some(url) to load now.
pub fn admit(app: &AppHandle, url: Url) -> Option<Url> {
    match app.try_state::<Gate>() {
        Some(gate) => gate.admit(url),
        None => Some(url),
    }
}

/// Make the window from its tauri.conf.json entry: hidden when the app starts
/// in its tray; on about:blank when its caches hold another release's pages,
/// which are cleared before `page` (the bundled web app) loads.
pub fn open_main(
    app: &AppHandle,
    page: &str,
    stamp: Stamp,
    own: &str,
    hidden: bool,
) -> tauri::Result<()> {
    let mut config = app
        .config()
        .app
        .windows
        .iter()
        .find(|w| w.label == MAIN)
        .cloned()
        .ok_or(tauri::Error::WindowNotFound)?;
    let start = Start::new(stamp, own);
    config.visible = config.visible && !hidden;
    let page: Url = page.parse().map_err(|_| tauri::Error::WindowNotFound)?;
    if start.clear {
        config.url = WebviewUrl::External(BLANK.parse().expect("about:blank is a URL"));
        app.manage(Gate::held(page));
    } else {
        app.manage(Gate::open());
    }
    let window = WebviewWindowBuilder::from_config(app, &config)?.build()?;
    if !start.clear {
        return Ok(());
    }
    attempts::log(&format!(
        "the window's caches may hold another release's pages: clearing them before it loads {own}"
    ));
    let handle = app.clone();
    std::thread::spawn(move || {
        std::thread::sleep(CLEAR_WAIT);
        load_held(&handle);
    });
    let handle = app.clone();
    start.run(|done| clear(&window, done), move || load_held(&handle));
    Ok(())
}

/// The load the gate held, once the clear is over.
fn load_held(app: &AppHandle) {
    let Some(url) = app.state::<Gate>().finish() else {
        return;
    };
    if let Some(w) = app.get_webview_window(MAIN) {
        if let Err(e) = w.navigate(url) {
            attempts::log(&format!("the window's page after its clear: {e}"));
        }
    }
}

/// A Done that runs once, whichever of the platform's callback or its failure to
/// start comes.
#[derive(Clone)]
#[cfg_attr(
    not(any(windows, target_os = "macos", target_os = "linux")),
    allow(dead_code)
)]
struct OnceDone(Arc<Mutex<Option<Done>>>);

#[cfg_attr(
    not(any(windows, target_os = "macos", target_os = "linux")),
    allow(dead_code)
)]
impl OnceDone {
    fn new(done: Done) -> Self {
        Self(Arc::new(Mutex::new(Some(done))))
    }

    fn call(&self, res: Result<(), String>) {
        let done = self.0.lock().unwrap().take();
        if let Some(done) = done {
            done(res);
        }
    }
}

/// WebView2: the profile's service workers, Cache Storage and disk cache.
#[cfg(windows)]
fn clear(window: &WebviewWindow, done: Done) {
    use webview2_com::ClearBrowsingDataCompletedHandler;
    use webview2_com::Microsoft::Web::WebView2::Win32::{
        ICoreWebView2Profile2, ICoreWebView2_13, COREWEBVIEW2_BROWSING_DATA_KINDS_CACHE_STORAGE,
        COREWEBVIEW2_BROWSING_DATA_KINDS_DISK_CACHE,
        COREWEBVIEW2_BROWSING_DATA_KINDS_SERVICE_WORKERS,
    };
    use windows_core::Interface;

    let done = OnceDone::new(done);
    let fail = done.clone();
    let res = window.with_webview(move |wv| {
        let kinds = COREWEBVIEW2_BROWSING_DATA_KINDS_SERVICE_WORKERS
            | COREWEBVIEW2_BROWSING_DATA_KINDS_CACHE_STORAGE
            | COREWEBVIEW2_BROWSING_DATA_KINDS_DISK_CACHE;
        let ended = done.clone();
        let handler = ClearBrowsingDataCompletedHandler::create(Box::new(move |res| {
            ended.call(res.map_err(|e| e.to_string()));
            Ok(())
        }));
        // SAFETY: COM calls on the window's live WebView2 controller, on the main
        // thread (with_webview runs this there), as WebView2 requires.
        let started = unsafe {
            wv.controller()
                .CoreWebView2()
                .and_then(|core| core.cast::<ICoreWebView2_13>())
                .and_then(|core| core.Profile())
                .and_then(|profile| profile.cast::<ICoreWebView2Profile2>())
                .and_then(|profile| profile.ClearBrowsingData(kinds, &handler))
        };
        if let Err(e) = started {
            done.call(Err(format!("WebView2: {e}")));
        }
    });
    if let Err(e) = res {
        fail.call(Err(e.to_string()));
    }
}

/// WKWebView: the data store's service worker registrations, Cache Storage, and
/// disk and memory caches.
#[cfg(target_os = "macos")]
fn clear(window: &WebviewWindow, done: Done) {
    use block2::RcBlock;
    use objc2_foundation::{NSDate, NSSet, NSString};
    use objc2_web_kit::{
        WKWebView, WKWebsiteDataTypeDiskCache, WKWebsiteDataTypeFetchCache,
        WKWebsiteDataTypeMemoryCache, WKWebsiteDataTypeServiceWorkerRegistrations,
    };

    let done = OnceDone::new(done);
    let fail = done.clone();
    let res = window.with_webview(move |wv| {
        // SAFETY: inner() is the window's live WKWebView, used on the main thread
        // (with_webview runs this there), as WebKit requires; the data types are
        // WebKit's own constants.
        unsafe {
            let view = &*wv.inner().cast::<WKWebView>();
            let store = view.configuration().websiteDataStore();
            let kinds: [&NSString; 4] = [
                WKWebsiteDataTypeServiceWorkerRegistrations,
                WKWebsiteDataTypeFetchCache,
                WKWebsiteDataTypeDiskCache,
                WKWebsiteDataTypeMemoryCache,
            ];
            let types = NSSet::from_slice(&kinds);
            let since = NSDate::dateWithTimeIntervalSince1970(0.0);
            let ended = RcBlock::new(move || done.call(Ok(())));
            store.removeDataOfTypes_modifiedSince_completionHandler(&types, &since, &ended);
        }
    });
    if let Err(e) = res {
        fail.call(Err(e.to_string()));
    }
}

/// WebKitGTK: the website data manager's service worker registrations, Cache
/// Storage, and disk and memory caches.
#[cfg(target_os = "linux")]
fn clear(window: &WebviewWindow, done: Done) {
    use webkit2gtk::{
        gio::Cancellable, glib::TimeSpan, WebContextExt, WebViewExt, WebsiteDataManagerExtManual,
        WebsiteDataTypes,
    };

    let done = OnceDone::new(done);
    let fail = done.clone();
    let res = window.with_webview(move |wv| {
        let Some(manager) = wv.inner().context().and_then(|c| c.website_data_manager()) else {
            return done.call(Err("the window has no website data manager".into()));
        };
        let kinds = WebsiteDataTypes::SERVICE_WORKER_REGISTRATIONS
            | WebsiteDataTypes::DOM_CACHE
            | WebsiteDataTypes::DISK_CACHE
            | WebsiteDataTypes::MEMORY_CACHE;
        manager.clear(
            kinds,
            TimeSpan::from_seconds(0),
            None::<&Cancellable>,
            move |res| done.call(res.map_err(|e| e.to_string())),
        );
    });
    if let Err(e) = res {
        fail.call(Err(e.to_string()));
    }
}

#[cfg(not(any(windows, target_os = "macos", target_os = "linux")))]
fn clear(_: &WebviewWindow, done: Done) {
    done(Err("no way to clear this platform's web caches".into()));
}

#[cfg(test)]
mod tests {
    use super::*;
    use std::sync::atomic::{AtomicUsize, Ordering};

    /// One start of `own` over the record at `path`, with a stand-in clearer that
    /// answers `answer`: whether it cleared, and whether the window's page came after.
    fn start(
        path: &std::path::Path,
        own: &str,
        answer: Result<(), String>,
        calls: &Arc<AtomicUsize>,
    ) -> (bool, bool) {
        let s = Start::new(Stamp::new(path.to_path_buf()), own);
        let due = s.clear;
        let loaded = Arc::new(Mutex::new(false));
        let l = loaded.clone();
        let c = calls.clone();
        s.run(
            move |done| {
                c.fetch_add(1, Ordering::SeqCst);
                done(answer);
            },
            move || *l.lock().unwrap() = true,
        );
        let loaded = *loaded.lock().unwrap();
        (due, loaded)
    }

    #[test]
    fn a_new_release_clears_once() {
        let dir = tempfile::tempdir().unwrap();
        let path = dir.path().join("com.hamzalabs.maktaba").join(STAMP_FILE);
        let calls = Arc::new(AtomicUsize::new(0));
        // the first start after an update from a release before the record: cleared
        assert_eq!(start(&path, "0.3.20", Ok(()), &calls), (true, true));
        assert_eq!(Stamp::new(path.clone()).read().as_deref(), Some("0.3.20"));
        // the same release again (a second start, the tray's start at sign-in): nothing
        assert_eq!(start(&path, "0.3.20", Ok(()), &calls), (false, true));
        assert_eq!(start(&path, "0.3.20", Ok(()), &calls), (false, true));
        assert_eq!(calls.load(Ordering::SeqCst), 1);
        // a later release: cleared again, once
        assert_eq!(start(&path, "0.3.21", Ok(()), &calls), (true, true));
        assert_eq!(start(&path, "0.3.21", Ok(()), &calls), (false, true));
        assert_eq!(calls.load(Ordering::SeqCst), 2);
        // an older one put back by hand holds the newer one's caches: cleared too
        assert_eq!(start(&path, "0.3.20", Ok(()), &calls), (true, true));
    }

    #[test]
    fn a_failed_clear_is_tried_again_and_the_page_loads_anyway() {
        let dir = tempfile::tempdir().unwrap();
        let path = dir.path().join(STAMP_FILE);
        Stamp::new(path.clone()).write("0.3.19").unwrap();
        let calls = Arc::new(AtomicUsize::new(0));
        assert_eq!(
            start(&path, "0.3.20", Err("WebView2: busy".into()), &calls),
            (true, true)
        );
        assert_eq!(Stamp::new(path.clone()).read().as_deref(), Some("0.3.19"));
        assert_eq!(start(&path, "0.3.20", Ok(()), &calls), (true, true));
        assert_eq!(start(&path, "0.3.20", Ok(()), &calls), (false, true));
        assert_eq!(calls.load(Ordering::SeqCst), 2);
    }

    #[test]
    fn the_record_is_one_release() {
        assert!(needs_clear(None, "0.3.20"));
        assert!(needs_clear(Some("0.3.19"), "0.3.20"));
        assert!(!needs_clear(Some("0.3.20"), "0.3.20"));
        let dir = tempfile::tempdir().unwrap();
        let s = Stamp::new(dir.path().join(STAMP_FILE));
        assert_eq!(s.read(), None);
        std::fs::write(dir.path().join(STAMP_FILE), "  \n").unwrap();
        assert_eq!(s.read(), None);
        s.write("0.3.20").unwrap();
        assert_eq!(s.read().as_deref(), Some("0.3.20"));
    }

    #[test]
    fn the_gate_holds_the_loads_until_the_clear_is_over() {
        let url = |p: &str| -> Url { format!("http://127.0.0.1:8780{p}").parse().unwrap() };
        let page: Url = "http://tauri.localhost/".parse().unwrap();
        let g = Gate::held(page.clone());
        // the server's page asked for while the clear runs: it waits, and wins
        assert_eq!(g.admit(url("/")), None);
        assert_eq!(g.admit(url("/logs")), None);
        assert_eq!(g.finish(), Some(url("/logs")));
        // once: the clear's end and the wait for it never load twice
        assert_eq!(g.finish(), None);
        assert_eq!(g.admit(url("/")), Some(url("/")));
        // nothing asked meanwhile: the bundled page
        let g = Gate::held(page.clone());
        assert_eq!(g.finish(), Some(page));
        // nothing to clear: every load goes at once
        let g = Gate::open();
        assert_eq!(g.admit(url("/")), Some(url("/")));
        assert_eq!(g.finish(), None);
    }
}
