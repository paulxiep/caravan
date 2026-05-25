"""Caravan-shipped resource seams + concrete impls.

Resource adapters (BlobStore, MessageQueue) are part of caravan-rpc rather
than per-user-repo code. User repos import the seam interface, call
``client(BlobStore).put(...)``, and let caravan auto-register the right impl
at startup via ``auto_register_resources()``.

The thesis: every caravan user writes only ``@wagon`` / ``provide`` /
``client`` against their own domain seams; resource adapters ship from
caravan so a new user doesn't have to hand-author boto3 wrappers / SQS
clients / RabbitMQ channels.

Optional extras gate the heavy deps:

  pip install caravan-rpc[aws]    # boto3 for S3 + SQS
  pip install caravan-rpc[redis]  # redis-py for cache + Redis Streams queue
  pip install caravan-rpc[rabbit] # pika for RabbitMQ

The seam classes (``BlobStore``, ``MessageQueue``) load with no extras —
only the concrete impls demand their respective backend libs at construction
time.
"""

from __future__ import annotations

from .auto_register import auto_register_resources
from .blob_store import BlobStore, LocalFsBlobStore, S3BlobStore
from .queue import (
    MessageQueue,
    RabbitMQQueue,
    RedisStreamQueue,
    SqsQueue,
)

__all__ = [
    "BlobStore",
    "LocalFsBlobStore",
    "MessageQueue",
    "RabbitMQQueue",
    "RedisStreamQueue",
    "S3BlobStore",
    "SqsQueue",
    "auto_register_resources",
]
