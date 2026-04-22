package vendordbexporter

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestScopeDBRowsProjectsTimestampColumns(t *testing.T) {
	payload := &IngestPayload{
		SchemaVersion: "v1",
		Signal:        signalLogs,
		Dataset:       "demo",
		Records: []Record{
			{
				"timestamp_unix_nano":          "1713835425123456789",
				"observed_timestamp_unix_nano": "1713835426123456789",
				"resource": map[string]any{
					"service.name": "collector-a",
				},
			},
			{
				"start_time_unix_nano": "1713835427123456789",
				"end_time_unix_nano":   "1713835428123456789",
			},
			{
				"start_timestamp_unix_nano": "1713835429123456789",
			},
		},
	}

	rows := payload.scopeDBRows()
	if assert.Len(t, rows, 3) {
		assert.Equal(t, "2024-04-23T01:23:45.123456789Z", rows[0]["record_timestamp"])
		assert.Equal(t, "2024-04-23T01:23:46.123456789Z", rows[0]["observed_timestamp"])
		assert.Equal(t, "collector-a", rows[0]["service_name"])
		assert.Equal(t, "2024-04-23T01:23:47.123456789Z", rows[1]["start_timestamp"])
		assert.Equal(t, "2024-04-23T01:23:48.123456789Z", rows[1]["end_timestamp"])
		assert.Equal(t, "2024-04-23T01:23:49.123456789Z", rows[2]["start_timestamp"])
	}
}

func TestUnixNanoStringToRFC3339(t *testing.T) {
	assert.Equal(t, "2024-04-23T01:23:45.123456789Z", unixNanoStringToRFC3339("1713835425123456789"))
	assert.Equal(t, "", unixNanoStringToRFC3339(""))
	assert.Equal(t, "", unixNanoStringToRFC3339("not-a-number"))
}
