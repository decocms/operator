#!/bin/bash

kubectx gke_gke-cluster-453314_us-east1_sites

make docker-buildx IMG=ghcr.io/decocms/operator:latest

kubectl rollout restart deployment/decocms-operator-controller-manager -n decocms-operator

kubectx arn:aws:eks:sa-east-1:578348582779:cluster/eks-cluster-eksCluster-ea385ba

kubectl rollout restart deployment/decocms-operator-controller-manager -n decocms-operator
