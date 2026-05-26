//! MessageQueue seam + Redis / RabbitMQ / SQS impls.

use serde::{Deserialize, Serialize};
use serde_json::Value;
use thiserror::Error;

use crate::wagon;

/// Errors from the MessageQueue seam. Serde-serializable for over-the-
/// wire dispatch; `serde_json::Error` is converted to `Json(String)` at
/// construction time.
#[derive(Debug, Error, Serialize, Deserialize, Clone)]
pub enum QueueError {
    #[error("JSON error: {0}")]
    Json(String),
    #[error("Redis error: {0}")]
    Redis(String),
    #[error("RabbitMQ error: {0}")]
    RabbitMQ(String),
    #[error("SQS error: {0}")]
    Sqs(String),
    #[error("invalid URL: {0}")]
    InvalidUrl(String),
}

impl From<serde_json::Error> for QueueError {
    fn from(e: serde_json::Error) -> Self {
        QueueError::Json(e.to_string())
    }
}

/// Caravan-owned seam for at-least-once message queues. Decorated with
/// `#[wagon]` so callers dispatch via `client::<dyn MessageQueue>()`.
///
/// The `topic:` parameter is a portability device: Redis Streams +
/// RabbitMQ use it as a queue/stream name; SQS ignores it (each SQS
/// queue URL is its own substrate).
#[wagon]
pub trait MessageQueue: Send + Sync {
    fn publish(&self, topic: &str, message: &Value) -> Result<String, QueueError>;
    fn consume(
        &self,
        topic: &str,
        count: usize,
        block_ms: usize,
    ) -> Result<Vec<(String, Value)>, QueueError>;
    fn ack(&self, topic: &str, message_id: &str) -> Result<(), QueueError>;
    fn extend_visibility(
        &self,
        topic: &str,
        message_id: &str,
        seconds: u64,
    ) -> Result<(), QueueError>;
}

// --- Redis Streams impl (gated behind `resources-redis`) -------------------

#[cfg(feature = "resources-redis")]
pub use redis_impl::RedisStreamQueue;

#[cfg(feature = "resources-redis")]
mod redis_impl {
    use redis::streams::{StreamReadOptions, StreamReadReply};
    use redis::{Commands, RedisError};
    use serde_json::Value;

    use super::{MessageQueue, QueueError};

    impl From<RedisError> for QueueError {
        fn from(e: RedisError) -> Self {
            QueueError::Redis(e.to_string())
        }
    }

    pub struct RedisStreamQueue {
        client: redis::Client,
        consumer_group: String,
        consumer_name: String,
    }

    impl RedisStreamQueue {
        pub fn new(url: &str, consumer_group: &str) -> Result<Self, QueueError> {
            let client = redis::Client::open(url).map_err(QueueError::from)?;
            let consumer_name = format!("worker-{}", &uuid::Uuid::new_v4().to_string()[..8]);
            Ok(Self {
                client,
                consumer_group: consumer_group.to_string(),
                consumer_name,
            })
        }

        fn ensure_group(
            &self,
            conn: &mut redis::Connection,
            topic: &str,
        ) -> Result<(), QueueError> {
            let result: Result<(), RedisError> = redis::cmd("XGROUP")
                .arg("CREATE")
                .arg(topic)
                .arg(&self.consumer_group)
                .arg("0")
                .arg("MKSTREAM")
                .query(conn);
            match result {
                Ok(()) => Ok(()),
                Err(e) if e.to_string().contains("BUSYGROUP") => Ok(()),
                Err(e) => Err(QueueError::Redis(e.to_string())),
            }
        }
    }

    impl MessageQueue for RedisStreamQueue {
        fn publish(&self, topic: &str, message: &Value) -> Result<String, QueueError> {
            let mut conn = self.client.get_connection()?;
            self.ensure_group(&mut conn, topic)?;
            let payload = serde_json::to_string(message)?;
            let id: String = conn.xadd(topic, "*", &[("data", &payload)])?;
            Ok(id)
        }

