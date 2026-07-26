package kubernetes

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/stretchr/testify/require"

	"github.com/zalando/skipper/secrets"
	"github.com/zalando/skipper/secrets/certregistry"
)

func testIngressDiagnostic(reason, note, occurrence string) ingressDiagnostic {
	return ingressDiagnostic{
		namespace:  "payments",
		name:       "app",
		uid:        "ingress-uid",
		reason:     reason,
		note:       note,
		occurrence: occurrence,
	}
}

func TestIngressEventPublication(t *testing.T) {
	for _, tt := range []struct {
		name       string
		diagnostic ingressDiagnostic
		wantPost   bool
		wantReason string
		wantNote   string
	}{
		{
			name:       "invalid annotation",
			diagnostic: testIngressDiagnostic(invalidIngressReason, invalidIngressNote, "validation"),
			wantPost:   true,
			wantReason: invalidIngressReason,
			wantNote:   invalidIngressNote,
		},
		{
			name:       "route omission multiline note",
			diagnostic: testIngressDiagnostic(routeNotCreatedReason, "service not found\nwhile creating route", "rule:0:path:0"),
			wantPost:   true,
			wantReason: routeNotCreatedReason,
			wantNote:   "service not found while creating route",
		},
		{
			name:       "tls failure long multibyte note",
			diagnostic: testIngressDiagnostic(invalidTLSConfigurationReason, strings.Repeat("é", 1025), "tls:0"),
			wantPost:   true,
			wantReason: invalidTLSConfigurationReason,
		},
		{
			name: "incomplete metadata",
			diagnostic: ingressDiagnostic{
				reason:     routeNotCreatedReason,
				note:       "service not found",
				occurrence: "rule:0:path:0",
			},
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			var events []ingressEvent
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				require.Equal(t, http.MethodPost, r.Method)
				require.Equal(t, "/apis/events.k8s.io/v1/namespaces/payments/events", r.URL.Path)
				require.Equal(t, "application/json", r.Header.Get("Content-Type"))
				defer r.Body.Close()
				var event ingressEvent
				require.NoError(t, json.NewDecoder(r.Body).Decode(&event))
				events = append(events, event)
				w.WriteHeader(http.StatusCreated)
			}))
			defer server.Close()

			reporter := newIngressEventReporter(&clusterClient{httpClient: server.Client(), apiURL: server.URL})
			reporter.instance = "skipper-test"
			reporter.reconcile([]ingressDiagnostic{tt.diagnostic})

			if !tt.wantPost {
				require.Empty(t, events)
				return
			}

			require.Len(t, events, 1)
			event := events[0]
			require.Equal(t, "events.k8s.io/v1", event.APIVersion)
			require.Equal(t, "Event", event.Kind)
			require.Equal(t, "Warning", event.Type)
			require.Equal(t, tt.wantReason, event.Reason)
			require.Equal(t, "RouteReconciliationFailed", event.Action)
			require.Equal(t, "skipper.zalando.org/ingress-controller", event.ReportingController)
			require.Equal(t, "skipper-test", event.ReportingInstance)
			require.Equal(t, "networking.k8s.io/v1", event.Regarding.APIVersion)
			require.Equal(t, "Ingress", event.Regarding.Kind)
			require.Equal(t, "payments", event.Regarding.Namespace)
			require.Equal(t, "app", event.Regarding.Name)
			require.Equal(t, "ingress-uid", event.Regarding.UID)
			require.LessOrEqual(t, len(event.Note), 1024)
			require.True(t, utf8.ValidString(event.Note))
			require.NotContains(t, event.Note, "\n")
			require.NotContains(t, event.Note, "zalando.org/skipper")
			require.NotContains(t, event.Note, "parser error")
			if tt.wantNote != "" {
				require.Equal(t, tt.wantNote, event.Note)
			}
		})
	}
}

func TestInvalidIngressDiagnostic(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, IngressesV1ClusterURI, r.URL.Path)
		_, err := w.Write([]byte(`{"items":[{"metadata":{"namespace":"payments","name":"app","uid":"ingress-uid","annotations":{"zalando.org/skipper-routes":"r1: Header(\"X-Region\") -> <shunt>"}},"spec":{"rules":[]}}]}`))
		require.NoError(t, err)
	}))
	defer server.Close()

	client, err := newClusterClient(Options{}, server.URL, defaultIngressClass, defaultRouteGroupClass, make(chan struct{}))
	require.NoError(t, err)
	_, diagnostics, err := client.loadIngressesV1()
	require.NoError(t, err)
	require.Equal(t, []ingressDiagnostic{testIngressDiagnostic(invalidIngressReason, invalidIngressNote, "validation")}, diagnostics)
}

func TestIngressEventReporterDisabled(t *testing.T) {
	client, err := newClusterClient(Options{}, "http://localhost", defaultIngressClass, defaultRouteGroupClass, make(chan struct{}))
	require.NoError(t, err)
	require.Nil(t, client.ingressEvents)
}

