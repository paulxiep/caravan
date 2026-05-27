"""BlobStore seam + LocalFs/S3 impls.

The ``BlobStore`` seam is ``@wagon``-decorated so callers dispatch through
``client(BlobStore).put(...)`` and caravan can swap the impl per yaml
composition (oss-local → S3 against MinIO; cloud-managed → S3 against real
AWS; LocalFs for non-Caravan local dev).

Path validation is intentionally NOT in this module — user-repo domain logic
(e.g. invoice-parse's tenant_id/job_id UUID convention) lives in user code
above the seam boundary. This adapter only knows how to put/get/exists/delete
opaque byte blobs keyed by string paths.
"""

from __future__ import annotations

import os
from pathlib import Path

from .. import wagon


@wagon
class BlobStore:
    """Caravan-owned seam for opaque blob storage.

    Methods are intentionally narrow — put/get/exists/delete by string path.
    Anything richer (range reads, multipart uploads, signed URLs) is out of
    scope for the PoC; users that need them subclass the impl or build above
    the seam.
    """

    def put(self, path: str, data: bytes) -> None: ...

    def get(self, path: str) -> bytes: ...

    def exists(self, path: str) -> bool: ...

    def delete(self, path: str) -> None: ...


class LocalFsBlobStore:
    """Filesystem-backed blob store. Used by oss-local targets that don't
    spin up MinIO + by the no-Caravan local-dev fallback."""

    def __init__(self, base_path: str | Path) -> None:
        self._base = Path(base_path).resolve()
        self._base.mkdir(parents=True, exist_ok=True)

    def _full(self, path: str) -> Path:
        # Naive path-traversal guard; user-side validators do the
        # domain-specific checks (tenant_id/job_id segments etc.).
        if ".." in path:
            raise ValueError(f"Path traversal in blob path: {path}")
        return (self._base / path.lstrip("/")).resolve()

    def put(self, path: str, data: bytes) -> None:
        full = self._full(path)
        full.parent.mkdir(parents=True, exist_ok=True)
        full.write_bytes(data)

    def get(self, path: str) -> bytes:
        return self._full(path).read_bytes()

    def exists(self, path: str) -> bool:
        return self._full(path).exists()

    def delete(self, path: str) -> None:
        full = self._full(path)
        if full.exists():
            full.unlink()


class S3BlobStore:
    """S3-protocol blob store via boto3.

    Same code path serves MinIO (compose, ``endpoint_url`` set) and real AWS
    S3 (cloud, ``endpoint_url`` unset → boto3 default endpoint resolution
    plus profile-resolved creds via the mounted ``~/.aws``).

    Requires the optional ``caravan-rpc[aws]`` extra (pulls in boto3).
    """

    def __init__(
        self,
        bucket: str,
        endpoint_url: str | None = None,
        access_key_id: str | None = None,
        secret_access_key: str | None = None,
        region: str | None = None,
    ) -> None:
        try:
            import boto3
        except ImportError as e:  # pragma: no cover — exercised in deploy
            raise ImportError(
                "S3BlobStore requires boto3. Install `caravan-rpc[aws]`."
            ) from e

        kwargs: dict = {}
        if endpoint_url:
            kwargs["endpoint_url"] = endpoint_url
        if access_key_id:
            kwargs["aws_access_key_id"] = access_key_id
        if secret_access_key:
            kwargs["aws_secret_access_key"] = secret_access_key
        if region:
            kwargs["region_name"] = region
        self._bucket = bucket
        self._client = boto3.client("s3", **kwargs)
        # Best-effort bucket create — MinIO needs it; real AWS rejects
        # with AccessDenied on a pre-existing bucket, which we ignore.
        try:
            self._client.create_bucket(Bucket=bucket)
        except Exception:  # noqa: BLE001 — bucket exists or no perm; both fine
            pass

    @classmethod
    def from_env(cls) -> S3BlobStore:
        """Construct from Caravan-injected env vars.

        Reads ``S3_BUCKET`` (required), ``S3_ENDPOINT_URL`` (set → MinIO mode;
        unset → real AWS), ``AWS_ACCESS_KEY_ID`` / ``AWS_SECRET_ACCESS_KEY``
        (set → static creds; unset → AWS resolution chain picks up profile
        from mounted ~/.aws), ``AWS_REGION``.
        """
        bucket = os.environ.get("S3_BUCKET")
        if not bucket:
            raise ValueError("S3BlobStore.from_env requires S3_BUCKET to be set")
        return cls(
            bucket=bucket,
            endpoint_url=os.environ.get("S3_ENDPOINT_URL"),
            access_key_id=os.environ.get("AWS_ACCESS_KEY_ID"),
            secret_access_key=os.environ.get("AWS_SECRET_ACCESS_KEY"),
            region=os.environ.get("AWS_REGION"),
        )

    def put(self, path: str, data: bytes) -> None:
        key = path.lstrip("/")
        self._client.put_object(Bucket=self._bucket, Key=key, Body=data)

    def get(self, path: str) -> bytes:
        key = path.lstrip("/")
        resp = self._client.get_object(Bucket=self._bucket, Key=key)
        return resp["Body"].read()

    def exists(self, path: str) -> bool:
        key = path.lstrip("/")
        try:
            self._client.head_object(Bucket=self._bucket, Key=key)
            return True
        except self._client.exceptions.ClientError:
            return False

    def delete(self, path: str) -> None:
        key = path.lstrip("/")
        self._client.delete_object(Bucket=self._bucket, Key=key)
