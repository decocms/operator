package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"

	"gopkg.in/yaml.v3"
)

type K8sResource struct {
	APIVersion string `yaml:"apiVersion"`
	Kind       string `yaml:"kind"`
	Metadata   struct {
		Name string `yaml:"name"`
	} `yaml:"metadata"`
}

func main() {
	// Get project root from environment or calculate it
	projectRoot := os.Getenv("PROJECT_ROOT")
	if projectRoot == "" {
		wd, _ := os.Getwd()
		projectRoot = wd
	}
	templatesDir := filepath.Join(projectRoot, "chart/templates")

	fmt.Println("Generating Helm chart from Kustomize manifests...")

	// Clean old templates
	fmt.Println("Cleaning old templates...")
	cleanTemplates(templatesDir)

	// Build Kustomize manifests
	fmt.Println("Building Kustomize manifests...")
	kustomizeOutput, err := buildKustomize(projectRoot)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error building kustomize: %v\n", err)
		os.Exit(1)
	}

	// Split and process documents
	fmt.Println("Processing manifests...")
	documents := splitYAMLDocuments(kustomizeOutput)
	fileCount := 0

	for _, doc := range documents {
		if len(strings.TrimSpace(doc)) < 20 {
			continue
		}

		// Parse to get kind and name
		var resource K8sResource
		if err := yaml.Unmarshal([]byte(doc), &resource); err != nil {
			continue
		}

		if resource.Kind == "" || resource.Metadata.Name == "" {
			continue
		}

		// Create filename
		kind := strings.ToLower(resource.Kind)
		name := resource.Metadata.Name
		filename := fmt.Sprintf("%s-%s.yaml", kind, name)
		filepath := filepath.Join(templatesDir, filename)

		// Apply template substitutions
		content := applySubstitutions(doc)

		// Add conditionals
		content = addConditionals(content, kind)

		// Write file
		if err := os.WriteFile(filepath, []byte(content), 0644); err != nil {
			fmt.Fprintf(os.Stderr, "Error writing %s: %v\n", filename, err)
			continue
		}

		fileCount++
	}

	// Add env vars to deployment
	if err := addEnvVarsToDeployment(templatesDir); err != nil {
		fmt.Fprintf(os.Stderr, "Warning: Could not add env vars to deployment: %v\n", err)
	}

	// Add builder ServiceAccount template (conditional on build.serviceAccount)
	if err := addBuilderServiceAccount(templatesDir); err != nil {
		fmt.Fprintf(os.Stderr, "Warning: Could not add builder service account: %v\n", err)
	}

	// Add podAnnotations to deployment pod template
	if err := addPodAnnotations(templatesDir); err != nil {
		fmt.Fprintf(os.Stderr, "Warning: Could not add pod annotations to deployment: %v\n", err)
	}

	// Add redirect infrastructure templates
	if err := addRedirectNamespace(templatesDir); err != nil {
		fmt.Fprintf(os.Stderr, "Warning: Could not add redirect namespace: %v\n", err)
	}
	if err := addClusterIssuer(templatesDir); err != nil {
		fmt.Fprintf(os.Stderr, "Warning: Could not add ClusterIssuer: %v\n", err)
	}
	if err := addRedirectControllerArgs(templatesDir); err != nil {
		fmt.Fprintf(os.Stderr, "Warning: Could not add redirect controller args: %v\n", err)
	}

	fmt.Printf("✓ Generated %d Helm templates\n\n", fileCount)
	fmt.Println("Test with:")
	fmt.Println("  make helm-lint")
	fmt.Println("  make helm-template")
	fmt.Println("")
}

func cleanTemplates(dir string) {
	files, _ := filepath.Glob(filepath.Join(dir, "*.yaml"))
	for _, file := range files {
		if !strings.HasSuffix(file, "_helpers.tpl") {
			os.Remove(file)
		}
	}
}