func TestIngressEventReconciliationDiagnostics(t *testing.T) {
	for _, tt := range []struct {
		name       string
		enabled    bool
		ingresses  string
		services   bool
		registry   bool
		wantRoutes int
		wantReason string
	}{
		{
			name:       "invalid annotation",
			enabled:    true,
			ingresses:  `[{"metadata":{"namespace":"payments","name":"app","uid":"invalid","annotations":{"zalando.org/skipper-routes":"r1: Header(\"X-Region\") -> <shunt>"}},"spec":{"rules":[]}}]`,
			wantReason: invalidIngressReason,
		},
		{
			name:       "route omission",
			enabled:    true,
			ingresses:  `[{"metadata":{"namespace":"payments","name":"app","uid":"missing-service"},"spec":{"rules":[{"host":"app.example.org","http":{"paths":[{"path":"/","pathType":"Prefix","backend":{"service":{"name":"missing","port":{"number":80}}}}]}}]}}]`,
			wantReason: routeNotCreatedReason,
		},
		{
			name:       "default backend omission",
			enabled:    true,
			ingresses:  `[{"metadata":{"namespace":"payments","name":"app","uid":"missing-default"},"spec":{"defaultBackend":{"service":{"name":"missing","port":{"number":80}}},"rules":[]}}]`,
			wantReason: routeNotCreatedReason,
		},
		{
			name:       "tls configuration",
			enabled:    true,
			services:   true,
			registry:   true,
			ingresses:  `[{"metadata":{"namespace":"payments","name":"app","uid":"tls"},"spec":{"rules":[{"host":"app.example.org","http":{"paths":[{"path":"/","pathType":"Prefix","backend":{"service":{"name":"svc","port":{"number":80}}}}]}}],"tls":[{"hosts":["other.example.org"],"secretName":"missing"}]}}]`,
			wantRoutes: 1,
			wantReason: invalidTLSConfigurationReason,
		},
		{
			name:      "disabled",
			ingresses: `[{"metadata":{"namespace":"payments","name":"app","uid":"missing-service"},"spec":{"rules":[{"host":"app.example.org","http":{"paths":[{"path":"/","pathType":"Prefix","backend":{"service":{"name":"missing","port":{"number":80}}}}]}}]}}]`,
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			var registry *certregistry.CertRegistry
			if tt.registry {
				registry = certregistry.NewCertRegistry()
			}
			client, events := newIngressEventClient(t, tt.enabled, tt.ingresses, tt.services, registry, http.StatusCreated)
			routes, err := client.loadAndConvert()
			require.NoError(t, err)
			require.Len(t, routes, tt.wantRoutes)
			if !tt.enabled {
				require.Empty(t, *events)
				return
			}
			require.Len(t, *events, 1)
			require.Equal(t, tt.wantReason, (*events)[0].Reason)
		})
	}
}

func TestIngressEventDeliveryFailurePreservesRoutes(t *testing.T) {
	ingresses := `[
{"metadata":{"namespace":"payments","name":"invalid","uid":"invalid","annotations":{"zalando.org/skipper-routes":"r1: Header(\"X-Region\") -> <shunt>"}},"spec":{"rules":[]}},
{"metadata":{"namespace":"payments","name":"valid","uid":"valid"},"spec":{"rules":[{"host":"app.example.org","http":{"paths":[{"path":"/","pathType":"Prefix","backend":{"service":{"name":"svc","port":{"number":80}}}}]}}]}}
]`
	client, events := newIngressEventClient(t, true, ingresses, true, nil, http.StatusInternalServerError)
	routes, err := client.loadAndConvert()
	require.NoError(t, err)
	require.Len(t, routes, 1)
	require.Len(t, *events, 1)
}

