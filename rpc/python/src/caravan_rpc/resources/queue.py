"""MessageQueue seam + Redis/RabbitMQ/SQS impls.

The ``MessageQueue`` seam is ``@wagon``-decorated so callers dispatch through
``client(MessageQueue).publish(...)``. Caravan picks the backend per yaml
composition (oss-local redis-streams or rabbitmq; cloud-managed SQS).

The ``topic:`` parameter is a portability device — Redis Streams + RabbitMQ
use it as a queue/stream name; SQS ignores it (each SqsQueue instance is
bound to a single queue URL because that's how SQS works).
"""

from __future__ import annotations

import json
import os
import time
import uuid
from urllib.parse import urlparse

from .. import wagon


@wagon
class MessageQueue:
    """Caravan-owned seam for at-least-once message queues."""

    def publish(self, topic: str, message: dict) -> str:
        """Publish a message. Returns the broker-assigned message ID."""
        ...

    def consume(self, topic: str, count: int = 1, block_ms: int = 5000) -> list[tuple[str, dict]]:
        """Pull-style consume. Returns up to ``count`` (id, payload) pairs,
        blocking up to ``block_ms`` for messages to arrive."""
        ...

    def ack(self, topic: str, message_id: str) -> None:
        """Acknowledge / delete a previously consumed message."""
        ...

    def extend_visibility(self, topic: str, message_id: str, seconds: int) -> None:
        """Extend the message's invisibility window. No-op on backends that
        don't support it (Redis Streams, RabbitMQ)."""
        ...


class RedisStreamQueue:
    """Redis Streams with consumer groups. Caravan emits this for oss-local
    queue/redis-streams (default) and queue/redis variants."""

    def __init__(self, url: str, consumer_group: str = "caravan-workers") -> None:
        try:
            import redis
        except ImportError as e:
            raise ImportError(
                "RedisStreamQueue requires redis. Install `caravan-rpc[redis]`."
            ) from e
        self._client = redis.Redis.from_url(url, decode_responses=True)
        self._consumer_group = consumer_group
        self._consumer_name = f"worker-{uuid.uuid4().hex[:8]}"
        self._initialized_groups: set[str] = set()

    def _ensure_group(self, topic: str) -> None:
        if topic in self._initialized_groups:
            return
        import redis as _redis

        try:
            self._client.xgroup_create(topic, self._consumer_group, id="0", mkstream=True)
        except _redis.ResponseError as e:
            if "BUSYGROUP" not in str(e):
                raise
        self._initialized_groups.add(topic)

    def publish(self, topic: str, message: dict) -> str:
        self._ensure_group(topic)
        msg_id: str = self._client.xadd(topic, {"data": json.dumps(message)})
        return msg_id

    def consume(self, topic: str, count: int = 1, block_ms: int = 5000) -> list[tuple[str, dict]]:
        self._ensure_group(topic)
        results = self._client.xreadgroup(
            groupname=self._consumer_group,
            consumername=self._consumer_name,
            streams={topic: ">"},
            count=count,
            block=block_ms,
        )
        if not results:
            return []
        out: list[tuple[str, dict]] = []
        for _stream, entries in results:
            for msg_id, fields in entries:
                out.append((msg_id, json.loads(fields["data"])))
        return out

    def ack(self, topic: str, message_id: str) -> None:
        self._client.xack(topic, self._consumer_group, message_id)

    def extend_visibility(self, topic: str, message_id: str, seconds: int) -> None:
        # Redis Streams have no visibility timeout — pending entries stay
        # claimed by the consumer until XACK or manual XCLAIM by reaper.
        return None