func buildKustomize(projectRoot string) (string, error) {
	cmd := exec.Command(filepath.Join(projectRoot, "bin/kustomize"), "build", "config/default")
	cmd.Dir = projectRoot
	output, err := cmd.Output()
	if err != nil {
		return "", err
	}
	return string(output), nil
}

func splitYAMLDocuments(input string) []string {
	return strings.Split(input, "\n---\n")
}

func applySubstitutions(content string) string {
	replacements := map[string]string{
		`image: controller:latest`:                        `image: "{{ .Values.image.repository }}:{{ .Values.image.tag }}"`,
		`image: ghcr.io/decocms/operator:latest`:          `image: "{{ .Values.image.repository }}:{{ .Values.image.tag }}"`,
		`  replicas: 1`:                                   `  replicas: {{ .Values.replicaCount }}`,
		`namespace: operator-system`:                      `namespace: {{ .Release.Namespace }}`,
		`namespace: system`:                               `namespace: {{ .Release.Namespace }}`,
		`name: operator-controller-manager`:               `name: {{ .Release.Name }}-controller-manager`,
		`serviceAccountName: operator-controller-manager`: `serviceAccountName: {{ .Release.Name }}-controller-manager`,
	}

	for old, new := range replacements {
		content = strings.ReplaceAll(content, old, new)
	}

	// Replace operator- prefix in names - use Release.Name directly to avoid length issues
	re := regexp.MustCompile(`(?m)^  name: operator-(\w+)`)
	content = re.ReplaceAllString(content, `  name: {{ .Release.Name }}-$1`)

	// Fix issuer references in certificates
	content = strings.ReplaceAll(content, "name: operator-selfsigned-issuer", "name: {{ .Release.Name }}-selfsigned-issuer")

	// Fix service references in webhook configurations
	content = strings.ReplaceAll(content, "name: operator-webhook-service", "name: {{ .Release.Name }}-webhook-service")
	content = strings.ReplaceAll(content, "name: operator-controller-manager-metrics-service", "name: {{ .Release.Name }}-controller-manager-metrics-service")

	// Fix DNS names in certificates to match service names
	content = strings.ReplaceAll(content, "operator-webhook-service.operator-system.svc", "{{ .Release.Name }}-webhook-service.{{ .Release.Namespace }}.svc")
	content = strings.ReplaceAll(content, "operator-webhook-service.operator-system.svc.cluster.local", "{{ .Release.Name }}-webhook-service.{{ .Release.Namespace }}.svc.cluster.local")

	return content
}

func addConditionals(content, kind string) string {
	if strings.Contains(kind, "certificate") || strings.Contains(kind, "issuer") {
		return "{{- if .Values.certManager.enabled }}\n" + content + "\n{{- end }}"
	}

	if strings.Contains(kind, "webhook") {
		// Add cert-manager CA injection annotation for webhook configurations
		if strings.Contains(content, "MutatingWebhookConfiguration") || strings.Contains(content, "ValidatingWebhookConfiguration") {
			// Add annotation after metadata: labels:
			re := regexp.MustCompile(`(?m)(metadata:\s+(?:annotations:.*?\s+)?name:)`)
			if !strings.Contains(content, "annotations:") {
				content = re.ReplaceAllString(content, "metadata:\n  annotations:\n    cert-manager.io/inject-ca-from: {{ .Release.Namespace }}/{{ .Release.Name }}-serving-cert\n  name:")
			} else {
				content = strings.ReplaceAll(content, "metadata:\n  annotations:", "metadata:\n  annotations:\n    cert-manager.io/inject-ca-from: {{ .Release.Namespace }}/{{ .Release.Name }}-serving-cert")
			}
		}
		return "{{- if .Values.webhook.enabled }}\n" + content + "\n{{- end }}"
	}

	return content
}

