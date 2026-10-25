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

"""Build portable operator archives from the tracked distribution file set."""

from pathlib import Path
import os
import subprocess
import sys
import tarfile
import tempfile

root = Path(__file__).resolve().parent.parent
dist = Path(sys.argv[1]).resolve()
version, ldflags = sys.argv[2:4]
files = subprocess.check_output(
    ["git", "ls-files", "--", "LICENSE", "README.md", "CONTRIBUTING.md", "docs", "deploy"],
    cwd=root, text=True,
).splitlines()


def portable_metadata(info):
    # Do not distribute build-machine usernames, extended attributes, or paths.
    info.uid = info.gid = 0
    info.uname = info.gname = ""
    info.pax_headers = {}
    return info


for target in sys.argv[4:]:
    goos, goarch = target.split("/")
    name = f"telescope_{version}_{goos}_{goarch}"
    print(f"Building {name}", flush=True)
    with tempfile.TemporaryDirectory(prefix="telescope-build-") as scratch:
        binary = Path(scratch) / "telescope"
        environment = os.environ | {"GOOS": goos, "GOARCH": goarch, "CGO_ENABLED": "0"}
        subprocess.run(
            ["go", "build", "-trimpath", "-ldflags", ldflags, "-o", str(binary), "./cmd/telescope"],
            cwd=root, env=environment, check=True,
        )
        with tarfile.open(dist / f"{name}.tar.gz", "w:gz", format=tarfile.USTAR_FORMAT) as archive:
            archive.add(binary, arcname=f"{name}/telescope", filter=portable_metadata)
            for path in files:
                archive.add(root / path, arcname=f"{name}/{path}", filter=portable_metadata)