        fn consume(
            &self,
            topic: &str,
            count: usize,
            block_ms: usize,
        ) -> Result<Vec<(String, Value)>, QueueError> {
            let mut conn = self.client.get_connection()?;
            self.ensure_group(&mut conn, topic)?;
            let opts = StreamReadOptions::default()
                .group(&self.consumer_group, &self.consumer_name)
                .count(count)
                .block(block_ms);
            let reply: StreamReadReply = conn.xread_options(&[topic], &[">"], &opts)?;
            let mut messages = Vec::new();
            for key in reply.keys {
                for entry in key.ids {
                    if let Some(redis::Value::BulkString(bytes)) = entry.map.get("data") {
                        let data_str = String::from_utf8_lossy(bytes);
                        let value: Value = serde_json::from_str(&data_str)?;
                        messages.push((entry.id, value));
                    }
                }
            }
            Ok(messages)
        }

        fn ack(&self, topic: &str, message_id: &str) -> Result<(), QueueError> {
            let mut conn = self.client.get_connection()?;
            let _: () = conn.xack(topic, &self.consumer_group, &[message_id])?;
            Ok(())
        }

        fn extend_visibility(
            &self,
            _topic: &str,
            _message_id: &str,
            _seconds: u64,
        ) -> Result<(), QueueError> {
            Ok(()) // No SQS-style visibility timeout in Redis Streams.
        }
    }
}

// --- RabbitMQ impl (gated behind `resources-rabbit`) -----------------------

#[cfg(feature = "resources-rabbit")]
pub use rabbit_impl::RabbitMQQueue;

#[cfg(feature = "resources-rabbit")]
mod rabbit_impl {
    use std::sync::Mutex;

    use serde_json::Value;

    use super::{MessageQueue, QueueError};

    pub struct RabbitMQQueue {
        url: String,
        state: Mutex<RabbitMQState>,
    }

    struct RabbitMQState {
        connection: Option<lapin::Connection>,
        channel: Option<lapin::Channel>,
        declared: std::collections::HashSet<String>,
    }

    impl RabbitMQQueue {
        pub fn new(url: &str) -> Result<Self, QueueError> {
            let scheme = url::Url::parse(url)
                .map_err(|e| QueueError::InvalidUrl(e.to_string()))?
                .scheme()
                .to_string();
            if scheme != "amqp" && scheme != "amqps" {
                return Err(QueueError::InvalidUrl(format!(
                    "RabbitMQQueue expects amqp(s)://; got {scheme}"
                )));
            }
            Ok(Self {
                url: url.to_string(),
                state: Mutex::new(RabbitMQState {
                    connection: None,
                    channel: None,
                    declared: std::collections::HashSet::new(),
                }),
            })
        }

        fn ensure_channel(&self) -> Result<lapin::Channel, QueueError> {
            let mut state = self.state.lock().unwrap();
            if let Some(ch) = &state.channel {
                if ch.status().connected() {
                    return Ok(ch.clone());
                }
            }
            let url = self.url.clone();
            let (conn, ch) = tokio::task::block_in_place(|| {
                tokio::runtime::Handle::current().block_on(async {
                    let conn =
                        lapin::Connection::connect(&url, lapin::ConnectionProperties::default())
                            .await
                            .map_err(|e| QueueError::RabbitMQ(e.to_string()))?;
                    let ch = conn
                        .create_channel()
                        .await
                        .map_err(|e| QueueError::RabbitMQ(e.to_string()))?;
                    Ok::<_, QueueError>((conn, ch))
                })
            })?;
            state.connection = Some(conn);
            state.channel = Some(ch.clone());
            state.declared.clear();
            Ok(ch)
        }

        fn ensure_queue(&self, ch: &lapin::Channel, topic: &str) -> Result<(), QueueError> {
            {
                let state = self.state.lock().unwrap();
                if state.declared.contains(topic) {
                    return Ok(());
                }
            }
            let topic_owned = topic.to_string();
            let ch_clone = ch.clone();
            tokio::task::block_in_place(|| {
                tokio::runtime::Handle::current().block_on(async {
                    ch_clone
                        .queue_declare(
                            &topic_owned,
                            lapin::options::QueueDeclareOptions {
                                durable: true,
                                ..Default::default()
                            },
                            lapin::types::FieldTable::default(),
                        )
                        .await
                        .map_err(|e| QueueError::RabbitMQ(e.to_string()))
                        .map(|_| ())
                })
            })?;
            let mut state = self.state.lock().unwrap();
            state.declared.insert(topic.to_string());
            Ok(())
        }
    }