class RabbitMQQueue:
    """RabbitMQ via pika (synchronous channel). Caravan emits this for
    oss-local queue/rabbitmq composition."""

    def __init__(self, url: str) -> None:
        try:
            import pika  # noqa: F401 — checked at construction; actual import lazy in _channel_
        except ImportError as e:
            raise ImportError(
                "RabbitMQQueue requires pika. Install `caravan-rpc[rabbit]`."
            ) from e
        scheme = urlparse(url).scheme
        if scheme not in ("amqp", "amqps"):
            raise ValueError(
                f"RabbitMQQueue expects amqp://... or amqps://...; got scheme {scheme!r}"
            )
        self._url = url
        self._connection = None
        self._channel = None
        self._declared_queues: set[str] = set()

    @classmethod
    def from_url(cls, url: str) -> RabbitMQQueue:
        return cls(url)

    def _channel_(self):
        if self._channel is not None and not self._channel.is_closed:
            return self._channel
        import pika

        params = pika.URLParameters(self._url)
        self._connection = pika.BlockingConnection(params)
        self._channel = self._connection.channel()
        return self._channel

    def _ensure_queue(self, topic: str) -> None:
        if topic in self._declared_queues:
            return
        ch = self._channel_()
        ch.queue_declare(queue=topic, durable=True)
        self._declared_queues.add(topic)

    def publish(self, topic: str, message: dict) -> str:
        import pika

        self._ensure_queue(topic)
        ch = self._channel_()
        msg_id = uuid.uuid4().hex
        ch.basic_publish(
            exchange="",
            routing_key=topic,
            body=json.dumps(message).encode(),
            properties=pika.BasicProperties(
                message_id=msg_id, delivery_mode=2, content_type="application/json"
            ),
        )
        return msg_id

    def consume(self, topic: str, count: int = 1, block_ms: int = 5000) -> list[tuple[str, dict]]:
        self._ensure_queue(topic)
        ch = self._channel_()
        deadline = time.monotonic() + block_ms / 1000.0
        out: list[tuple[str, dict]] = []
        while len(out) < count:
            method, _props, body = ch.basic_get(queue=topic, auto_ack=False)
            if method is None:
                if time.monotonic() >= deadline:
                    break
                time.sleep(0.05)
                continue
            out.append((str(method.delivery_tag), json.loads(body.decode())))
        return out

    def ack(self, topic: str, message_id: str) -> None:
        ch = self._channel_()
        ch.basic_ack(delivery_tag=int(message_id))

    def extend_visibility(self, topic: str, message_id: str, seconds: int) -> None:
        # RabbitMQ has no SQS-style visibility timeout; unacked messages
        # requeue when the channel closes.
        return None


class SqsQueue:
    """AWS SQS via boto3. Caravan emits this for cloud-managed queue
    composition (M4-cloud hybrid-dev and onward).

    SQS is single-queue-per-URL — the ``topic:`` parameter on publish/consume
    is ignored. Each ``aws_sqs_queue`` Caravan emits gets its own SqsQueue
    instance via ``from_url``.
    """

    def __init__(self, queue_url: str) -> None:
        try:
            import boto3
        except ImportError as e:
            raise ImportError(
                "SqsQueue requires boto3. Install `caravan-rpc[aws]`."
            ) from e
        self._queue_url = queue_url
        self._client = boto3.client("sqs")

    @classmethod
    def from_url(cls, url: str) -> SqsQueue:
        scheme = urlparse(url).scheme
        if scheme not in ("http", "https"):
            raise ValueError(
                f"SqsQueue expects an https://sqs.<region>.amazonaws.com/... URL; got scheme {scheme!r}"
            )
        return cls(url)

    @classmethod
    def from_env(cls) -> SqsQueue:
        url = os.environ.get("QUEUE_URL")
        if not url:
            raise ValueError("SqsQueue.from_env requires QUEUE_URL to be set")
        return cls.from_url(url)

    def publish(self, topic: str, message: dict) -> str:
        # topic ignored — single-queue substrate.
        resp = self._client.send_message(
            QueueUrl=self._queue_url, MessageBody=json.dumps(message)
        )
        return resp["MessageId"]

    def consume(self, topic: str, count: int = 1, block_ms: int = 5000) -> list[tuple[str, dict]]:
        wait_time = max(0, min(20, block_ms // 1000))  # SQS long-poll 0-20s.
        resp = self._client.receive_message(
            QueueUrl=self._queue_url,
            MaxNumberOfMessages=min(count, 10),  # SQS hard cap.
            WaitTimeSeconds=wait_time,
        )
        out: list[tuple[str, dict]] = []
        for m in resp.get("Messages", []):
            out.append((m["ReceiptHandle"], json.loads(m["Body"])))
        return out

    def ack(self, topic: str, message_id: str) -> None:
        # message_id is the ReceiptHandle from consume.
        self._client.delete_message(QueueUrl=self._queue_url, ReceiptHandle=message_id)

    def extend_visibility(self, topic: str, message_id: str, seconds: int) -> None:
        self._client.change_message_visibility(
            QueueUrl=self._queue_url,
            ReceiptHandle=message_id,
            VisibilityTimeout=seconds,
        )
