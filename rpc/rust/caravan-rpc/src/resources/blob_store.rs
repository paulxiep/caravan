//! BlobStore seam + LocalFs / S3 impls.
//!
//! The seam dispatch (sync trait methods) bridges to async aws-sdk-s3
//! via `tokio::task::block_in_place + Handle::current().block_on(...)`.
//! Callers must therefore be inside a multi-threaded tokio runtime
//! (which `#[tokio::main]` provides by default).

use std::path::{Path, PathBuf};

use serde::{Deserialize, Serialize};
use thiserror::Error;

use crate::wagon;

/// Errors from the BlobStore seam. Serde-serializable so the seam can
/// dispatch over the wire — `std::io::Error` is wrapped into the
/// `Io(String)` variant at construction time rather than carried as
/// the non-serializable `std::io::Error` type.
#[derive(Debug, Error, Serialize, Deserialize, Clone)]
pub enum BlobError {
    #[error("path traversal in blob path: {0}")]
    PathTraversal(String),
    #[error("path escapes base directory: {0}")]
    PathEscape(String),
    #[error("IO error: {0}")]
    Io(String),
    #[error("S3 error: {0}")]
    S3(String),
}

impl From<std::io::Error> for BlobError {
    fn from(e: std::io::Error) -> Self {
        BlobError::Io(e.to_string())
    }
}

/// Caravan-owned seam for opaque blob storage. Decorated with
/// `#[wagon]` so callers dispatch via `client::<dyn BlobStore>()` and
/// caravan can swap impls per yaml composition.
#[wagon]
pub trait BlobStore: Send + Sync {
    fn put(&self, path: &str, data: &[u8]) -> Result<(), BlobError>;
    fn get(&self, path: &str) -> Result<Vec<u8>, BlobError>;
    fn exists(&self, path: &str) -> Result<bool, BlobError>;
    fn delete(&self, path: &str) -> Result<(), BlobError>;
}

/// Filesystem-backed blob store. Used by oss-local targets that don't
/// spin up MinIO + by the no-Caravan local-dev fallback.
pub struct LocalFsBlobStore {
    base: PathBuf,
}

impl LocalFsBlobStore {
    pub fn new(base_path: &str) -> Result<Self, BlobError> {
        let base = Path::new(base_path).canonicalize().or_else(|_| {
            std::fs::create_dir_all(base_path)?;
            Path::new(base_path).canonicalize()
        })?;
        Ok(Self { base })
    }

    /// Validate that `path` resolves inside `self.base` without mutating
    /// the filesystem. Side-effect-free; safe for read paths (get,
    /// exists, delete on a missing file).
    ///
    /// Checks:
    ///   1. No `..` substring (early reject).
    ///   2. Walk up the joined path to the closest existing ancestor,
    ///      canonicalize it, verify it starts with `self.base`. Defeats
    ///      symlinks anywhere in the parent chain (canonicalize follows
    ///      them; if the resolved ancestor escapes base, reject).
    ///   3. If `full` itself exists, reject when its file type is a
    ///      symlink — the parent check would otherwise pass (the parent
    ///      is a real dir under base) while operations on `full` follow
    ///      the symlink and escape. This is the final-component symlink
    ///      attack vector.
    ///
    /// Returns the un-canonicalized `full` path so callers can `write`,
    /// `read`, etc. against the intended location.
    fn validated_path(&self, path: &str) -> Result<PathBuf, BlobError> {
        if path.contains("..") {
            return Err(BlobError::PathTraversal(path.to_string()));
        }
        let clean = path.trim_start_matches('/');
        let full = self.base.join(clean);

        // Find the closest existing ancestor (parent might not exist
        // yet for first-write paths) and canonicalize it.
        let mut probe = full.parent().unwrap_or(&full).to_path_buf();
        while !probe.exists() {
            match probe.parent() {
                Some(p) => probe = p.to_path_buf(),
                None => break,
            }
        }
        let resolved = probe.canonicalize()?;
        if !resolved.starts_with(&self.base) {
            return Err(BlobError::PathEscape(path.to_string()));
        }

        // Final-component symlink defense: if `full` exists and is a
        // symlink, reject regardless of where it points.
        if let Ok(meta) = std::fs::symlink_metadata(&full) {
            if meta.file_type().is_symlink() {
                return Err(BlobError::PathEscape(path.to_string()));
            }
        }
        Ok(full)
    }

    /// Same as `validated_path` plus `create_dir_all` on the parent.
    /// Only writes need this; reads stay side-effect-free via
    /// `validated_path` directly.
    fn prepared_write_path(&self, path: &str) -> Result<PathBuf, BlobError> {
        let full = self.validated_path(path)?;
        if let Some(parent) = full.parent() {
            std::fs::create_dir_all(parent)?;
        }
        Ok(full)
    }
}

impl BlobStore for LocalFsBlobStore {
    fn put(&self, path: &str, data: &[u8]) -> Result<(), BlobError> {
        let full = self.prepared_write_path(path)?;
        std::fs::write(full, data)?;
        Ok(())
    }

    fn get(&self, path: &str) -> Result<Vec<u8>, BlobError> {
        Ok(std::fs::read(self.validated_path(path)?)?)
    }

    fn exists(&self, path: &str) -> Result<bool, BlobError> {
        Ok(self.validated_path(path)?.exists())
    }

    fn delete(&self, path: &str) -> Result<(), BlobError> {
        let full = self.validated_path(path)?;
        if full.exists() {
            std::fs::remove_file(full)?;
        }
        Ok(())
    }
}