    impl MessageQueue for RabbitMQQueue {
        fn publish(&self, topic: &str, message: &Value) -> Result<String, QueueError> {
            let ch = self.ensure_channel()?;
            self.ensure_queue(&ch, topic)?;
            let body = serde_json::to_vec(message)?;
            let msg_id = uuid::Uuid::new_v4().to_string().replace('-', "");
            let topic_owned = topic.to_string();
            let msg_id_owned = msg_id.clone();
            tokio::task::block_in_place(|| {
                tokio::runtime::Handle::current().block_on(async {
                    ch.basic_publish(
                        "",
                        &topic_owned,
                        lapin::options::BasicPublishOptions::default(),
                        &body,
                        lapin::BasicProperties::default()
                            .with_message_id(msg_id_owned.into())
                            .with_delivery_mode(2)
                            .with_content_type("application/json".into()),
                    )
                    .await
                    .map_err(|e| QueueError::RabbitMQ(e.to_string()))?
                    .await
                    .map_err(|e| QueueError::RabbitMQ(e.to_string()))?;
                    Ok::<_, QueueError>(())
                })
            })?;
            Ok(msg_id)
        }

        fn consume(
            &self,
            topic: &str,
            count: usize,
            block_ms: usize,
        ) -> Result<Vec<(String, Value)>, QueueError> {
            let ch = self.ensure_channel()?;
            self.ensure_queue(&ch, topic)?;
            let deadline =
                std::time::Instant::now() + std::time::Duration::from_millis(block_ms as u64);
            let topic_owned = topic.to_string();
            tokio::task::block_in_place(|| {
                tokio::runtime::Handle::current().block_on(async {
                    let mut out: Vec<(String, Value)> = Vec::new();
                    while out.len() < count {
                        if std::time::Instant::now() >= deadline {
                            break;
                        }
                        let msg = ch
                            .basic_get(&topic_owned, lapin::options::BasicGetOptions::default())
                            .await
                            .map_err(|e| QueueError::RabbitMQ(e.to_string()))?;
                        match msg {
                            Some(delivery) => {
                                let payload: Value = serde_json::from_slice(&delivery.data)?;
                                out.push((delivery.delivery_tag.to_string(), payload));
                            }
                            None => {
                                tokio::time::sleep(std::time::Duration::from_millis(50)).await;
                            }
                        }
                    }
                    Ok::<_, QueueError>(out)
                })
            })
        }

        fn ack(&self, _topic: &str, message_id: &str) -> Result<(), QueueError> {
            let ch = self.ensure_channel()?;
            let delivery_tag: u64 = message_id
                .parse()
                .map_err(|e: std::num::ParseIntError| QueueError::RabbitMQ(e.to_string()))?;
            tokio::task::block_in_place(|| {
                tokio::runtime::Handle::current().block_on(async {
                    ch.basic_ack(delivery_tag, lapin::options::BasicAckOptions::default())
                        .await
                        .map_err(|e| QueueError::RabbitMQ(e.to_string()))
                })
            })
        }

        fn extend_visibility(
            &self,
            _topic: &str,
            _message_id: &str,
            _seconds: u64,
        ) -> Result<(), QueueError> {
            Ok(())
        }
    }
}

// --- SQS impl (gated behind `resources-aws`) -------------------------------

#[cfg(feature = "resources-aws")]
pub use sqs_impl::SqsQueue;

#[cfg(feature = "resources-aws")]
mod sqs_impl {
    use serde_json::Value;

    use super::{MessageQueue, QueueError};

    /// AWS SQS impl via aws-sdk-sqs. Single-queue per URL.
    pub struct SqsQueue {
        client: aws_sdk_sqs::Client,
        queue_url: String,
    }

