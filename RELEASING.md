## For k8s manifests only

- change manifests
- run `make generate`
- get a valid GITHUB_TOKEN
- run GITHUB_TOKEN=xpto ./upgrade_helm.sh

## For changes in operator itself

- run ./publish.sh