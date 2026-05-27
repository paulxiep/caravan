"""Tests for caravan_rpc.resources — BlobStore + MessageQueue seams,
LocalFs impl, env-var auto-registration routing.

Tests that need backend libs (boto3 / redis / pika) are skipped when those
libs aren't installed; the seam classes themselves load with no extras.
"""

from __future__ import annotations

import os
import tempfile
from unittest import mock

import pytest

from caravan_rpc import (
    BlobStore,
    LocalFsBlobStore,
    MessageQueue,
    auto_register_resources,
    client,
    provide,
)
from caravan_rpc import _registry


@pytest.fixture(autouse=True)
def clear_registry():
    """Ensure each test starts from an empty provide() registry."""
    _registry.clear()
    yield
    _registry.clear()


# --- BlobStore seam shape ---------------------------------------------------


def test_blob_store_is_wagon():
    assert getattr(BlobStore, "__caravan_wagon__", False)


def test_blob_store_method_signatures_captured():
    assert set(BlobStore.__caravan_methods__.keys()) == {
        "put",
        "get",
        "exists",
        "delete",
    }


def test_message_queue_is_wagon():
    assert getattr(MessageQueue, "__caravan_wagon__", False)


def test_message_queue_method_signatures_captured():
    assert set(MessageQueue.__caravan_methods__.keys()) == {
        "publish",
        "consume",
        "ack",
        "extend_visibility",
    }


# --- LocalFsBlobStore roundtrip --------------------------------------------


def test_localfs_put_get_roundtrip():
    with tempfile.TemporaryDirectory() as tmp:
        store = LocalFsBlobStore(tmp)
        store.put("hello.txt", b"caravan")
        assert store.exists("hello.txt")
        assert store.get("hello.txt") == b"caravan"
        store.delete("hello.txt")
        assert not store.exists("hello.txt")


def test_localfs_blocks_path_traversal():
    with tempfile.TemporaryDirectory() as tmp:
        store = LocalFsBlobStore(tmp)
        with pytest.raises(ValueError, match="Path traversal"):
            store.put("../escape.txt", b"x")


def test_localfs_dispatches_via_client():
    """End-to-end via the @wagon proxy."""
    with tempfile.TemporaryDirectory() as tmp:
        provide(BlobStore, LocalFsBlobStore(tmp))
        client(BlobStore).put("x.bin", b"42")
        assert client(BlobStore).get("x.bin") == b"42"


# --- auto_register_resources routing ---------------------------------------


def test_auto_register_no_env_no_fallback_skips():
    """No env vars + no fallback → no impl registered. First client call
    should raise LookupError with a clear seam-name in the message."""
    with mock.patch.dict(os.environ, {}, clear=True):
        auto_register_resources(yaml_fallback=None)
    with pytest.raises(LookupError, match="BlobStore"):
        client(BlobStore).put("nope.txt", b"x")


def test_auto_register_yaml_fallback_local_fs():
    with tempfile.TemporaryDirectory() as tmp:
        with mock.patch.dict(os.environ, {}, clear=True):
            auto_register_resources(
                yaml_fallback={"blob_storage": {"type": "local_fs", "base_path": tmp}}
            )
        # Should resolve to LocalFs via the fallback.
        client(BlobStore).put("yfb.txt", b"hi")
        assert client(BlobStore).get("yfb.txt") == b"hi"


def test_auto_register_env_overrides_yaml():
    """When CARAVAN_BLOB_BACKEND=s3 marker + S3_BUCKET are present, the
    explicit-marker path wins over the yaml fallback's local_fs.
    Skipped if boto3 isn't installed because S3BlobStore.from_env() builds
    a real boto3 client at construction time."""
    boto3 = pytest.importorskip("boto3")
    del boto3  # only needed to gate the test
    with mock.patch.dict(
        os.environ,
        {
            "CARAVAN_BLOB_BACKEND": "s3",
            "S3_BUCKET": "test-bucket-name",
            "AWS_REGION": "us-east-1",
        },
        clear=True,
    ):
        # Should pick the marker path (S3BlobStore), not the yaml local_fs.
        auto_register_resources(
            yaml_fallback={"blob_storage": {"type": "local_fs", "base_path": "/tmp"}}
        )
    # We don't actually call put — that'd hit AWS. Just verify the
    # registered impl is the S3 one.
    from caravan_rpc.resources.blob_store import S3BlobStore

    assert isinstance(_registry.lookup(BlobStore), S3BlobStore)


def test_auto_register_blob_backend_s3_missing_bucket_loud_fails():
    """CARAVAN_BLOB_BACKEND=s3 with no S3_BUCKET must raise rather than
    silently fall through to LocalFs. Catches the "user forgot
    .env.hybrid" cloud footgun at startup."""
    with mock.patch.dict(
        os.environ,
        {"CARAVAN_BLOB_BACKEND": "s3"},
        clear=True,
    ):
        with pytest.raises(ValueError, match=r"CARAVAN_BLOB_BACKEND=s3 but S3_BUCKET"):
            auto_register_resources(yaml_fallback=None)


def test_auto_register_blob_backend_local_fs_skips_minio():
    """CARAVAN_BLOB_BACKEND=local-fs selects LocalFs even when MinIO
    env vars (S3_ENDPOINT_URL + AWS_*) are also set. Mirrors caravan's
    oss-local emit pattern: MinIO container present but unused."""
    with mock.patch.dict(
        os.environ,
        {
            "CARAVAN_BLOB_BACKEND": "local-fs",
            "S3_ENDPOINT_URL": "http://minio:9000",
            "AWS_ACCESS_KEY_ID": "minioadmin",
            "AWS_SECRET_ACCESS_KEY": "minioadmin",
        },
        clear=True,
    ):
        auto_register_resources(
            yaml_fallback={"blob_storage": {"type": "local_fs", "base_path": "/data/blobs"}}
        )
    from caravan_rpc.resources.blob_store import LocalFsBlobStore

    assert isinstance(_registry.lookup(BlobStore), LocalFsBlobStore)


def test_auto_register_queue_yaml_redis_stream():
    """YAML fallback path for redis-streams. Skipped if redis isn't
    installed (the impl constructor imports it)."""
    pytest.importorskip("redis")
    with mock.patch.dict(os.environ, {}, clear=True):
        auto_register_resources(
            yaml_fallback={
                "queue": {
                    "type": "redis_stream",
                    "url": "redis://localhost:6379",
                    "consumer_group": "tests",
                }
            }
        )
    from caravan_rpc.resources.queue import RedisStreamQueue

    assert isinstance(_registry.lookup(MessageQueue), RedisStreamQueue)


def test_auto_register_queue_yaml_sqs_requires_env():
    """queue.type=sqs in YAML without QUEUE_URL env should raise — the
    cloud-managed SQS path is env-driven by tofu output."""
    with mock.patch.dict(os.environ, {}, clear=True):
        with pytest.raises(ValueError, match="QUEUE_URL"):
            auto_register_resources(yaml_fallback={"queue": {"type": "sqs"}})


def test_auto_register_queue_env_unsupported_scheme():
    with mock.patch.dict(os.environ, {"QUEUE_URL": "ftp://nope"}, clear=True):
        with pytest.raises(ValueError, match="not supported"):
            auto_register_resources()
