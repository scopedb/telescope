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

"""Check that every advertised deployment setting reaches the container."""

import json
import os
from pathlib import Path
import re
import subprocess
import tempfile

root = Path(__file__).resolve().parent.parent
keys = re.findall(r"^#?(TELESCOPE_[A-Z_]+)=", (root / "deploy/.env.example").read_text(), re.M)
values = {key: f"compose-check-{index}" for index, key in enumerate(keys)}
with tempfile.TemporaryDirectory(prefix="telescope-compose-") as directory:
    env_file = Path(directory) / ".env"
    env_file.write_text("".join(f"{key}={value}\n" for key, value in values.items()))
    # An inherited variable would override --env-file and hide broken wiring.
    environment = {key: value for key, value in os.environ.items() if key not in values}
    result = subprocess.run(
        ["docker", "compose", "--env-file", str(env_file), "-f", str(root / "deploy/docker-compose.yaml"), "config", "--format", "json"],
        env=environment, check=True, text=True, capture_output=True,
    )
    actual = json.loads(result.stdout)["services"]["telescope"]["environment"]
    for key, value in values.items():
        if actual.get(key) != value:
            raise SystemExit(f"{key} from deploy/.env.example is not passed to the container")
print(f"Compose forwards all {len(values)} documented settings")
