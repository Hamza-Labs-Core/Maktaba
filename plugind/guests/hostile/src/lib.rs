//! A plugin that misbehaves on purpose; the stage name says how (stories 54.02 T04–T10,
//! 54.03 T07). Built without std, like the SDK's Rust template.
#![no_std]
extern crate alloc;
// wit-bindgen's generated code names `std`; without std, alloc has what it uses
extern crate alloc as std;

use alloc::format;
use alloc::string::String;
use alloc::vec::Vec;

wit_bindgen::generate!({ path: "../../../shared/plugins/wit", world: "plugin", std_feature });

use maktaba::plugin::types::CallError;
use maktaba::plugin::{data, job, log, queue, write};

#[global_allocator]
static ALLOC: dlmalloc::GlobalDlmalloc = dlmalloc::GlobalDlmalloc;

#[panic_handler]
fn panic(_: &core::panic::PanicInfo) -> ! {
    core::arch::wasm32::unreachable()
}

#[inline(never)]
#[allow(unconditional_recursion)]
fn recurse(n: u64) -> u64 {
    let x = core::hint::black_box(n);
    recurse(x + 1) + x
}

fn err(code: &str) -> RunError {
    RunError { code: code.into(), message: String::new() }
}

struct P;

impl Guest for P {
    fn run(stage: String) -> Result<RunOutput, RunError> {
        let s = job::job_subject();
        match stage.as_str() {
            "ok" => {}
            "loop" => loop {
                core::hint::black_box(0);
            },
            "memory" => {
                let mut keep: Vec<Vec<u8>> = Vec::new();
                loop {
                    keep.push(core::hint::black_box(alloc::vec![1u8; 1 << 20]));
                }
            }
            "trap" => panic!("boom"),
            "recurse" => {
                core::hint::black_box(recurse(0));
            }
            "deny" => {
                // ungranted: the host answers capability without calling the API
                match write::add_tags(&s.id, &[String::from("x")]) {
                    Err(CallError::Capability) => return Err(err("no_tags_permission")),
                    other => return Err(err(&format!("unexpected {other:?}"))),
                }
            }
            "flood_log" => {
                for i in 0..10_000 {
                    log::write(log::Level::Info, "tick", &format!("{{\"i\":{i}}}"));
                }
            }
            "flood_api" => {
                for _ in 0..300 {
                    let _ = data::get(&s, "k");
                }
            }
            "progress" => {
                job::progress(50);
                job::checkpoint(b"half").map_err(|_| err("checkpoint"))?;
            }
            "cancel" => {
                job::checkpoint(b"before-cancel").map_err(|_| err("checkpoint"))?;
                loop {
                    core::hint::black_box(0);
                }
            }
            "request" => {
                queue::request("other", &s, "{}").map_err(|e| err(&format!("{e:?}")))?;
            }
            "echo" => {
                let summary = format!(
                    "{{\"payload\":{},\"settings\":{},\"grants\":{},\"last\":{}}}",
                    job::payload(),
                    job::settings(),
                    job::grants().len(),
                    job::last_checkpoint().map(|c| c.len()).unwrap_or(0)
                );
                return Ok(RunOutput { output_hash: format!("echo-{}", s.id), summary });
            }
            other => return Err(err(&format!("unknown stage {other}"))),
        }
        Ok(RunOutput { output_hash: format!("{stage}-{}", s.id), summary: "{}".into() })
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

/// libc's memcmp, which std's libc gives on wasm32-wasip2 and a no_std guest must bring.
#[no_mangle]
unsafe extern "C" fn memcmp(a: *const core::ffi::c_void, b: *const core::ffi::c_void, n: usize) -> i32 {
    let (a, b) = (a as *const u8, b as *const u8);
    for i in 0..n {
        let (x, y) = (*a.add(i), *b.add(i));
        if x != y {
            return x as i32 - y as i32;
        }
    }
    0
}

#[no_mangle]
unsafe extern "C" fn bcmp(a: *const core::ffi::c_void, b: *const core::ffi::c_void, n: usize) -> i32 {
    memcmp(a, b, n)
}