    impl SqsQueue {
        pub fn from_url(url: &str) -> Result<Self, QueueError> {
            let scheme = url::Url::parse(url)
                .map_err(|e| QueueError::InvalidUrl(e.to_string()))?
                .scheme()
                .to_string();
            if scheme != "http" && scheme != "https" {
                return Err(QueueError::InvalidUrl(format!(
                    "SqsQueue expects https://sqs.<region>.amazonaws.com/...; got {scheme}"
                )));
            }
            let client = tokio::task::block_in_place(|| {
                tokio::runtime::Handle::current().block_on(async {
                    let cfg = aws_config::defaults(aws_config::BehaviorVersion::latest())
                        .load()
                        .await;
                    aws_sdk_sqs::Client::new(&cfg)
                })
            });
            Ok(Self {
                client,
                queue_url: url.to_string(),
            })
        }

        pub fn from_env() -> Result<Self, QueueError> {
            let url = std::env::var("QUEUE_URL")
                .map_err(|_| QueueError::Sqs("SqsQueue::from_env requires QUEUE_URL".into()))?;
            Self::from_url(&url)
        }
    }

    impl MessageQueue for SqsQueue {
        fn publish(&self, _topic: &str, message: &Value) -> Result<String, QueueError> {
            let body = serde_json::to_string(message)?;
            let queue_url = self.queue_url.clone();
            let client = self.client.clone();
            tokio::task::block_in_place(|| {
                tokio::runtime::Handle::current().block_on(async {
                    let resp = client
                        .send_message()
                        .queue_url(&queue_url)
                        .message_body(body)
                        .send()
                        .await
                        .map_err(|e| QueueError::Sqs(e.to_string()))?;
                    Ok(resp.message_id().unwrap_or_default().to_string())
                })
            })
        }

        fn consume(
            &self,
            _topic: &str,
            count: usize,
            block_ms: usize,
        ) -> Result<Vec<(String, Value)>, QueueError> {
            let wait_seconds = (block_ms / 1000).clamp(0, 20) as i32;
            let max_msgs = count.min(10) as i32;
            let queue_url = self.queue_url.clone();
            let client = self.client.clone();
            tokio::task::block_in_place(|| {
                tokio::runtime::Handle::current().block_on(async {
                    let resp = client
                        .receive_message()
                        .queue_url(&queue_url)
                        .max_number_of_messages(max_msgs)
                        .wait_time_seconds(wait_seconds)
                        .send()
                        .await
                        .map_err(|e| QueueError::Sqs(e.to_string()))?;
                    let mut out: Vec<(String, Value)> = Vec::new();
                    for m in resp.messages.unwrap_or_default() {
                        let handle = m
                            .receipt_handle
                            .ok_or_else(|| QueueError::Sqs("missing ReceiptHandle".into()))?;
                        let body = m
                            .body
                            .ok_or_else(|| QueueError::Sqs("missing Body".into()))?;
                        let value: Value = serde_json::from_str(&body)?;
                        out.push((handle, value));
                    }
                    Ok(out)
                })
            })
        }

        fn ack(&self, _topic: &str, message_id: &str) -> Result<(), QueueError> {
            let queue_url = self.queue_url.clone();
            let handle = message_id.to_string();
            let client = self.client.clone();
            tokio::task::block_in_place(|| {
                tokio::runtime::Handle::current().block_on(async {
                    client
                        .delete_message()
                        .queue_url(&queue_url)
                        .receipt_handle(handle)
                        .send()
                        .await
                        .map_err(|e| QueueError::Sqs(e.to_string()))
                        .map(|_| ())
                })
            })
        }

        fn extend_visibility(
            &self,
            _topic: &str,
            message_id: &str,
            seconds: u64,
        ) -> Result<(), QueueError> {
            let queue_url = self.queue_url.clone();
            let handle = message_id.to_string();
            let client = self.client.clone();
            tokio::task::block_in_place(|| {
                tokio::runtime::Handle::current().block_on(async {
                    client
                        .change_message_visibility()
                        .queue_url(&queue_url)
                        .receipt_handle(handle)
                        .visibility_timeout(seconds as i32)
                        .send()
                        .await
                        .map_err(|e| QueueError::Sqs(e.to_string()))
                        .map(|_| ())
                })
            })
        }
    }
}
