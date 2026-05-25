//! Auto-registration: read env vars, call `provide()` for each
//! Caravan-shipped resource seam. Mirrors `caravan_rpc.resources.auto_register`
//! in the Python SDK.
//!
//! Env-var routing:
//! - BlobStore:
//!   - `S3_ENDPOINT_URL` set → S3BlobStore against MinIO (oss-local).
//!   - `S3_BUCKET` set without endpoint → S3BlobStore against real AWS.
//!   - neither → LocalFs from yaml fallback (best-effort).
//! - MessageQueue:
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
use super::queue::MessageQueue;
use crate::provide;

/// One-line resource bootstrap. Call once at process startup.
/// `yaml_fallback` is optional — pass `None` if your app has no
/// non-Caravan local-dev path.
pub fn auto_register_resources(
    yaml_fallback: Option<&Value>,
) -> Result<(), AutoRegisterError> {
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
    // Env path: S3_ENDPOINT_URL → MinIO; bare S3_BUCKET → real AWS.
    let has_endpoint = std::env::var("S3_ENDPOINT_URL").is_ok();
    let has_bucket = std::env::var("S3_BUCKET").is_ok();
    if has_endpoint || has_bucket {
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
                "S3_BUCKET/S3_ENDPOINT_URL set but caravan-rpc built without `resources-aws` feature".into(),
            ));
        }
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
        let scheme = env_url
            .split("://")
            .next()
            .unwrap_or("")
            .to_lowercase();
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
