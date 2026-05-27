//! Auto-registration: read env vars, call `provide()` for each
//! Caravan-shipped resource seam. Mirrors `caravan_rpc.resources.auto_register`
//! in the Python SDK.
//!
//! Env-var routing:
//! - BlobStore (marker-driven so the caller's compose / HCL emit
//!   declares intent unambiguously; defeats the silent-fallback footgun):
//!   - `CARAVAN_BLOB_BACKEND=s3` → S3BlobStore via
//!     `S3BlobStore::from_env()` (consumes `S3_BUCKET` +
//!     optional `S3_ENDPOINT_URL` / `AWS_*`). Loud-fails when
//!     `S3_BUCKET` is unset.
//!   - `CARAVAN_BLOB_BACKEND=local-fs` → LocalFsBlobStore at
//!     yaml_fallback's `blob_storage.base_path` (or `/data/blobs`
//!     default). Skips S3 even when `S3_ENDPOINT_URL` is set —
//!     the "MinIO emitted but skipped" oss-local case.
//!   - unset → consult `yaml_fallback` (non-caravan local-dev).
//! - MessageQueue (URL-scheme routing — no marker needed since the
//!   scheme is unambiguous):
//!   - `QUEUE_URL` scheme `redis(s)://` → RedisStreamQueue.
//!   - `QUEUE_URL` scheme `amqp(s)://` → RabbitMQQueue.
//!   - `QUEUE_URL` scheme `http(s)://` → SqsQueue.
//!   - none → YAML fallback (best-effort).
//!
//! `yaml_fallback` is the user's parsed application config dict. We
//! consult known sub-keys (`blob_storage.type`, `queue.type`, etc.) but
//! ignore everything else.

use std::sync::Arc;

use serde_yaml::Value;

use super::blob_store::{BlobStore, LocalFsBlobStore};
#[cfg(any(
    feature = "resources-aws",
    feature = "resources-redis",
    feature = "resources-rabbit"
))]
use super::queue::MessageQueue;
use crate::provide;

/// One-line resource bootstrap. Call once at process startup.
/// `yaml_fallback` is optional — pass `None` if your app has no
/// non-Caravan local-dev path.
pub fn auto_register_resources(yaml_fallback: Option<&Value>) -> Result<(), AutoRegisterError> {
    register_blob_store(yaml_fallback)?;
    register_message_queue(yaml_fallback)?;
    Ok(())
}

#[derive(Debug, thiserror::Error)]
pub enum AutoRegisterError {
    #[error("BlobStore registration failed: {0}")]
    BlobStore(String),
    #[error("MessageQueue registration failed: {0}")]
    Queue(String),
    #[error("unsupported QUEUE_URL scheme {0:?}")]
    UnsupportedQueueScheme(String),
}

fn register_blob_store(fallback: Option<&Value>) -> Result<(), AutoRegisterError> {
    // Caravan-emitted explicit marker: `CARAVAN_BLOB_BACKEND` declares
    // which impl the caller's compose/HCL emit intends. With backend=s3
    // we assert S3_BUCKET is set — catches the "user forgot .env.hybrid
    // from tofu output" footgun loudly at startup. With backend=local-fs
    // we skip S3 entirely (oss-local "MinIO emitted but skipped"). No
    // marker → consult yaml_fallback (non-caravan local-dev path).
    match std::env::var("CARAVAN_BLOB_BACKEND").ok().as_deref() {
        Some("s3") => {
            if std::env::var("S3_BUCKET").is_err() {
                return Err(AutoRegisterError::BlobStore(
                    "CARAVAN_BLOB_BACKEND=s3 but S3_BUCKET is unset; did you forget \
                     to populate .env.hybrid from `tofu output -json`? \
                     (`caravan up` auto-generates it.)"
                        .into(),
                ));
            }
            #[cfg(feature = "resources-aws")]
            {
                let s3 = super::blob_store::S3BlobStore::from_env()
                    .map_err(|e| AutoRegisterError::BlobStore(e.to_string()))?;
                provide::<dyn BlobStore>(Arc::new(s3));
                return Ok(());
            }
            #[cfg(not(feature = "resources-aws"))]
            {
                return Err(AutoRegisterError::BlobStore(
                    "CARAVAN_BLOB_BACKEND=s3 but caravan-rpc built without `resources-aws` feature".into(),
                ));
            }
        }
        Some("local-fs") => {
            let base = yaml_blob_base(fallback).unwrap_or_else(|| "/data/blobs".into());
            let store = LocalFsBlobStore::new(&base)
                .map_err(|e| AutoRegisterError::BlobStore(e.to_string()))?;
            provide::<dyn BlobStore>(Arc::new(store));
            return Ok(());
        }
        _ => {}
    }

    // YAML fallback.
    let Some(fb) = fallback else {
        return Ok(());
    };
    let Some(blob) = fb.get("blob_storage") else {
        return Ok(());
    };
    let Some(storage_type) = blob.get("type").and_then(Value::as_str) else {
        return Ok(());
    };
    match storage_type {
        "local_fs" => {
            if let Some(base) = blob.get("base_path").and_then(Value::as_str) {
                let store = LocalFsBlobStore::new(base)
                    .map_err(|e| AutoRegisterError::BlobStore(e.to_string()))?;
                provide::<dyn BlobStore>(Arc::new(store));
            }
        }
        "s3" => {
            #[cfg(feature = "resources-aws")]
            {
                let bucket = blob.get("bucket").and_then(Value::as_str).ok_or_else(|| {
                    AutoRegisterError::BlobStore("blob_storage.s3 requires bucket".into())
                })?;
                let region = blob.get("region").and_then(Value::as_str);
                let store = super::blob_store::S3BlobStore::new(bucket, None, None, None, region)
                    .map_err(|e| AutoRegisterError::BlobStore(e.to_string()))?;
                provide::<dyn BlobStore>(Arc::new(store));
            }
            #[cfg(not(feature = "resources-aws"))]
            return Err(AutoRegisterError::BlobStore(
                "blob_storage.s3 requires `resources-aws` feature".into(),
            ));
        }
        _ => {}
    }
    Ok(())
}

