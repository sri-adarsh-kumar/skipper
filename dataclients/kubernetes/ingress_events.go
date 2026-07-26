package kubernetes

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	log "github.com/sirupsen/logrus"

	"github.com/zalando/skipper/dataclients/kubernetes/definitions"
)

const (
	ingressEventInterval = 30 * time.Minute
	ingressEventTimeout  = 5 * time.Second

	invalidIngressReason          = "InvalidIngress"
	routeNotCreatedReason         = "RouteNotCreated"
	invalidTLSConfigurationReason = "InvalidTLSConfiguration"

	invalidIngressNote = "invalid Skipper Ingress annotation"
)

type ingressDiagnostic struct {
	namespace  string
	name       string
	uid        string
	reason     string
	note       string
	occurrence string
}

func ingressDiagnosticForMetadata(metadata *definitions.Metadata, reason, note, occurrence string) ingressDiagnostic {
	if metadata == nil {
		return ingressDiagnostic{reason: reason, note: note, occurrence: occurrence}
	}

	return ingressDiagnostic{
		namespace:  metadata.Namespace,
		name:       metadata.Name,
		uid:        metadata.Uid,
		reason:     reason,
		note:       note,
		occurrence: occurrence,
	}
}

type ingressEventState struct {
	lastAttempt time.Time
	lastSuccess time.Time
}

type ingressEventReporter struct {
	client   *clusterClient
	instance string
	now      func() time.Time
	timeout  time.Duration

	mu          sync.Mutex
	active      map[string]ingressEventState
	reconcileMu sync.Mutex
}

func newIngressEventReporter(client *clusterClient) *ingressEventReporter {
	instance, err := os.Hostname()
	if err != nil || instance == "" {
		instance = "skipper"
	}

	return &ingressEventReporter{
		client:   client,
		instance: instance,
		now:      time.Now,
		timeout:  ingressEventTimeout,
		active:   make(map[string]ingressEventState),
	}
}

func (r *ingressEventReporter) reconcile(diagnostics []ingressDiagnostic) {
	r.reconcileMu.Lock()
	defer r.reconcileMu.Unlock()

	now := r.now()
	observed := make(map[string]ingressDiagnostic, len(diagnostics))
	for _, diagnostic := range diagnostics {
		if !validIngressDiagnostic(diagnostic) {
			continue
		}
		diagnostic.note = sanitizeIngressEventNote(diagnostic.note)
		observed[ingressDiagnosticKey(diagnostic)] = diagnostic
	}

	r.mu.Lock()
	for key := range r.active {
		if _, ok := observed[key]; !ok {
			delete(r.active, key)
		}
	}

	toPublish := make([]struct {
		key        string
		diagnostic ingressDiagnostic
	}, 0, len(observed))
	for key, diagnostic := range observed {
		state, ok := r.active[key]
		if ok && now.Sub(state.lastAttempt) < ingressEventInterval {
			continue
		}
		state.lastAttempt = now
		r.active[key] = state
		toPublish = append(toPublish, struct {
			key        string
			diagnostic ingressDiagnostic
		}{key: key, diagnostic: diagnostic})
	}
	r.mu.Unlock()

	for _, event := range toPublish {
		if err := r.publish(event.diagnostic, now); err != nil {
			log.WithFields(log.Fields{
				"namespace": event.diagnostic.namespace,
				"name":      event.diagnostic.name,
				"reason":    event.diagnostic.reason,
			}).Errorf("failed to create ingress event: %v", err)
			continue
		}

		r.mu.Lock()
		if state, ok := r.active[event.key]; ok {
			state.lastSuccess = now
			r.active[event.key] = state
		}
		r.mu.Unlock()
	}
}

func validIngressDiagnostic(diagnostic ingressDiagnostic) bool {
	return diagnostic.namespace != "" && diagnostic.name != "" && diagnostic.uid != "" && diagnostic.reason != ""
}

func ingressDiagnosticKey(diagnostic ingressDiagnostic) string {
	sum := sha256.Sum256([]byte(strings.Join([]string{
		diagnostic.uid,
		diagnostic.namespace,
		diagnostic.name,
		diagnostic.reason,
		diagnostic.occurrence,
		diagnostic.note,
	}, "\x00")))
	return hex.EncodeToString(sum[:])
}

func sanitizeIngressEventNote(note string) string {
	note = strings.ToValidUTF8(note, "?")
	note = strings.NewReplacer("\r\n", " ", "\r", " ", "\n", " ").Replace(note)
	if len(note) <= 1024 {
		return note
	}

	var result strings.Builder
	result.Grow(1024)
	for _, character := range note {
		if result.Len()+utf8.RuneLen(character) > 1024 {
			break
		}
		result.WriteRune(character)
	}
	return result.String()
}

func (r *ingressEventReporter) publish(diagnostic ingressDiagnostic, eventTime time.Time) error {
	payload, err := json.Marshal(ingressEvent{
		APIVersion: "events.k8s.io/v1",
		Kind:       "Event",
		Metadata: ingressEventMetadata{
			GenerateName: "skipper-ingress-",
			Namespace:    diagnostic.namespace,
		},
		Type:                "Warning",
		Reason:              diagnostic.reason,
		Note:                diagnostic.note,
		Action:              "RouteReconciliationFailed",
		EventTime:           eventTime.UTC(),
		ReportingController: "skipper.zalando.org/ingress-controller",
		ReportingInstance:   r.instance,
		Regarding: ingressEventRegarding{
			APIVersion: "networking.k8s.io/v1",
			Kind:       "Ingress",
			Namespace:  diagnostic.namespace,
			Name:       diagnostic.name,
			UID:        diagnostic.uid,
		},
	})
	if err != nil {
		return err
	}

	uri := "/apis/events.k8s.io/v1/namespaces/" + url.PathEscape(diagnostic.namespace) + "/events"
	req, err := r.client.createRequestWithMethod(http.MethodPost, uri, bytes.NewReader(payload))
	if err != nil {
		return err
	}

	ctx, cancel := context.WithTimeout(req.Context(), r.timeout)
	defer cancel()
	req = req.WithContext(ctx)
	req.Header.Set("Content-Type", "application/json")

	response, err := r.client.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer response.Body.Close()

	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return fmt.Errorf("event request to %s failed, status: %d, %s", uri, response.StatusCode, response.Status)
	}

	return nil
}

type ingressEvent struct {
	APIVersion          string                `json:"apiVersion"`
	Kind                string                `json:"kind"`
	Metadata            ingressEventMetadata  `json:"metadata"`
	Type                string                `json:"type"`
	Reason              string                `json:"reason"`
	Note                string                `json:"note"`
	Action              string                `json:"action"`
	EventTime           time.Time             `json:"eventTime"`
	ReportingController string                `json:"reportingController"`
	ReportingInstance   string                `json:"reportingInstance"`
	Regarding           ingressEventRegarding `json:"regarding"`
}

type ingressEventMetadata struct {
	GenerateName string `json:"generateName"`
	Namespace    string `json:"namespace"`
}

type ingressEventRegarding struct {
	APIVersion string `json:"apiVersion"`
	Kind       string `json:"kind"`
	Namespace  string `json:"namespace"`
	Name       string `json:"name"`
	UID        string `json:"uid"`
}