// --- S3 impl (gated behind `resources-aws` feature) ------------------------

#[cfg(feature = "resources-aws")]
pub use s3_impl::S3BlobStore;

#[cfg(feature = "resources-aws")]
mod s3_impl {
    use super::{BlobError, BlobStore};

    /// S3-protocol blob store via aws-sdk-s3.
    ///
    /// Same code path serves MinIO (compose, `endpoint_url` set) and real AWS
    /// S3 (cloud, endpoint unset → SDK default resolution + profile-resolved
    /// creds via the mounted `~/.aws`).
    pub struct S3BlobStore {
        client: aws_sdk_s3::Client,
        bucket: String,
    }

    impl S3BlobStore {
        pub fn new(
            bucket: &str,
            endpoint_url: Option<&str>,
            access_key_id: Option<&str>,
            secret_access_key: Option<&str>,
            region: Option<&str>,
        ) -> Result<Self, BlobError> {
            let region_provider = region
                .map(|r| aws_config::Region::new(r.to_string()))
                .unwrap_or_else(|| aws_config::Region::new("us-east-1"));

            let client = tokio::task::block_in_place(|| {
                tokio::runtime::Handle::current().block_on(async {
                    let mut loader = aws_config::defaults(aws_config::BehaviorVersion::latest())
                        .region(region_provider);
                    if let Some(url) = endpoint_url {
                        loader = loader.endpoint_url(url);
                    }
                    if let (Some(ak), Some(sk)) = (access_key_id, secret_access_key) {
                        let creds =
                            aws_sdk_s3::config::Credentials::new(ak, sk, None, None, "caravan-env");
                        loader = loader.credentials_provider(creds);
                    }
                    let cfg = loader.load().await;
                    let s3_cfg = aws_sdk_s3::config::Builder::from(&cfg)
                        .force_path_style(true) // MinIO requires path-style.
                        .build();
                    aws_sdk_s3::Client::from_conf(s3_cfg)
                })
            });

            // Best-effort bucket create — MinIO needs it; real AWS rejects
            // with AccessDenied on a pre-existing bucket which we ignore.
            let bucket_name = bucket.to_string();
            let client_for_create = client.clone();
            let _ = tokio::task::block_in_place(|| {
                tokio::runtime::Handle::current().block_on(async {
                    client_for_create
                        .create_bucket()
                        .bucket(&bucket_name)
                        .send()
                        .await
                        .map(|_| ())
                })
            });

            Ok(Self {
                client,
                bucket: bucket.to_string(),
            })
        }

        /// Construct from Caravan-injected env vars (S3_BUCKET required;
        /// S3_ENDPOINT_URL / AWS_ACCESS_KEY_ID / AWS_SECRET_ACCESS_KEY /
        /// AWS_REGION optional).
        pub fn from_env() -> Result<Self, BlobError> {
            let bucket = std::env::var("S3_BUCKET")
                .map_err(|_| BlobError::S3("S3BlobStore::from_env requires S3_BUCKET".into()))?;
            Self::new(
                &bucket,
                std::env::var("S3_ENDPOINT_URL").ok().as_deref(),
                std::env::var("AWS_ACCESS_KEY_ID").ok().as_deref(),
                std::env::var("AWS_SECRET_ACCESS_KEY").ok().as_deref(),
                std::env::var("AWS_REGION").ok().as_deref(),
            )
        }
    }

    impl BlobStore for S3BlobStore {
        fn put(&self, path: &str, data: &[u8]) -> Result<(), BlobError> {
            let key = path.trim_start_matches('/').to_string();
            tokio::task::block_in_place(|| {
                tokio::runtime::Handle::current().block_on(async {
                    self.client
                        .put_object()
                        .bucket(&self.bucket)
                        .key(&key)
                        .body(aws_sdk_s3::primitives::ByteStream::from(data.to_vec()))
                        .send()
                        .await
                        .map_err(|e| BlobError::S3(e.to_string()))
                        .map(|_| ())
                })
            })
        }

        fn get(&self, path: &str) -> Result<Vec<u8>, BlobError> {
            let key = path.trim_start_matches('/').to_string();
            tokio::task::block_in_place(|| {
                tokio::runtime::Handle::current().block_on(async {
                    let response = self
                        .client
                        .get_object()
                        .bucket(&self.bucket)
                        .key(&key)
                        .send()
                        .await
                        .map_err(|e| BlobError::S3(e.to_string()))?;
                    let bytes = response
                        .body
                        .collect()
                        .await
                        .map_err(|e| BlobError::S3(e.to_string()))?
                        .into_bytes()
                        .to_vec();
                    Ok(bytes)
                })
            })
        }

        fn exists(&self, path: &str) -> Result<bool, BlobError> {
            let key = path.trim_start_matches('/').to_string();
            tokio::task::block_in_place(|| {
                tokio::runtime::Handle::current().block_on(async {
                    match self
                        .client
                        .head_object()
                        .bucket(&self.bucket)
                        .key(&key)
                        .send()
                        .await
                    {
                        Ok(_) => Ok(true),
                        Err(_) => Ok(false),
                    }
                })
            })
        }

        fn delete(&self, path: &str) -> Result<(), BlobError> {
            let key = path.trim_start_matches('/').to_string();
            tokio::task::block_in_place(|| {
                tokio::runtime::Handle::current().block_on(async {
                    self.client
                        .delete_object()
                        .bucket(&self.bucket)
                        .key(&key)
                        .send()
                        .await
                        .map_err(|e| BlobError::S3(e.to_string()))
                        .map(|_| ())
                })
            })
        }
    }
}