func addEnvVarsToDeployment(templatesDir string) error {
	files, err := filepath.Glob(filepath.Join(templatesDir, "deployment-*.yaml"))
	if err != nil || len(files) == 0 {
		return fmt.Errorf("no deployment file found")
	}

	deploymentFile := files[0]
	content, err := os.ReadFile(deploymentFile)
	if err != nil {
		return err
	}

	contentStr := string(content)

	// Find the image line and add env vars after it
	envBlock := `        {{- if or (and .Values.github (or .Values.github.token .Values.github.existingSecret)) (and .Values.valkey (get .Values.valkey "sentinelUrls")) .Values.cfworkers.existingSecret .Values.cfworkers.builderImage .Values.cfworkers.artifactsBucket .Values.s3.region .Values.s3.logsBucket .Values.s3.stateBucket .Values.build.serviceAccount .Values.build.roleArn .Values.build.nodeSelector .Values.build.tolerations }}
        env:
        {{- if and .Values.github .Values.github.existingSecret }}
        - name: GITHUB_TOKEN
          valueFrom:
            secretKeyRef:
              name: {{ .Values.github.existingSecret | quote }}
              key: {{ .Values.github.existingSecretKey | default "token" | quote }}
        {{- else if and .Values.github .Values.github.token }}
        - name: GITHUB_TOKEN
          value: {{ .Values.github.token | quote }}
        {{- end }}
        {{- with .Values.valkey }}
        {{- if .sentinelUrls }}
        - name: VALKEY_SENTINEL_URLS
          value: {{ .sentinelUrls | quote }}
        - name: VALKEY_SENTINEL_MASTER_NAME
          value: {{ .sentinelMasterName | quote }}
        {{- if .existingSecret }}
        - name: VALKEY_ADMIN_PASSWORD
          valueFrom:
            secretKeyRef:
              name: {{ .existingSecret | quote }}
              key: {{ .existingSecretKey | quote }}
        {{- else if .adminPassword }}
        - name: VALKEY_ADMIN_PASSWORD
          value: {{ .adminPassword | quote }}
        {{- end }}
        {{- end }}
        {{- end }}
        {{- with .Values.cfworkers }}
        {{- if .existingSecret }}
        - name: CLOUDFLARE_API_WORKERS_TOKEN
          valueFrom:
            secretKeyRef:
              name: {{ .existingSecret | quote }}
              key: cf-api-token
        - name: CLOUDFLARE_ACCOUNT_ID
          valueFrom:
            secretKeyRef:
              name: {{ .existingSecret | quote }}
              key: cf-account-id
        {{- end }}
        {{- if .builderImage }}
        - name: CFWORKERS_BUILDER_IMAGE
          value: {{ .builderImage | quote }}
        {{- end }}
        {{- if .artifactsBucket }}
        - name: S3_ARTIFACTS_BUCKET
          value: {{ .artifactsBucket | quote }}
        {{- end }}
        {{- end }}
        {{- with .Values.s3 }}
        {{- if .region }}
        - name: S3_REGION
          value: {{ .region | quote }}
        {{- end }}
        {{- if .logsBucket }}
        - name: S3_LOGS_BUCKET
          value: {{ .logsBucket | quote }}
        {{- end }}
        {{- if .stateBucket }}
        - name: S3_STATE_BUCKET
          value: {{ .stateBucket | quote }}
        {{- end }}
        {{- end }}
        {{- with .Values.build }}
        {{- if .serviceAccount }}
        - name: BUILD_SERVICE_ACCOUNT
          value: {{ .serviceAccount | quote }}
        {{- end }}
        {{- if .roleArn }}
        - name: BUILD_ROLE_ARN
          value: {{ .roleArn | quote }}
        {{- end }}
        {{- if .nodeSelector }}
        - name: BUILD_NODE_SELECTOR
          value: {{ .nodeSelector | toJson | quote }}
        {{- end }}
        {{- if .tolerations }}
        - name: BUILD_TOLERATIONS
          value: {{ .tolerations | toJson | quote }}
        {{- end }}
        {{- end }}
        {{- end }}`

	imageLine := `        image: "{{ .Values.image.repository }}:{{ .Values.image.tag }}"`
	contentStr = strings.Replace(contentStr, imageLine, imageLine+"\n"+envBlock, 1)

	return os.WriteFile(deploymentFile, []byte(contentStr), 0644)
}

