//! {{id}}: a Maktaba plugin (no_std: it imports maktaba:plugin only, never std's WASI).
#![no_std]
extern crate alloc;
// wit-bindgen's generated code names `std`; without std, alloc has what it uses
extern crate alloc as std;

use alloc::format;
use alloc::string::String;

wit_bindgen::generate!({ path: "wit", world: "plugin", std_feature });

use maktaba::plugin::{job, log};

#[global_allocator]
static ALLOC: dlmalloc::GlobalDlmalloc = dlmalloc::GlobalDlmalloc;

#[panic_handler]
fn panic(_: &core::panic::PanicInfo) -> ! {
    core::arch::wasm32::unreachable()
}

struct P;

impl Guest for P {
    fn run(stage: String) -> Result<RunOutput, RunError> {
        log::write(log::Level::Info, "hello", &format!("{{\"stage\":\"{stage}\"}}"));
        let s = job::job_subject();
        Ok(RunOutput { output_hash: format!("h-{}", s.id), summary: "{}".into() })
    }
}

export!(P);

/// The canonical ABI's allocator (std provides it on wasm32-wasip2; without std, this does).
#[no_mangle]
unsafe extern "C" fn cabi_realloc(old_ptr: *mut u8, old_len: usize, align: usize, new_len: usize) -> *mut u8 {
    use alloc::alloc::{alloc, realloc, Layout};
    if new_len == 0 {
        return align as *mut u8;
    }
    let p = if old_len == 0 {
        alloc(Layout::from_size_align_unchecked(new_len, align))
    } else {
        realloc(old_ptr, Layout::from_size_align_unchecked(old_len, align), new_len)
    };
    if p.is_null() {
        core::arch::wasm32::unreachable()
    }
    p
}