func newIngressEventClient(t *testing.T, enabled bool, ingresses string, services bool, registry *certregistry.CertRegistry, eventStatus int) (*Client, *[]ingressEvent) {
	t.Helper()
	events := make([]ingressEvent, 0)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method + " " + r.URL.Path {
		case http.MethodGet + " " + IngressesV1ClusterURI:
			_, _ = w.Write([]byte(`{"items":` + ingresses + `}`))
		case http.MethodGet + " " + ZalandoResourcesClusterURI:
			_, _ = w.Write([]byte(`{"resources":[]}`))
		case http.MethodGet + " " + ServicesClusterURI:
			if services {
				_, _ = w.Write([]byte(`{"items":[{"metadata":{"namespace":"payments","name":"svc"},"spec":{"ports":[{"port":80,"targetPort":8080}]}}]}`))
				return
			}
			_, _ = w.Write([]byte(`{"items":[]}`))
		case http.MethodGet + " " + EndpointsClusterURI, http.MethodGet + " " + SecretsClusterURI:
			_, _ = w.Write([]byte(`{"items":[]}`))
		case http.MethodPost + " /apis/events.k8s.io/v1/namespaces/payments/events":
			defer r.Body.Close()
			var event ingressEvent
			require.NoError(t, json.NewDecoder(r.Body).Decode(&event))
			events = append(events, event)
			w.WriteHeader(eventStatus)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(server.Close)

	client, err := New(Options{
		KubernetesURL:                 server.URL,
		KubernetesEnableIngressEvents: enabled,
		ForceKubernetesService:        true,
		CertificateRegistry:           registry,
	})
	require.NoError(t, err)
	t.Cleanup(client.Close)
	return client, &events
}

func TestIngressEventReconciliation(t *testing.T) {
	var posts atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		posts.Add(1)
		w.WriteHeader(http.StatusCreated)
	}))
	defer server.Close()

	now := time.Date(2026, time.July, 26, 12, 0, 0, 0, time.UTC)
	reporter := newIngressEventReporter(&clusterClient{httpClient: server.Client(), apiURL: server.URL})
	reporter.now = func() time.Time { return now }
	first := testIngressDiagnostic(routeNotCreatedReason, "service not found", "rule:0:path:0")
	second := testIngressDiagnostic(routeNotCreatedReason, "service not found", "rule:0:path:1")

	reporter.reconcile([]ingressDiagnostic{first})
	require.Equal(t, int32(1), posts.Load())
	reporter.reconcile([]ingressDiagnostic{first})
	require.Equal(t, int32(1), posts.Load())
	reporter.reconcile([]ingressDiagnostic{first, second})
	require.Equal(t, int32(2), posts.Load())

	now = now.Add(ingressEventInterval)
	reporter.reconcile([]ingressDiagnostic{first, second})
	require.Equal(t, int32(4), posts.Load())
	reporter.reconcile(nil)
	reporter.reconcile([]ingressDiagnostic{first})
	require.Equal(t, int32(5), posts.Load())
}

func TestIngressEventDeliveryFailure(t *testing.T) {
	for _, tt := range []struct {
		name      string
		client    func(*httptest.Server) *http.Client
		status    int
		wait      bool
		wantPosts int32
	}{
		{name: "forbidden", status: http.StatusForbidden, wantPosts: 2},
		{name: "server error", status: http.StatusInternalServerError, wantPosts: 2},
		{name: "transport error", client: func(*httptest.Server) *http.Client {
			return &http.Client{Transport: roundTripperFunc(func(*http.Request) (*http.Response, error) { return nil, errors.New("transport failed") })}
		}},
		{name: "timeout", wait: true, wantPosts: 2},
	} {
		t.Run(tt.name, func(t *testing.T) {
			var posts atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				posts.Add(1)
				require.Equal(t, "application/json", r.Header.Get("Content-Type"))
				require.Equal(t, "Bearer event-token", r.Header.Get("Authorization"))
				if tt.wait {
					time.Sleep(100 * time.Millisecond)
					return
				}
				w.WriteHeader(tt.status)
			}))
			defer server.Close()

			client := server.Client()
			if tt.client != nil {
				client = tt.client(server)
			}
			cluster := &clusterClient{
				httpClient:    client,
				apiURL:        server.URL,
				tokenProvider: testSecretsProvider{},
				tokenFile:     "event-token",
			}
			reporter := newIngressEventReporter(cluster)
			now := time.Date(2026, time.July, 26, 12, 0, 0, 0, time.UTC)
			reporter.now = func() time.Time { return now }
			if tt.wait {
				reporter.timeout = 10 * time.Millisecond
			}
			diagnostic := testIngressDiagnostic(routeNotCreatedReason, "service not found", "rule:0:path:0")
			reporter.reconcile([]ingressDiagnostic{diagnostic})
			reporter.reconcile([]ingressDiagnostic{diagnostic})
			now = now.Add(ingressEventInterval - time.Second)
			reporter.reconcile([]ingressDiagnostic{diagnostic})
			now = now.Add(time.Second)
			reporter.reconcile([]ingressDiagnostic{diagnostic})
			require.Equal(t, tt.wantPosts, posts.Load())
		})
	}
}

func TestIngressEventReporterConcurrency(t *testing.T) {
	var posts atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		posts.Add(1)
		w.WriteHeader(http.StatusCreated)
	}))
	defer server.Close()

	reporter := newIngressEventReporter(&clusterClient{httpClient: server.Client(), apiURL: server.URL})
	diagnostic := testIngressDiagnostic(routeNotCreatedReason, "service not found", "rule:0:path:0")
	var waitGroup sync.WaitGroup
	for range 20 {
		waitGroup.Add(1)
		go func() {
			defer waitGroup.Done()
			reporter.reconcile([]ingressDiagnostic{diagnostic})
		}()
	}
	waitGroup.Wait()
	require.Equal(t, int32(1), posts.Load())
}

type roundTripperFunc func(*http.Request) (*http.Response, error)

func (f roundTripperFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return f(request)
}

type testSecretsProvider struct{}

func (testSecretsProvider) Add(string) error {
	return nil
}

func (testSecretsProvider) GetSecret(string) ([]byte, bool) {
	return []byte("event-token"), true
}

func (testSecretsProvider) Close() {}

var _ secrets.SecretsProvider = testSecretsProvider{}
