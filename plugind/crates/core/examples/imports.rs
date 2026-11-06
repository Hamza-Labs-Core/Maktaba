//! Prints a component's imports and exports: `cargo run --example imports -- x.wasm`.
fn main() {
    let path = std::env::args().nth(1).expect("a .wasm path");
    let engine = wasmtime::Engine::new(&plugind_core::engine::engine_config()).unwrap();
    let c = wasmtime::component::Component::from_file(&engine, path).unwrap();
    let (i, e) = plugind_core::audit::names(&engine, &c);
    println!("imports: {i:#?}\nexports: {e:#?}");
}
