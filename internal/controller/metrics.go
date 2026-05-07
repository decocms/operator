/*
Copyright 2025.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package controller

import (
	"github.com/prometheus/client_golang/prometheus"
	"sigs.k8s.io/controller-runtime/pkg/metrics"
)

const (
	metricsNamespace       = "deco_operator"
	metricsSubsystemValkey = "valkey"
)

var (
	// cfworkersBuildDuration tracks how long each build took (seconds), labelled by site, status, and build type.
	cfworkersBuildDuration = prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Namespace: metricsNamespace,
		Subsystem: "cfworkers",
		Name:      "build_duration_seconds",
		Help:      "Duration of cfworkers build jobs in seconds.",
		Buckets:   []float64{30, 60, 120, 180, 300, 600, 900},
	}, []string{"site", "status", "type"}) // type: production | preview

	// cfworkersBuildTotal counts completed builds by site, status, and type.
	cfworkersBuildTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: metricsNamespace,
		Subsystem: "cfworkers",
		Name:      "builds_total",
		Help:      "Total number of cfworkers builds completed.",
	}, []string{"site", "status", "type"}) // type: production | preview

	// valkeyACLProvisioned counts successful ACL user + Secret provisioning operations.
	valkeyACLProvisioned = prometheus.NewCounter(prometheus.CounterOpts{
		Namespace: metricsNamespace,
		Subsystem: metricsSubsystemValkey,
		Name:      "acl_provisioned_total",
		Help:      "Total number of Valkey ACL users provisioned (new or re-provisioned).",
	})

	// valkeyACLDeleted counts ACL user deletions triggered by namespace removal.
	valkeyACLDeleted = prometheus.NewCounter(prometheus.CounterOpts{
		Namespace: metricsNamespace,
		Subsystem: metricsSubsystemValkey,
		Name:      "acl_deleted_total",
		Help:      "Total number of Valkey ACL users deleted on namespace removal.",
	})

	// valkeyACLErrors counts failures when interacting with Valkey.
	valkeyACLErrors = prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: metricsNamespace,
		Subsystem: metricsSubsystemValkey,
		Name:      "acl_errors_total",
		Help:      "Total number of Valkey ACL operation errors by operation type.",
	}, []string{"operation"}) // operation: upsert | delete | check

	// valkeyACLSelfHealed counts how many times an ACL was re-created after Valkey restart.
	valkeyACLSelfHealed = prometheus.NewCounter(prometheus.CounterOpts{
		Namespace: metricsNamespace,
		Subsystem: metricsSubsystemValkey,
		Name:      "acl_self_healed_total",
		Help:      "Total number of Valkey ACL users re-provisioned after being lost (e.g. Valkey restart).",
	})

	// valkeyTenantsProvisioned tracks the current number of provisioned tenants (gauge).
	valkeyTenantsProvisioned = prometheus.NewGauge(prometheus.GaugeOpts{
		Namespace: metricsNamespace,
		Subsystem: metricsSubsystemValkey,
		Name:      "tenants_provisioned",
		Help:      "Current number of site namespaces with a provisioned Valkey ACL user.",
	})

	// valkeySentinelFailovers counts Sentinel +switch-master events received.
	// Each event triggers an immediate full ACL resync to all nodes.
	valkeySentinelFailovers = prometheus.NewCounter(prometheus.CounterOpts{
		Namespace: metricsNamespace,
		Subsystem: metricsSubsystemValkey,
		Name:      "sentinel_failovers_total",
		Help:      "Total number of Sentinel master failovers detected via +switch-master pub/sub.",
	})
)

// RecordSentinelFailover increments the sentinel_failovers_total counter.
// Called from main.go when a +switch-master event is received.
func RecordSentinelFailover() {
	valkeySentinelFailovers.Inc()
}

// RecordBuild emits duration and count metrics when a build job completes.
func RecordBuild(site, status, buildType string, durationSeconds float64) {
	cfworkersBuildDuration.WithLabelValues(site, status, buildType).Observe(durationSeconds)
	cfworkersBuildTotal.WithLabelValues(site, status, buildType).Inc()
}

func init() {
	metrics.Registry.MustRegister(
		cfworkersBuildDuration,
		cfworkersBuildTotal,
		valkeyACLProvisioned,
		valkeyACLDeleted,
		valkeyACLErrors,
		valkeyACLSelfHealed,
		valkeyTenantsProvisioned,
		valkeySentinelFailovers,
	)
}
