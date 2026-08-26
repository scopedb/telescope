/*
 * Copyright 2026 ScopeDB, Inc.
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *     http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 */

package scopedbexporter

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestUnixNanoStringToRFC3339(t *testing.T) {
	assert.Equal(t, "2024-04-23T01:23:45.123456789Z", unixNanoStringToRFC3339("1713835425123456789"))
	assert.Equal(t, "", unixNanoStringToRFC3339(""))
	assert.Equal(t, "", unixNanoStringToRFC3339("not-a-number"))
}
