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

var (
	// valkeyACLProvisioned counts successful ACL user + Secret provisioning operations.
	valkeyACLProvisioned = prometheus.NewCounter(prometheus.CounterOpts{
		Namespace: "deco_operator",
		Subsystem: "valkey",
		Name:      "acl_provisioned_total",
		Help:      "Total number of Valkey ACL users provisioned (new or re-provisioned).",
	})

	// valkeyACLDeleted counts ACL user deletions triggered by namespace removal.
	valkeyACLDeleted = prometheus.NewCounter(prometheus.CounterOpts{
		Namespace: "deco_operator",
		Subsystem: "valkey",
		Name:      "acl_deleted_total",
		Help:      "Total number of Valkey ACL users deleted on namespace removal.",
	})

	// valkeyACLErrors counts failures when interacting with Valkey.
	valkeyACLErrors = prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: "deco_operator",
		Subsystem: "valkey",
		Name:      "acl_errors_total",
		Help:      "Total number of Valkey ACL operation errors by operation type.",
	}, []string{"operation"}) // operation: upsert | delete | check

	// valkeyACLSelfHealed counts how many times an ACL was re-created after Valkey restart.
	valkeyACLSelfHealed = prometheus.NewCounter(prometheus.CounterOpts{
		Namespace: "deco_operator",
		Subsystem: "valkey",
		Name:      "acl_self_healed_total",
		Help:      "Total number of Valkey ACL users re-provisioned after being lost (e.g. Valkey restart).",
	})

	// valkeyTenantsProvisioned tracks the current number of provisioned tenants (gauge).
	valkeyTenantsProvisioned = prometheus.NewGauge(prometheus.GaugeOpts{
		Namespace: "deco_operator",
		Subsystem: "valkey",
		Name:      "tenants_provisioned",
		Help:      "Current number of site namespaces with a provisioned Valkey ACL user.",
	})

	// valkeySentinelFailovers counts Sentinel +switch-master events received.
	// Each event triggers an immediate full ACL resync to all nodes.
	valkeySentinelFailovers = prometheus.NewCounter(prometheus.CounterOpts{
		Namespace: "deco_operator",
		Subsystem: "valkey",
		Name:      "sentinel_failovers_total",
		Help:      "Total number of Sentinel master failovers detected via +switch-master pub/sub.",
	})
)

// RecordSentinelFailover increments the sentinel_failovers_total counter.
// Called from main.go when a +switch-master event is received.
func RecordSentinelFailover() {
	valkeySentinelFailovers.Inc()
}

func init() {
	metrics.Registry.MustRegister(
		valkeyACLProvisioned,
		valkeyACLDeleted,
		valkeyACLErrors,
		valkeyACLSelfHealed,
		valkeyTenantsProvisioned,
		valkeySentinelFailovers,
	)
}
