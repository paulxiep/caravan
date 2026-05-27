//! End-to-end tests for the caravan_rpc.resources module —
//! BlobStore + MessageQueue seam dispatch via the inproc registry.

use std::sync::{Arc, Mutex};

use caravan_rpc::resources::{BlobError, BlobStore, LocalFsBlobStore, auto_register_resources};
use caravan_rpc::{__clear_registry_for_tests, client, provide};

/// Serializes tests that mutate process-global env vars (and the
/// global wagon registry). Rust runs tests in parallel by default; env
/// reads/writes from `std::env` are not thread-safe (Rust 2024 marked
/// `set_var`/`remove_var` unsafe for exactly this reason). Every test
/// that touches env vars or `__clear_registry_for_tests` acquires this
/// guard so the suite runs deterministically without a `--test-threads=1`
/// instruction.
///
/// `unwrap_or_else(|e| e.into_inner())` swallows poisoning so a panic
/// in one test doesn't cascade-fail the rest.
static ENV_TEST_LOCK: Mutex<()> = Mutex::new(());

fn env_test_guard() -> std::sync::MutexGuard<'static, ()> {
    ENV_TEST_LOCK
        .lock()
        .unwrap_or_else(|poisoned| poisoned.into_inner())
}

/// LocalFs roundtrip via `client::<dyn BlobStore>()`.
#[test]
fn localfs_roundtrip_via_client_dispatch() {
    let _guard = env_test_guard();
    __clear_registry_for_tests();

    let tmp = tempfile::tempdir().unwrap();
    let store = LocalFsBlobStore::new(tmp.path().to_str().unwrap()).unwrap();
    provide::<dyn BlobStore>(Arc::new(store));

    let blob = client::<dyn BlobStore>();
    blob.put("hello.txt", b"caravan").unwrap();
    assert!(blob.exists("hello.txt").unwrap());
    assert_eq!(blob.get("hello.txt").unwrap(), b"caravan");
    blob.delete("hello.txt").unwrap();
    assert!(!blob.exists("hello.txt").unwrap());
}

#[test]
fn localfs_rejects_path_traversal() {
    let _guard = env_test_guard();
    __clear_registry_for_tests();

    let tmp = tempfile::tempdir().unwrap();
    let store = LocalFsBlobStore::new(tmp.path().to_str().unwrap()).unwrap();
    match store.put("../escape.txt", b"x") {
        Err(BlobError::PathTraversal(_)) => {}
        other => panic!("expected PathTraversal; got {other:?}"),
    }
}

/// auto_register_resources with yaml fallback wires LocalFs into the
/// registry; subsequent client() calls dispatch through it.
#[test]
fn auto_register_yaml_fallback_local_fs() {
    let _guard = env_test_guard();
    __clear_registry_for_tests();
    // Clear env so the fallback path runs.
    unsafe {
        std::env::remove_var("CARAVAN_BLOB_BACKEND");
        std::env::remove_var("S3_ENDPOINT_URL");
        std::env::remove_var("S3_BUCKET");
        std::env::remove_var("QUEUE_URL");
    }

    let tmp = tempfile::tempdir().unwrap();
    let yaml = format!(
        r#"
blob_storage:
  type: local_fs
  base_path: {}
"#,
        tmp.path().to_str().unwrap().replace('\\', "/")
    );
    let fallback: serde_yaml::Value = serde_yaml::from_str(&yaml).unwrap();

    auto_register_resources(Some(&fallback)).unwrap();

    let blob = client::<dyn BlobStore>();
    blob.put("from-fallback.bin", b"42").unwrap();
    assert_eq!(blob.get("from-fallback.bin").unwrap(), b"42");
}

/// QUEUE_URL with an unsupported scheme should be surfaced as an
/// AutoRegisterError, not a silent skip.
#[test]
fn auto_register_unsupported_queue_scheme_errors() {
    let _guard = env_test_guard();
    __clear_registry_for_tests();
    unsafe {
        std::env::set_var("QUEUE_URL", "ftp://nope");
    }
    let res = auto_register_resources(None);
    unsafe {
        std::env::remove_var("QUEUE_URL");
    }
    let err = res.expect_err("expected error");
    let msg = err.to_string();
    assert!(
        msg.contains("ftp") || msg.contains("unsupported"),
        "unexpected error message: {msg}"
    );
}
