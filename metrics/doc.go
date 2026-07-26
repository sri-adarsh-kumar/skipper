/*
Package metrics implements collection of common performance metrics in Coda
Hale, Prometheus, and OpenTelemetry (OTel) formats.

For CodaHale format it uses the Go implementation of the Go Coda Hale metrics library:

https://github.com/dropwizard/metrics
https://github.com/rcrowley/go-metrics

For Prometheus format, it uses Prometheus Go official client library:

https://github.com/prometheus/client_golang

OTel exports metrics periodically with OTLP/HTTP. It does not register an HTTP
metrics handler by itself.

The collected metrics include detailed information about Skipper's relevant processes while serving requests -
looking up routes, filters (aggregate and individual), backend communication and forwarding the response to
the client.

# Options

Options configure metric collection. The Skipper CLI mounts the handler on its
support listener; library callers create it with NewHandler and mount the
returned http.Handler themselves.

You can define a custom Prefix to every reported metrics key. This allows you to avoid conflicts between Skipper's
metrics and other systems if you aggregate them later in some monitoring system. The command-line default prefix is
"skipper.". A bare library Options value leaves Coda Hale keys unprefixed, but
Prometheus and OTel use their `skipper` fallback namespace.

You can also enable some Go garbage collector and runtime metrics using EnableDebugGcMetrics and EnableRuntimeMetrics,
respectively.

# REST API

This listener accepts GET requests on the /metrics endpoint like any other REST API. A request to "/metrics" returns
JSON including all collected metrics when Coda Hale is used, or Prometheus exposition text when Prometheus is used.
Many metrics are created lazily whenever a request triggers them. Consequently, inactive Coda Hale returns 404, while
Prometheus returns 200 when its registered collectors have metrics.

If you use CodaHale format you can also query for specific metrics, individually or by prefix matching. You can either use the metrics key name
and you should get back only the values for that particular key or a prefix in which case you should get all the
metrics that share the same prefix. If you request an unknown key or prefix the response will be an HTTP 404.

Prometheus returns the whole registry, including for /metrics/<suffix>; select
series in PromQL instead. When Coda Hale and Prometheus are combined,
Accept: application/codahale+json selects Coda Hale and Prometheus is the
default response.

See https://opensource.zalando.com/skipper/operation/operation/#monitoring for
the complete endpoint, metric, and configuration reference.
*/
package metrics
