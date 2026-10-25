# Connect your telemetry

Start Telescope with a table and mapping for each signal you want to receive; the [quickstart](quickstart.md) enables traces. Then choose the path that matches your environment.

| You already have… | Connect it this way |
| --- | --- |
| An application instrumented with OpenTelemetry | Set its OTLP exporter endpoint to Telescope. |
| An OpenTelemetry Collector or Grafana Alloy pipeline | Add an OTLP exporter targeting Telescope; keep collection and processing there. |
| File logs, Prometheus targets, or another wire format | Collect or convert them with an upstream agent, then forward OTLP. |
| A custom Go Collector distribution | Register [the ScopeDB exporter](exporter.md) directly. |

## From an application

Configure an installed OpenTelemetry SDK or auto-instrumentation agent for OTLP/HTTP:

```bash
export OTEL_SERVICE_NAME=my-service
export OTEL_EXPORTER_OTLP_ENDPOINT=http://127.0.0.1:4318
export OTEL_EXPORTER_OTLP_PROTOCOL=http/protobuf
```

Restart the application and generate traffic. These variables configure an existing exporter; they do not instrument an application by themselves. For a new application, follow the [OpenTelemetry guide for your language](https://opentelemetry.io/docs/languages/). Environment-variable support varies by SDK.

Use Telescope's reachable hostname when it runs on another host or in a container. The base HTTP endpoint has no `/v1/traces` suffix; a signal-specific variable such as `OTEL_EXPORTER_OTLP_TRACES_ENDPOINT` needs the full `http://telescope:4318/v1/traces` URL. For gRPC, use port `4317` and protocol `grpc`. See the [OTLP exporter configuration reference](https://opentelemetry.io/docs/languages/sdk-configuration/otlp-exporter/).

## From an existing Collector

Add this exporter to the Collector that already receives your telemetry, and include `otlp_http/telescope` in the relevant pipeline's `exporters` list:

```yaml
exporters:
  otlp_http/telescope:
    endpoint: http://telescope:4318
```

For example, an existing traces pipeline using the `otlp` receiver and `batch` processor becomes:

```yaml
service:
  pipelines:
    traces:
      receivers: [otlp]
      processors: [batch]
      exporters: [otlp_http/telescope]
```

Merge these fragments into your existing configuration, retaining its receiver and processor definitions. Add the exporter to logs or metrics pipelines only after enabling those signals in Telescope. Older Collector versions may name the component `otlphttp`; check your distribution's `components` output and [configuration reference](https://opentelemetry.io/docs/collector/configuration/).

Keep file tailing, scraping, enrichment, filtering, and sampling in that Collector. If you retain another exporter, the pipeline sends to both destinations. For Alloy, use its [OpenTelemetry forwarding guide](https://grafana.com/docs/alloy/latest/collect/opentelemetry-data/) with Telescope as the destination.

## Confirm application delivery

Run `telescope status` before and after generating application traffic. A rising `received` count confirms reception; `written` confirms ScopeDB append acknowledgement. Allow for the batch timeout. Use `telescope query` to inspect stored fields, or [capture and preview a sample](operations.md#capture-and-preview) when the application arrives but its mapping needs work.

`telescope verify` tests the transport with synthetic input; it does not exercise your application's attributes. Telescope's default OTLP listeners use plaintext on a trusted network. Use a TLS/authentication proxy or a custom Collector configuration across a trust boundary.
