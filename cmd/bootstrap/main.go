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

// bootstrap is a one-shot CLI that annotates existing site Namespaces so the
// NamespaceReconciler picks them up and provisions Valkey ACL credentials.
//
// Usage:
//
//	go run ./cmd/bootstrap --namespace-pattern sites-
//	go run ./cmd/bootstrap --namespace-pattern sites- --dry-run
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"strings"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	_ "k8s.io/client-go/plugin/pkg/client/auth"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
)

const valkeyACLAnnotation = "deco.sites/valkey-acl"

func main() {
	var namespacePattern string
	var dryRun bool

	flag.StringVar(&namespacePattern, "namespace-pattern", "sites-",
		"Prefix used to filter Namespaces eligible for Valkey ACL provisioning.")
	flag.BoolVar(&dryRun, "dry-run", false,
		"Print which Namespaces would be annotated without making changes.")

	opts := zap.Options{Development: true}
	opts.BindFlags(flag.CommandLine)
	flag.Parse()

	ctrl.SetLogger(zap.New(zap.UseFlagOptions(&opts)))
	log := ctrl.Log.WithName("bootstrap")

	scheme := runtime.NewScheme()
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))

	cfg, err := ctrl.GetConfig()
	if err != nil {
		log.Error(err, "Failed to get kubeconfig")
		os.Exit(1)
	}

	k8s, err := client.New(cfg, client.Options{Scheme: scheme})
	if err != nil {
		log.Error(err, "Failed to create Kubernetes client")
		os.Exit(1)
	}

	ctx := context.Background()

	nsList := &corev1.NamespaceList{}
	if err := k8s.List(ctx, nsList); err != nil {
		log.Error(err, "Failed to list Namespaces")
		os.Exit(1)
	}

	annotated, skipped := 0, 0
	for i := range nsList.Items {
		ns := &nsList.Items[i]

		if !strings.HasPrefix(ns.Name, namespacePattern) {
			continue
		}

		if ns.Annotations[valkeyACLAnnotation] == "true" {
			log.Info("Already annotated, skipping", "namespace", ns.Name)
			skipped++
			continue
		}

		if dryRun {
			fmt.Printf("[dry-run] would annotate namespace %s\n", ns.Name)
			annotated++
			continue
		}

		patch := client.MergeFrom(ns.DeepCopy())
		if ns.Annotations == nil {
			ns.Annotations = make(map[string]string)
		}
		ns.Annotations[valkeyACLAnnotation] = "true"
		if err := k8s.Patch(ctx, ns, patch); err != nil {
			log.Error(err, "Failed to annotate namespace", "namespace", ns.Name)
			continue
		}
		log.Info("Annotated namespace", "namespace", ns.Name)
		annotated++
	}

	log.Info("Bootstrap complete", "annotated", annotated, "skipped", skipped)
}
