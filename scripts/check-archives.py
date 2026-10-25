#!/usr/bin/env python3
# Copyright 2026 ScopeDB, Inc.
#
# Licensed under the Apache License, Version 2.0 (the "License");
# you may not use this file except in compliance with the License.
# You may obtain a copy of the License at
#
#     http://www.apache.org/licenses/LICENSE-2.0
#
# Unless required by applicable law or agreed to in writing, software
# distributed under the License is distributed on an "AS IS" BASIS,
# WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
# See the License for the specific language governing permissions and
# limitations under the License.

"""Verify the release file set, local documentation links, and native binary."""

import hashlib
from pathlib import Path
import platform
import re
import subprocess
import sys
import tarfile
import tempfile

root = Path(__file__).resolve().parent.parent
dist = Path(sys.argv[1]).resolve()
version = sys.argv[2]
files = set(subprocess.check_output(
    ["git", "ls-files", "--", "LICENSE", "README.md", "CONTRIBUTING.md", "docs", "deploy"],
    cwd=root, text=True,
).splitlines()) | {"telescope"}
checksums = {}
for line in (dist / "SHA256SUMS").read_text().splitlines():
    digest, name = line.split()
    checksums[name] = digest
archives = sorted(dist.glob("*.tar.gz"))
if not archives or set(checksums) != {archive.name for archive in archives}:
    raise SystemExit("Archive/checksum file sets differ or are empty")
native_os = {"Darwin": "darwin", "Linux": "linux"}.get(platform.system())
native_arch = {"arm64": "arm64", "aarch64": "arm64", "x86_64": "amd64"}.get(platform.machine())
for archive in archives:
    if hashlib.sha256(archive.read_bytes()).hexdigest() != checksums[archive.name]:
        raise SystemExit(f"Checksum mismatch: {archive.name}")
    directory = archive.name.removesuffix(".tar.gz")
    with tempfile.TemporaryDirectory(prefix="telescope-archive-") as scratch:
        with tarfile.open(archive) as tar:
            actual = {member.name for member in tar.getmembers() if member.isfile()}
            expected = {f"{directory}/{name}" for name in files}
            if actual != expected:
                raise SystemExit(f"Unexpected archive contents: {archive.name}: {actual ^ expected}")
            tar.extractall(scratch, filter="data")
        unpacked = Path(scratch) / directory
        for document in unpacked.rglob("*.md"):
            for target in re.findall(r"\[[^\]]*\]\(([^)]+)\)", document.read_text()):
                if "://" in target or target.startswith("#"):
                    continue
                path = target.split("#", 1)[0]
                if path and not (document.parent / path).exists():
                    raise SystemExit(f"Broken archive link: {document.relative_to(unpacked)} -> {target}")
        if archive.name.endswith(f"_{native_os}_{native_arch}.tar.gz"):
            binary = str(unpacked / "telescope")
            actual_version = subprocess.check_output([binary, "version"], text=True).strip()
            if actual_version != version:
                raise SystemExit(f"Binary version mismatch: {actual_version}")
            for config in ["telescope.example.yaml", "telescope.v0.2-compatible.yaml"]:
                subprocess.run([binary, "validate", "--offline", f"deploy/{config}"], cwd=unpacked, check=True, stdout=subprocess.DEVNULL)
            subprocess.run([binary, "preview", "--offline", "--sample", "traces=deploy/samples/traces.otlp.json", "deploy/telescope.v0.2-compatible.yaml"], cwd=unpacked, check=True, stdout=subprocess.DEVNULL)
    print(f"Verified {archive.name}")