fn register_message_queue(fallback: Option<&Value>) -> Result<(), AutoRegisterError> {
    if let Ok(env_url) = std::env::var("QUEUE_URL") {
        // Parse the scheme without depending on the `url` crate — that's
        // a feature-gated optional dep. Scheme is `<chars>://` so a simple
        // split suffices for our routing decision.
        let scheme = env_url.split("://").next().unwrap_or("").to_lowercase();
        match scheme.as_str() {
            "redis" | "rediss" => {
                #[cfg(feature = "resources-redis")]
                {
                    let consumer_group = consumer_group_from_fallback(fallback);
                    let q = super::queue::RedisStreamQueue::new(&env_url, &consumer_group)
                        .map_err(|e| AutoRegisterError::Queue(e.to_string()))?;
                    provide::<dyn MessageQueue>(Arc::new(q));
                    return Ok(());
                }
                #[cfg(not(feature = "resources-redis"))]
                return Err(AutoRegisterError::Queue(
                    "QUEUE_URL redis:// scheme requires `resources-redis` feature".into(),
                ));
            }
            "amqp" | "amqps" => {
                #[cfg(feature = "resources-rabbit")]
                {
                    let q = super::queue::RabbitMQQueue::new(&env_url)
                        .map_err(|e| AutoRegisterError::Queue(e.to_string()))?;
                    provide::<dyn MessageQueue>(Arc::new(q));
                    return Ok(());
                }
                #[cfg(not(feature = "resources-rabbit"))]
                return Err(AutoRegisterError::Queue(
                    "QUEUE_URL amqp:// scheme requires `resources-rabbit` feature".into(),
                ));
            }
            "http" | "https" => {
                #[cfg(feature = "resources-aws")]
                {
                    let q = super::queue::SqsQueue::from_url(&env_url)
                        .map_err(|e| AutoRegisterError::Queue(e.to_string()))?;
                    provide::<dyn MessageQueue>(Arc::new(q));
                    return Ok(());
                }
                #[cfg(not(feature = "resources-aws"))]
                return Err(AutoRegisterError::Queue(
                    "QUEUE_URL https:// (SQS) scheme requires `resources-aws` feature".into(),
                ));
            }
            other => {
                return Err(AutoRegisterError::UnsupportedQueueScheme(other.to_string()));
            }
        }
    }
    // YAML fallback intentionally minimal — most Rust services that want
    // a no-Caravan local-dev queue path can set QUEUE_URL=redis://localhost
    // in their .env. Extend here when a user repo asks for it.
    let _ = fallback;
    Ok(())
}

#[cfg(feature = "resources-redis")]
fn consumer_group_from_fallback(fallback: Option<&Value>) -> String {
    let default = "caravan-workers".to_string();
    let Some(fb) = fallback else {
        return default;
    };
    fb.get("queue")
        .and_then(|q| q.get("consumer_group"))
        .and_then(Value::as_str)
        .map(String::from)
        .unwrap_or(default)
}

/// Pulls `blob_storage.base_path` from the user's YAML fallback config
/// when type=local_fs. Returns None otherwise. Used by the
/// CARAVAN_BLOB_BACKEND=local-fs path to find the on-disk root.
fn yaml_blob_base(fallback: Option<&Value>) -> Option<String> {
    let fb = fallback?;
    let blob = fb.get("blob_storage")?;
    if blob.get("type").and_then(Value::as_str)? != "local_fs" {
        return None;
    }
    blob.get("base_path")
        .and_then(Value::as_str)
        .map(String::from)
}