func addPodAnnotations(templatesDir string) error {
	files, err := filepath.Glob(filepath.Join(templatesDir, "deployment-*.yaml"))
	if err != nil || len(files) == 0 {
		return fmt.Errorf("no deployment file found")
	}

	deploymentFile := files[0]
	content, err := os.ReadFile(deploymentFile)
	if err != nil {
		return err
	}

	contentStr := string(content)

	annotationsBlock := `      annotations:
        kubectl.kubernetes.io/default-container: manager
        {{- with .Values.podAnnotations }}
        {{- toYaml . | nindent 8 }}
        {{- end }}`

	contentStr = strings.ReplaceAll(contentStr,
		"      annotations:\n        kubectl.kubernetes.io/default-container: manager",
		annotationsBlock)

	return os.WriteFile(deploymentFile, []byte(contentStr), 0644)
}

func addBuilderServiceAccount(templatesDir string) error {
	content := `{{- if .Values.build.serviceAccount }}
apiVersion: v1
kind: ServiceAccount
metadata:
  name: {{ .Values.build.serviceAccount }}
  namespace: {{ .Release.Namespace }}
  {{- if .Values.build.roleArn }}
  annotations:
    eks.amazonaws.com/role-arn: {{ .Values.build.roleArn | quote }}
  {{- end }}
{{- end }}
`
	return os.WriteFile(filepath.Join(templatesDir, "serviceaccount-builder.yaml"), []byte(content), 0644)
}


func addRedirectNamespace(templatesDir string) error {
	content := `{{- if (index .Values "ingress-nginx" "enabled") }}
apiVersion: v1
kind: Namespace
metadata:
  name: {{ .Values.redirect.namespace }}
{{- end }}
`
	return os.WriteFile(filepath.Join(templatesDir, "namespace-deco-redirect-system.yaml"), []byte(content), 0644)
}

func addClusterIssuer(templatesDir string) error {
	content := `{{- if .Values.redirect.clusterIssuer.enabled }}
apiVersion: cert-manager.io/v1
kind: ClusterIssuer
metadata:
  name: {{ .Values.redirect.clusterIssuer.name }}
spec:
  acme:
    {{- if .Values.redirect.clusterIssuer.staging }}
    server: https://acme-staging-v02.api.letsencrypt.org/directory
    {{- else }}
    server: https://acme-v02.api.letsencrypt.org/directory
    {{- end }}
    email: {{ required "redirect.clusterIssuer.email is required when redirect.clusterIssuer.enabled=true" .Values.redirect.clusterIssuer.email }}
    privateKeySecretRef:
      name: letsencrypt-account-key
    solvers:
      - http01:
          ingress:
            ingressClassName: {{ .Values.redirect.ingressClass }}
{{- end }}
`
	return os.WriteFile(filepath.Join(templatesDir, "clusterissuer-letsencrypt.yaml"), []byte(content), 0644)
}

func addRedirectControllerArgs(templatesDir string) error {
	files, err := filepath.Glob(filepath.Join(templatesDir, "deployment-*.yaml"))
	if err != nil || len(files) == 0 {
		return fmt.Errorf("no deployment file found")
	}

	deploymentFile := files[0]
	content, err := os.ReadFile(deploymentFile)
	if err != nil {
		return err
	}

	args := `        {{- if .Values.redirect.ingressClass }}
        - --redirect-ingress-class={{ .Values.redirect.ingressClass }}
        - --redirect-cluster-issuer={{ .Values.redirect.clusterIssuer.name }}
        {{- end }}`

	anchor := `        - --webhook-cert-path=/tmp/k8s-webhook-server/serving-certs`
	if !strings.Contains(string(content), anchor) {
		return fmt.Errorf("anchor %q not found in %s", anchor, deploymentFile)
	}
	contentStr := strings.Replace(string(content), anchor, anchor+"\n"+args, 1)
	return os.WriteFile(deploymentFile, []byte(contentStr), 0644)
}
