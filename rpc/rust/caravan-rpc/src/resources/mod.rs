//! Caravan-shipped resource seams + concrete impls.
//!
//! Resource adapters (BlobStore, MessageQueue) are part of caravan-rpc
//! rather than per-user-repo code. User crates import the seam trait,
//! call `client::<dyn BlobStore>().put(...)`, and let caravan
//! auto-register the right impl at startup via
//! [`auto_register_resources`].
//!
//! The thesis: every caravan user writes only `#[wagon]` / `provide` /
//! `client` against their own domain seams; resource adapters ship from
//! caravan so a new user doesn't have to hand-author aws-sdk wrappers /
//! redis clients / lapin channels.
//!
//! Optional features gate the heavy deps:
//!
//! ```toml
//! caravan-rpc = { version = "0.1", features = ["resources-aws", "resources-redis", "resources-rabbit"] }
//! ```
//!
//! The seam traits themselves load without any feature; only the
//! concrete impls demand their respective backend crates.

pub mod auto_register;
pub mod blob_store;
pub mod queue;

pub use auto_register::auto_register_resources;
pub use blob_store::{BlobError, BlobStore, LocalFsBlobStore};
pub use queue::{MessageQueue, QueueError};

#[cfg(feature = "resources-aws")]
pub use blob_store::S3BlobStore;
#[cfg(feature = "resources-aws")]
pub use queue::SqsQueue;
#[cfg(feature = "resources-redis")]
pub use queue::RedisStreamQueue;
#[cfg(feature = "resources-rabbit")]
pub use queue::RabbitMQQueue;
