package scopedbexporter

import "go.opentelemetry.io/collector/pdata/plog"

func mapLogs(cfg *Config, logs plog.Logs) (*IngestPayload, error) {
	payload := newPayload(cfg, signalLogs)

	resourceLogs := logs.ResourceLogs()
	for i := 0; i < resourceLogs.Len(); i++ {
		resourceLog := resourceLogs.At(i)
		resource := attributesToMap(resourceLog.Resource().Attributes())

		scopeLogs := resourceLog.ScopeLogs()
		for j := 0; j < scopeLogs.Len(); j++ {
			scopeLog := scopeLogs.At(j)
			scope := scopeToMap(scopeLog.Scope())

			logRecords := scopeLog.LogRecords()
			for k := 0; k < logRecords.Len(); k++ {
				record := logRecords.At(k)
				body := valueToAny(record.Body())
				payload.Records = append(payload.Records, Record{
					"timestamp_unix_nano":          timestampString(record.Timestamp()),
					"observed_timestamp_unix_nano": timestampString(record.ObservedTimestamp()),
					"trace_id":                     traceIDString(record.TraceID()),
					"span_id":                      spanIDString(record.SpanID()),
					"severity_text":                record.SeverityText(),
					"severity_number":              int(record.SeverityNumber()),
					"body":                         body,
					"message":                      messageString(body),
					"resource":                     resource,
					"scope":                        scope,
					"attributes":                   attributesToMap(record.Attributes()),
				})
			}
		}
	}

	return payload, nil
}
