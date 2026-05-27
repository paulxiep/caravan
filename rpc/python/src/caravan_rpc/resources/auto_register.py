"""auto_register_resources — one-line resource bootstrap.

A user's process startup (worker entry, CLI main()) calls
``auto_register_resources()`` once. caravan-rpc reads its own env vars
(injected by ``caravan compile``), picks the right impl per seam, and calls
``provide()`` so subsequent ``client(BlobStore)`` / ``client(MessageQueue)``
dispatches resolve.

Env-var routing (matches what the caravan compiler emits in
``internal/compiler/resource_endpoints.go``):

  BlobStore:
    S3_ENDPOINT_URL set        → S3BlobStore against MinIO (oss-local)
    S3_BUCKET set, no endpoint → S3BlobStore against real AWS (cloud-managed)
    neither                    → LocalFsBlobStore from yaml_fallback hint
                                  (non-Caravan local-dev path); skipped if
                                  the hint doesn't describe a blob store

  MessageQueue:
    QUEUE_URL scheme:
      redis://    → RedisStreamQueue
      amqp://     → RabbitMQQueue
      https://    → SqsQueue (cloud-managed)
    none        → YAML-fallback dispatch; skipped if hint absent

The ``yaml_fallback`` dict is the user's parsed application config, passed
verbatim. caravan-rpc reads a known sub-shape:

    {
        "blob_storage": {"type": "local_fs", "base_path": "..."} | {"type": "s3", "bucket": "...", "region": "..."},
        "queue":        {"type": "redis_stream", "url": "..."} | {"type": "rabbitmq", "url": "..."},
    }

Unrecognized keys are ignored. Fallback is best-effort — when neither env
vars nor fallback yield a usable impl, the seam stays unregistered and the
first ``client(BlobStore).put(...)`` raises ``LookupError`` with a clear
message.
"""

from __future__ import annotations

import os
from typing import Any
from urllib.parse import urlparse

from .. import provide
from .blob_store import BlobStore, LocalFsBlobStore, S3BlobStore
from .queue import MessageQueue, RabbitMQQueue, RedisStreamQueue, SqsQueue


def auto_register_resources(yaml_fallback: dict | None = None) -> None:
    """Read env vars + optional yaml fallback; call ``provide()`` for each
    resource seam caravan-rpc can satisfy. Idempotent within one process —
    re-registering replaces the prior impl (last-write-wins, matches
    ``provide()``'s own semantics).
    """
    _register_blob_store(yaml_fallback)
    _register_message_queue(yaml_fallback)


def _register_blob_store(fallback: dict | None) -> None:
    # Caravan-emitted explicit marker: `CARAVAN_BLOB_BACKEND` declares
    # which impl the caller's compose/HCL emit intends. With backend=s3
    # the SDK asserts S3_BUCKET is set — catches the "user forgot
    # .env.hybrid from tofu output" footgun loudly at startup instead of
    # silently masquerading cloud-managed as LocalFs. With backend=local-fs
    # the SDK skips S3 entirely (oss-local "MinIO emitted but skipped").
    # No marker → fall through to yaml_fallback (non-caravan local dev).
    backend = os.environ.get("CARAVAN_BLOB_BACKEND")
    if backend == "s3":
        if not os.environ.get("S3_BUCKET"):
            raise ValueError(
                "CARAVAN_BLOB_BACKEND=s3 but S3_BUCKET is unset; "
                "did you forget to populate .env.hybrid from `tofu output -json`? "
                "(`caravan up` auto-generates it.)"
            )
        provide(BlobStore, S3BlobStore.from_env())
        return
    if backend == "local-fs":
        base = _yaml_blob_base(fallback) or "/data/blobs"
        provide(BlobStore, LocalFsBlobStore(base))
        return

    # No marker — non-caravan local dev path; consult yaml_fallback only.
    if not fallback:
        return
    blob = fallback.get("blob_storage")
    if not isinstance(blob, dict):
        return
    storage_type = blob.get("type")
    if storage_type == "local_fs":
        base = blob.get("base_path")
        if base:
            provide(BlobStore, LocalFsBlobStore(base))
    elif storage_type == "s3":
        bucket = blob.get("bucket")
        if not bucket:
            return
        provide(
            BlobStore,
            S3BlobStore(bucket=bucket, region=blob.get("region")),
        )


def _yaml_blob_base(fallback: dict | None) -> str | None:
    """Pull `blob_storage.base_path` from the user's YAML fallback config,
    if present and the type is local_fs. Returns None otherwise.
    """
    if not fallback:
        return None
    blob = fallback.get("blob_storage")
    if not isinstance(blob, dict):
        return None
    if blob.get("type") != "local_fs":
        return None
    base = blob.get("base_path")
    return base if isinstance(base, str) and base else None


def _register_message_queue(fallback: dict | None) -> None:
    env_url = os.environ.get("QUEUE_URL", "")
    if env_url:
        scheme = urlparse(env_url).scheme
        consumer_group = _consumer_group_from_fallback(fallback)
        impl: Any
        if scheme in ("redis", "rediss"):
            impl = RedisStreamQueue(env_url, consumer_group)
        elif scheme in ("amqp", "amqps"):
            impl = RabbitMQQueue.from_url(env_url)
        elif scheme in ("http", "https"):
            impl = SqsQueue.from_url(env_url)
        else:
            raise ValueError(f"QUEUE_URL scheme {scheme!r} not supported")
        provide(MessageQueue, impl)
        return

    # YAML fallback.
    if not fallback:
        return
    queue = fallback.get("queue")
    if not isinstance(queue, dict):
        return
    qtype = queue.get("type")
    consumer_group = queue.get("consumer_group", "caravan-workers")
    if qtype == "redis_stream":
        url = queue.get("url")
        if url:
            provide(MessageQueue, RedisStreamQueue(url, consumer_group))
    elif qtype == "rabbitmq":
        url = queue.get("url")
        if url:
            provide(MessageQueue, RabbitMQQueue(url))
    elif qtype == "sqs":
        # SQS via YAML hint: requires QUEUE_URL env (set by Caravan from
        # tofu output). Documented; no silent fallback.
        raise ValueError(
            "queue.type=sqs requires QUEUE_URL env var (caravan emits it from tofu output)"
        )


def _consumer_group_from_fallback(fallback: dict | None) -> str:
    if not fallback:
        return "caravan-workers"
    queue = fallback.get("queue")
    if isinstance(queue, dict):
        return queue.get("consumer_group", "caravan-workers")
    return "caravan-workers"
